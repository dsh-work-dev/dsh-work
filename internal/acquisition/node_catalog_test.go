package acquisition

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNodeCatalogResolvesLatestStableExactWindowsArtifact(t *testing.T) {
	digest := strings.Repeat("a", 64)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/index.json":
			_, _ = response.Write([]byte(`[
				{"version":"v27.0.0-rc.1","files":["win-x64-zip"]},
				{"version":"v26.1.0","files":["win-arm64-zip"]},
				{"version":"v23.11.0","files":["win-x64-zip"]}
			]`))
		case "/v23.11.0/SHASUMS256.txt":
			_, _ = response.Write([]byte(digest + "  node-v23.11.0-win-x64.zip\n"))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	catalog := NodeCatalogClient{
		Client: server.Client(), OfficialBaseURL: server.URL + "/",
		Platform: "windows", Architecture: "amd64", StagingRoot: t.TempDir(),
		Now: func() time.Time { return time.Date(2026, 9, 6, 1, 2, 3, 0, time.UTC) },
	}
	release, err := catalog.RefreshLatest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if release.Version != "v23.11.0" || release.Filename != "node-v23.11.0-win-x64.zip" || release.SHA256 != digest {
		t.Fatalf("release = %#v", release)
	}
	if release.Route != RouteOfficial || release.FallbackUsed || release.ObservedAt != "2026-09-06T01:02:03Z" {
		t.Fatalf("provenance = %#v", release)
	}
}

func TestNodeCatalogUsesMirrorOnlyAfterOfficialReachabilityFailure(t *testing.T) {
	digest := strings.Repeat("b", 64)
	official := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer official.Close()
	mirror := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/index.json" {
			_, _ = response.Write([]byte(`[{"version":"v26.0.0","files":["win-x64-zip"]}]`))
			return
		}
		if request.URL.Path == "/v26.0.0/SHASUMS256.txt" {
			_, _ = response.Write([]byte(digest + "  node-v26.0.0-win-x64.zip\n"))
			return
		}
		http.NotFound(response, request)
	}))
	defer mirror.Close()

	catalog := NodeCatalogClient{
		Client: official.Client(), OfficialBaseURL: official.URL + "/", MirrorBaseURL: mirror.URL + "/",
		Platform: "windows", Architecture: "amd64", StagingRoot: t.TempDir(),
	}
	release, err := catalog.RefreshLatest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if release.Route != RouteMirror || !release.FallbackUsed {
		t.Fatalf("release provenance = %#v", release)
	}
}

func TestNodeCatalogDoesNotFallbackAfterOfficialNotFound(t *testing.T) {
	official := httptest.NewServer(http.NotFoundHandler())
	defer official.Close()
	mirrorCalls := 0
	mirror := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		mirrorCalls++
		response.WriteHeader(http.StatusOK)
	}))
	defer mirror.Close()

	catalog := NodeCatalogClient{
		Client: official.Client(), OfficialBaseURL: official.URL + "/", MirrorBaseURL: mirror.URL + "/",
		Platform: "windows", Architecture: "amd64", StagingRoot: t.TempDir(),
	}
	if _, err := catalog.RefreshLatest(context.Background()); err == nil {
		t.Fatal("RefreshLatest() error = nil")
	}
	if mirrorCalls != 0 {
		t.Fatalf("mirror calls = %d, want 0", mirrorCalls)
	}
}
