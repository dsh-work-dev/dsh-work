package acquisition

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDSHCatalogReturnsExactVersionsTagsAndPublicationMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.EscapedPath() != "/@deepseek-ai%2Fdsh" {
			t.Fatalf("metadata path = %q", request.URL.EscapedPath())
		}
		_, _ = response.Write([]byte(`{
			"dist-tags":{"latest":"1.4.0","next":"2.0.0-beta.1"},
			"versions":{"1.2.3":{},"2.0.0-beta.1":{},"1.4.0":{},"workspace:*":{}},
			"time":{"1.4.0":"2026-08-01T00:00:00.000Z"}
		}`))
	}))
	defer server.Close()
	catalog := DSHCatalogClient{Client: server.Client(), OfficialBaseURL: server.URL, StagingRoot: t.TempDir(), Now: func() time.Time {
		return time.Date(2026, 9, 6, 2, 3, 4, 0, time.UTC)
	}}
	releases, err := catalog.Refresh(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(releases) != 3 || releases[0].Version != "2.0.0-beta.1" || releases[1].Version != "1.4.0" || len(releases[1].Tags) != 1 || releases[1].Tags[0] != "latest" {
		t.Fatalf("releases = %#v", releases)
	}
	if releases[1].PublishedAt != "2026-08-01T00:00:00.000Z" || releases[1].ObservedAt != "2026-09-06T02:03:04Z" || releases[1].Route != RouteOfficial {
		t.Fatalf("release metadata = %#v", releases[1])
	}
}

func TestDSHCatalogUsesMirrorOnlyForOfficialReachability(t *testing.T) {
	official := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer official.Close()
	mirror := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte(`{"versions":{"1.2.3":{}},"dist-tags":{"latest":"1.2.3"}}`))
	}))
	defer mirror.Close()
	releases, err := (DSHCatalogClient{Client: official.Client(), OfficialBaseURL: official.URL, MirrorBaseURL: mirror.URL, StagingRoot: t.TempDir()}).Refresh(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(releases) != 1 || releases[0].Route != RouteMirror || !releases[0].FallbackUsed {
		t.Fatalf("releases = %#v", releases)
	}
}

func TestDSHCatalogDoesNotFallbackAfterOfficialNotFound(t *testing.T) {
	official := httptest.NewServer(http.NotFoundHandler())
	defer official.Close()
	mirrorCalls := 0
	mirror := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		mirrorCalls++
		response.WriteHeader(http.StatusOK)
	}))
	defer mirror.Close()
	_, err := (DSHCatalogClient{Client: official.Client(), OfficialBaseURL: official.URL, MirrorBaseURL: mirror.URL, StagingRoot: t.TempDir()}).Refresh(context.Background())
	if err == nil {
		t.Fatal("Refresh() error = nil")
	}
	if mirrorCalls != 0 {
		t.Fatalf("mirror calls = %d", mirrorCalls)
	}
}
