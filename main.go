package main

import (
	"embed"
	"runtime"

	"github.com/local/dsh-work/internal/desktopapp"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed frontend/public/branding/dsh-work-appicon.png
var appIcon []byte

//go:embed frontend/public/branding/dsh-work-tray.png
var trayIcon []byte

//go:embed frontend/public/branding/dsh-work-tray-dark.png
var trayDarkIcon []byte

//go:embed frontend/public/branding/dsh-work-tray-template.png
var trayTemplateIcon []byte

func main() {
	if supervisorBootstrap() {
		return
	}
	icon := appIcon
	if runtime.GOOS == "darwin" {
		// AppKit loads icons.icns from the bundle. Wails would otherwise
		// replace it with the full-bleed icon shared by the other platforms.
		icon = nil
	}
	desktopapp.Run(desktopapp.Resources{
		Assets: assets, AppIcon: icon, TrayIcon: trayIcon,
		TrayDarkIcon: trayDarkIcon, TrayTemplateIcon: trayTemplateIcon,
	})
}
