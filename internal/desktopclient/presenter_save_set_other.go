//go:build !linux || !cgo || !e67presenter || server

package desktopclient

import "github.com/wailsapp/wails/v3/pkg/application"

// Only the Linux native-child presenter needs a UI X-client save set. Existing
// Windows/macOS and unavailable presenters retain their original contract.
func protectPresenterWindow(application.Window) error { return nil }
