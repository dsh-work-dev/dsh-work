package main

import (
	"embed"

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
	desktopapp.Run(desktopapp.Resources{
		Assets: assets, AppIcon: appIcon, TrayIcon: trayIcon,
		TrayDarkIcon: trayDarkIcon, TrayTemplateIcon: trayTemplateIcon,
	})
}
