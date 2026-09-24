package desktopbridge

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBootReportIsBoundToGenerationAndInjectedInBothTransports(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, "<html><head></head><body><div id=\"root\"></div></body></html>")
	}))
	defer upstream.Close()
	for _, standard := range []bool{false, true} {
		var received string
		bridge := &Bridge{Client: upstream.Client(), Origin: upstream.URL, Generation: "current", StandardHTTP: standard,
			ReportBoot: func(_ context.Context, generation, detail string) error {
				received = generation + ":" + detail
				return nil
			},
		}
		page := httptest.NewRecorder()
		bridge.Assets(page, httptest.NewRequest("GET", "/", nil))
		if !strings.Contains(page.Body.String(), "data-dsh-boot-spinner") {
			t.Fatal("missing boot observer")
		}
		for _, generation := range []string{"old", "current"} {
			result := httptest.NewRecorder()
			bridge.Assets(result, httptest.NewRequest("POST", "/__work/web-boot", strings.NewReader(`{"Generation":"`+generation+`","Detail":"settingsScope"}`)))
			if generation == "old" && (result.Code != 409 || received != "") {
				t.Fatal("accepted stale report")
			}
			if generation == "current" && (result.Code != 204 || received != "current:settingsScope") {
				t.Fatal("lost current report")
			}
		}
	}
}

func TestAssetsPreserveHEADAndRange(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/clip" {
			http.ServeContent(w, r, "clip.bin", time.Time{}, strings.NewReader("0123456789"))
			return
		}
		w.Header().Set("Content-Type", "text/html")
		if r.Method != "HEAD" {
			w.Write([]byte("<html><head></head><body>Workspace</body></html>"))
		}
	}))
	defer upstream.Close()
	bridge := &Bridge{Client: upstream.Client(), Origin: upstream.URL, Generation: "test"}
	head := httptest.NewRecorder()
	bridge.Assets(head, httptest.NewRequest("HEAD", "/", nil))
	if head.Code != 200 || head.Body.Len() != 0 {
		t.Fatalf("HEAD: status=%d body=%q", head.Code, head.Body.String())
	}
	request := httptest.NewRequest("GET", "/clip", nil)
	request.Header.Set("Range", "bytes=2-4")
	ranged := httptest.NewRecorder()
	bridge.Assets(ranged, request)
	if ranged.Code != 206 || ranged.Body.String() != "234" || ranged.Header().Get("Content-Range") != "bytes 2-4/10" {
		t.Fatalf("Range: status=%d body=%q headers=%v", ranged.Code, ranged.Body.String(), ranged.Header())
	}
}

func TestAssetsPreserveDSHComboURL(t *testing.T) {
	const resource = "/plugins/??@deepseek-ai/dsh-client-modules/client.js&rev=abc123"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.RequestURI != resource {
			http.Error(w, "invalid combo URL", 400)
			return
		}
		w.Header().Set("Content-Type", "text/javascript")
		w.Write([]byte("bootstrap"))
	}))
	defer upstream.Close()
	bridge := &Bridge{Client: upstream.Client(), Origin: upstream.URL, Generation: "test"}
	response := httptest.NewRecorder()
	bridge.Assets(response, httptest.NewRequest("GET", resource, nil))
	if response.Code != 200 || response.Body.String() != "bootstrap" {
		t.Fatalf("bootstrap request corrupted: status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestProxyHTTPUsesNormalRequestAndResponseBodies(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/echo" {
			http.Error(w, "bad request shape", http.StatusBadRequest)
			return
		}
		if r.Header.Get("Origin") != upstreamOrigin(r) || r.Header.Get("Cookie") != "" || r.Header.Get("X-DSH-Path") != "" {
			http.Error(w, "unsafe headers forwarded", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.Copy(w, r.Body)
	}))
	defer upstream.Close()
	bridge := &Bridge{Client: upstream.Client(), Origin: upstream.URL}
	request := httptest.NewRequest(http.MethodPost, "/echo?generation=stale", bytes.NewReader([]byte("payload")))
	request.Header.Set("Cookie", "secret=must-not-forward")
	request.Header.Set("X-DSH-Path", "/spoofed")
	response := httptest.NewRecorder()
	bridge.ProxyHTTP(response, request)
	if response.Code != http.StatusCreated || response.Body.String() != "payload" {
		t.Fatalf("proxy response: status=%d body=%q", response.Code, response.Body.String())
	}
}

func upstreamOrigin(r *http.Request) string {
	return "http://" + r.Host
}
