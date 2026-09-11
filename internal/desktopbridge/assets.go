package desktopbridge

import (
	_ "embed"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

//go:embed assets/client.js
var clientScript string

// Assets serves a dedicated Worker-only WebView. Do not attach management
// bindings to this surface: DSH plugins share its origin.
func (b *Bridge) Assets(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" && r.Method != "HEAD" {
		http.Error(w, "method not allowed", 405)
		return
	}
	if r.URL.Path == "/__work/bridge.js" {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		if r.Method != "HEAD" {
			io.WriteString(w, clientScript)
		}
		return
	}
	u := *r.URL
	u.Scheme = ""
	u.Host = ""
	// Only the navigation URL carries our generation. DSH resource queries
	// include opaque combo syntax (??package/client.js) and must stay intact.
	if u.Path == "/" {
		query := u.Query()
		query.Del("generation")
		u.RawQuery = query.Encode()
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, b.Origin+u.String(), nil)
	if err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	for _, name := range []string{"Accept", "Accept-Language", "Range", "If-Range", "If-None-Match", "If-Modified-Since"} {
		if value := r.Header.Get(name); value != "" {
			req.Header.Set(name, value)
		}
	}
	req.Header.Set("Origin", b.Origin)
	req.Header.Set("Accept-Encoding", "identity")
	response, err := b.Client.Do(req)
	if err != nil {
		http.Error(w, "Worker unavailable", 502)
		return
	}
	defer response.Body.Close()
	for key, values := range response.Header {
		if strings.EqualFold(key, "Set-Cookie") || strings.EqualFold(key, "Content-Length") {
			continue
		}
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == "GET" && response.StatusCode == http.StatusOK && strings.Contains(response.Header.Get("Content-Type"), "text/html") {
		data, err := io.ReadAll(io.LimitReader(response.Body, 4<<20+1))
		if err != nil || len(data) > 4<<20 {
			http.Error(w, "index too large", 502)
			return
		}
		body := string(data)
		at := strings.Index(strings.ToLower(body), "<head>")
		if at < 0 {
			http.Error(w, "invalid index", 502)
			return
		}
		at += len("<head>")
		generation, _ := json.Marshal(b.Generation)
		body = body[:at] + `<script>globalThis.__WORK_GENERATION__=` + string(generation) + `;</script><script type="module" src="/__work/bridge.js"></script>` + body[at:]
		w.WriteHeader(response.StatusCode)
		io.WriteString(w, body)
		return
	}
	w.WriteHeader(response.StatusCode)
	io.Copy(w, response.Body)
}
