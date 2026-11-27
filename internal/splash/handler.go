package splash

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const mediaPrefix = "/__splash/"

// mediaCSP keeps a media URL inert even if it is opened as a document.
const mediaCSP = "default-src 'none'; frame-ancestors 'none'; sandbox"

// Handler serves imported media at /__splash/<id> and passes other paths to next.
func (s *Store) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, mediaPrefix) {
			next.ServeHTTP(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		id := strings.TrimPrefix(r.URL.Path, mediaPrefix)
		if !idPattern.MatchString(id) || s.dir == "" {
			http.NotFound(w, r)
			return
		}
		s.mu.Lock()
		c := s.load()
		s.mu.Unlock()
		var found *item
		for i := range c.Items {
			if c.Items[i].ID == id {
				found = &c.Items[i]
			}
		}
		if found == nil {
			http.NotFound(w, r)
			return
		}
		root, err := os.OpenRoot(s.dir)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer root.Close()
		file, err := root.Open(found.File)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			http.NotFound(w, r)
			return
		}
		h := w.Header()
		h.Set("Content-Type", mediaTypes[filepath.Ext(found.File)].contentType)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Content-Security-Policy", mediaCSP)
		h.Set("Cache-Control", "no-store")
		http.ServeContent(w, r, "", info.ModTime(), file)
	})
}
