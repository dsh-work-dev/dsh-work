//go:build !windows

package nativeui

import "github.com/wailsapp/wails/v3/pkg/application"

func ShowComputerOverlay(w *application.WebviewWindow, x, y int, pointer bool) {
	w.SetPosition(x, y)
	w.Show()
}

func ShowComputerPointer(w *application.WebviewWindow, x, y float64, target uint64, hotspotX, hotspotY float64) {
	ShowComputerOverlay(w, int(x-hotspotX), int(y-hotspotY), true)
}
