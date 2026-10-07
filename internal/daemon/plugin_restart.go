package daemon

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/local/dsh-work/internal/lifecycle"
	"github.com/local/dsh-work/internal/workerchannel"
	"github.com/local/dsh-work/internal/workeripc"
)

func isPluginRestartPath(path string) bool {
	return path == "/dsh-market/restart" || path == "/dsh-market/api/v1/restart"
}

// dshmarket's self-restart requires TCP loopback and spawns a detached helper.
// The desktop owns the Worker job and its IPC generation, so these two explicit
// plugin routes must enter the same cleanup/restart boundary as the native menu.
func servePluginRestart(w http.ResponseWriter, r *http.Request, session workerchannel.Session, path string, restart func(context.Context, string) (lifecycle.Status, error)) {
	reply := func(status int, result any) {
		if path == "/dsh-market/api/v1/restart" {
			result = map[string]any{"schema": "dsh-market/update-api/v1", "result": result}
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(result)
	}
	fail := func(status int, message string) { reply(status, map[string]string{"error": message}) }
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		fail(http.StatusMethodNotAllowed, "restart requires POST")
		return
	}
	// The dedicated WebView bridge sets this origin. The outer /worker route
	// has already checked the authenticated pipe and current generation.
	if r.Header.Get("Origin") != Origin || r.Header.Get("Forwarded") != "" || r.Header.Get("X-Forwarded-For") != "" || r.Header.Get("X-Real-IP") != "" {
		fail(http.StatusForbidden, "restart requires the desktop Worker surface")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, workeripc.Origin+"/dsh-market/status", nil)
	if err != nil {
		fail(http.StatusServiceUnavailable, "plugin restart status unavailable")
		return
	}
	request.Header.Set("Origin", workeripc.Origin)
	response, err := session.Client().Do(request)
	if err != nil {
		fail(http.StatusServiceUnavailable, "plugin restart status unavailable")
		return
	}
	defer response.Body.Close()
	var status struct {
		Boot     string  `json:"boot"`
		Busy     *bool   `json:"busy"`
		Active   bool    `json:"active"`
		Restart  bool    `json:"restart"`
		Debugger *string `json:"debugger"`
	}
	if response.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&status) != nil || status.Boot == "" || status.Busy == nil {
		fail(http.StatusServiceUnavailable, "plugin restart status unavailable")
		return
	}
	if !status.Restart || status.Debugger != nil {
		fail(http.StatusForbidden, "restart is disabled for this plugin host")
		return
	}
	if *status.Busy || status.Active {
		fail(http.StatusConflict, "cannot restart while a plugin operation is running")
		return
	}
	if _, err := restart(ctx, session.Generation()); err != nil {
		fail(http.StatusConflict, err.Error())
		return
	}
	reply(http.StatusAccepted, map[string]any{"ok": true, "boot": status.Boot})
}
