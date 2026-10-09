//go:build !production

package desktopapp

import "os"

// webviewDebugArgs opens a WebView2 remote-debugging port on loopback for
// development inspection (CDP). Release builds never open one; their developer
// tools window is enough for users. Wails' own browser arguments override the
// WebView2 environment variable, so the port is passed here.
func webviewDebugArgs() []string {
	port := os.Getenv("DSH_WORK_WEBVIEW_DEBUG_PORT")
	if port == "" {
		return nil
	}
	return []string{"--remote-debugging-port=" + port}
}
