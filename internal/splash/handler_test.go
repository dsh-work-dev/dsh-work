package splash

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerServesImportedMedia(t *testing.T) {
	store, src := newTestStore(t)
	imported, err := store.Import(writeFile(t, src, "mark.svg", 2048))
	if err != nil {
		t.Fatal(err)
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(299) })
	h := store.Handler(next)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, imported.URL, nil))
	if rec.Code != http.StatusOK || rec.Body.Len() != 2048 {
		t.Fatalf("status %d len %d", rec.Code, rec.Body.Len())
	}
	headers := rec.Header()
	if headers.Get("Content-Type") != "image/svg+xml" || headers.Get("X-Content-Type-Options") != "nosniff" ||
		headers.Get("Content-Security-Policy") != mediaCSP || headers.Get("Cache-Control") != "no-store" {
		t.Fatalf("headers %v", headers)
	}

	rangeReq := httptest.NewRequest(http.MethodGet, imported.URL, nil)
	rangeReq.Header.Set("Range", "bytes=0-9")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, rangeReq)
	if rec.Code != http.StatusPartialContent || rec.Body.Len() != 10 {
		t.Fatalf("range: %d %d", rec.Code, rec.Body.Len())
	}
}

func TestHandlerRejections(t *testing.T) {
	store, _ := newTestStore(t)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(299) })
	h := store.Handler(next)
	cases := map[string]int{
		"/__splash/" + strings.Repeat("a", 32): http.StatusNotFound,
		"/__splash/NOTHEX":                     http.StatusNotFound,
		"/__splash/../catalog.json":            http.StatusNotFound,
		"/__splash/":                           http.StatusNotFound,
		"/index.html":                          299,
	}
	for path, want := range cases {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != want {
			t.Fatalf("%s: got %d want %d", path, rec.Code, want)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/__splash/"+strings.Repeat("a", 32), nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("post: %d", rec.Code)
	}
}
