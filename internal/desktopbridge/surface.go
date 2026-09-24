package desktopbridge

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// Surface is a permanent Worker window role. Native-stamped IDs select the
// boundary before Wails dispatches runtime calls or serves application files.
type Surface struct {
	Window       func() application.Window
	Current      func() *Bridge
	Assets       func(*Bridge) http.Handler
	StandardHTTP bool
}

func (s *Surface) forWindow(window application.Window) *Bridge {
	own := s.Window()
	if own == nil || window == nil || window.ID() != own.ID() {
		return nil
	}
	return s.Current()
}
func (s *Surface) Fetch(c *application.StreamConn) {
	if b := s.forWindow(c.Window()); b != nil {
		_ = b.Serve(c)
	}
}
func (s *Surface) WebSocket(c *application.StreamConn) {
	if b := s.forWindow(c.Window()); b != nil {
		_ = b.WebSocket(c)
	}
}
func (s *Surface) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		own := s.Window()
		if own == nil || r.Header.Get("x-wails-window-id") != strconv.FormatUint(uint64(own.ID()), 10) {
			next.ServeHTTP(w, r)
			return
		}
		if r.Header.Get("Service-Worker") == "script" || r.Header.Get("Sec-Fetch-Dest") == "serviceworker" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/wails/") {
			switch r.URL.Path {
			case "/wails/runtime.js", "/wails/custom.js", "/wails/stream/send", "/wails/stream/poll":
				next.ServeHTTP(w, r)
			default:
				http.Error(w, "forbidden", http.StatusForbidden)
			}
			return
		}

		bridge := s.Current()
		if bridge == nil {
			http.Error(w, "Worker unavailable", http.StatusServiceUnavailable)
			return
		}
		if generation := r.URL.Query().Get("generation"); generation != "" && generation != bridge.Generation {
			http.Error(w, "Worker expired", http.StatusGone)
			return
		}
		if s.StandardHTTP && r.URL.Path != "/__work/web-boot" && r.Method != http.MethodGet && r.Method != http.MethodHead {
			bridge.ProxyHTTP(w, r)
			return
		}
		if s.Assets != nil {
			s.Assets(bridge).ServeHTTP(w, r)
		} else {
			bridge.Assets(w, r)
		}
	})
}
