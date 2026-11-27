//go:build linux || darwin

package desktopapp

// Wails registers its custom scheme in WebKitGTK and WKWebView.
// The Worker authority stays separate from the shell's wails://localhost.
const shellWorkerScheme = "wails"
