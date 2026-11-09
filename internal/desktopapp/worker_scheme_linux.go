//go:build linux

package desktopapp

// WebKitGTK registers only Wails' custom URI scheme. A distinct authority
// keeps the Worker role separate from the shell's wails://localhost.
const shellWorkerScheme = "wails"
