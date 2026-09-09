package nativeui

import "github.com/wailsapp/wails/v3/pkg/application"

// Sprite preferences retain their canonical dimensions. Text remains legible
// even when the pet is set to 50%; the activity region never scales with sprites.
func PetActivityWindowSize(width, height int) (int, int) {
	return max(width, 240), height + 64
}

func PetWindowOptions() application.WebviewWindowOptions {
	return application.WebviewWindowOptions{
		Name:        "pet",
		Title:       "dsh-work Pet",
		Width:       240,
		Height:      168,
		AlwaysOnTop: false,
		Frameless:   true,
		// The Settings size slider is the sole resize control. Keeping the
		// native border disabled prevents a second, unsaved resize path.
		DisableResize:    true,
		BackgroundType:   application.BackgroundTypeTransparent,
		BackgroundColour: application.NewRGBA(0, 0, 0, 0),
		URL:              "/?surface=pet",
		InitialPosition:  application.WindowXY,
		X:                0,
		Y:                0,
		Hidden:           true,
		// The overlay must receive hover and drag input for its explicit handle.
		IgnoreMouseEvents: false,
		Mac: application.MacWindow{
			Backdrop: application.MacBackdropTransparent,
		},
		Windows: application.WindowsWindow{
			HiddenOnTaskbar:                   true,
			DisableFramelessWindowDecorations: true,
		},
	}
}
