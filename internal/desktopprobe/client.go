package desktopprobe

import (
	"encoding/json"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// InstallClient exercises the real Worker WebView through both process hops,
// writes evidence, then closes the actual native window. The background owner
// is deliberately left running for the lifecycle probe to inspect and reopen.
// A shell-framed Worker reports again after each new generation; later reports
// land in numbered files beside the first.
func InstallClient(app *application.App, window application.Window, path string) {
	var mu sync.Mutex
	reports := 0
	finish := func(data map[string]any) {
		mu.Lock()
		defer mu.Unlock()
		reports++
		data["uiPID"] = os.Getpid()
		data["timestamp"] = time.Now().UTC().Format(time.RFC3339)
		raw, _ := json.MarshalIndent(data, "", "  ")
		if reports > 1 {
			_ = os.WriteFile(path+"."+strconv.Itoa(reports), raw, 0600)
			return
		}
		_ = os.WriteFile(path, raw, 0600)
		go window.Close()
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
		mu.Lock()
		reported := reports > 0
		mu.Unlock()
		if !reported {
			finish(map[string]any{"ok": false, "failure": "WebView probe timed out"})
		}
	}()
}
