package desktopbridge

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

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
