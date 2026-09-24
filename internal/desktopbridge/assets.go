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

//go:embed boot.js
var bootScript string

// Assets serves a dedicated Worker-only WebView. Do not attach management
// bindings to this surface: DSH plugins share its origin.
func (b *Bridge) Assets(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/__work/web-boot" {
		var report struct {
			Generation string
			Detail     string
		}
		if r.Method != http.MethodPost || json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&report) != nil {
			http.Error(w, "invalid boot report", 400)
			return
		}
		if report.Generation != b.Generation || b.ReportBoot == nil {
			http.Error(w, "expired boot report", 409)
			return
		}
		if err := b.ReportBoot(r.Context(), report.Generation, report.Detail); err != nil {
			http.Error(w, "boot report unavailable", 503)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
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
		if b.StandardHTTP && b.ReportBoot == nil {
			w.WriteHeader(response.StatusCode)
			io.WriteString(w, string(data))
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
		injection := `<script>globalThis.__WORK_GENERATION__=` + string(generation) + `;</script>`
		if b.ReportBoot != nil {
			injection += `<script>` + bootScript + `</script>`
		}
		if !b.StandardHTTP {
			injection += `<script type="module" src="/__work/bridge.js"></script>`
		}
		body = body[:at] + injection + body[at:]
		w.WriteHeader(response.StatusCode)
		io.WriteString(w, body)
		return
	}
	w.WriteHeader(response.StatusCode)
	flush := http.NewResponseController(w).Flush
	buffer := make([]byte, 32<<10)
	for {
		n, readErr := response.Body.Read(buffer)
		if n > 0 {
			if _, writeErr := w.Write(buffer[:n]); writeErr != nil {
				return
			}
			_ = flush()
		}
		if readErr == io.EOF || readErr != nil {
			return
		}
	}
}
