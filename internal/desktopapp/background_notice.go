package desktopapp

import (
	"runtime"

	"github.com/wailsapp/wails/v3/pkg/application"
)

func showBackgroundNotice(dialog *application.MessageDialog, complete func()) {
	dialog.AddButton("Ok").SetAsDefault().SetAsCancel().OnClick(complete)
	dialog.Show()
	if runtime.GOOS == "windows" {
		complete()
	}
}
