package desktopprobe

import (
	"encoding/json"
	"os"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// InstallClient exercises the real Worker WebView through both process hops,
// writes evidence, then closes the actual native window. The background owner
// is deliberately left running for the lifecycle probe to inspect and reopen.
func InstallClient(app *application.App, window application.Window, path string) {
	var once sync.Once
	finish := func(data map[string]any) {
		once.Do(func() {
			data["uiPID"] = os.Getpid()
			data["timestamp"] = time.Now().UTC().Format(time.RFC3339)
			raw, _ := json.MarshalIndent(data, "", "  ")
			_ = os.WriteFile(path, raw, 0600)
			go window.Close()
		})
	}
	app.HandleStream("probe-report", func(c *application.StreamConn) {
		if c.Window() == nil || c.Window().ID() != window.ID() {
			return
		}
		raw, err := c.Receive()
		if err != nil || len(raw) > 32<<10 {
			return
		}
		var data map[string]any
		if json.Unmarshal(raw, &data) == nil {
			finish(data)
		}
	})
	go func() {
		time.Sleep(150 * time.Second)
		finish(map[string]any{"ok": false, "failure": "WebView probe timed out"})
	}()
}
