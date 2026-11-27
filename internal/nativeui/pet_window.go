package nativeui

import (
	"math"
	"runtime"

	"github.com/local/dsh-work/internal/pet"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// PetScreenWorkArea uses the coordinate space of Window.Position and
// Window.SetPosition. Wails' Darwin Window.GetScreen returns the raw screen
// snapshot, while ScreenManager returns a layout with adjusted logical bounds.
func PetScreenWorkArea(screen *application.Screen) pet.WorkArea {
	area := screen.WorkArea
	if runtime.GOOS == "darwin" && screen.ScaleFactor > 0 && screen.PhysicalWorkArea.Width > 0 && screen.PhysicalWorkArea.Height > 0 {
		physical := screen.PhysicalWorkArea
		points := func(pixels int) int { return int(math.Round(float64(pixels) / float64(screen.ScaleFactor))) }
		area = application.Rect{X: points(physical.X), Y: points(physical.Y), Width: points(physical.Width), Height: points(physical.Height)}
	}
	return pet.WorkArea{X: area.X, Y: area.Y, Width: area.Width, Height: area.Height}
}

// Sprite preferences retain their canonical dimensions. Text remains legible
// even when the pet is set to 50%; the activity region never scales with sprites.
func PetActivityWindowSize(width, height int) (int, int) {
	return max(width, 240), height + 64
}

func PetWindowOptions() application.WebviewWindowOptions {
	return application.WebviewWindowOptions{
		Name:        "pet",
		Title:       "DSH Work Pet",
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
