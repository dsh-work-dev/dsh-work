package desktopapp

import (
	"encoding/json"
	"testing"

	"github.com/local/dsh-work/internal/acquisition"
	"github.com/local/dsh-work/internal/app"
	"github.com/local/dsh-work/internal/daemon"
	"github.com/local/dsh-work/internal/lifecycle"
	"github.com/local/dsh-work/internal/settings"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// Exercise the production JSON hop against Wails' actual registered-event
// validator: decoding to map/string silently cancels typed native events.
func TestDaemonEventsSurviveWailsValidation(t *testing.T) {
	processor := application.NewWailsEventProcessor(func(*application.CustomEvent) {})
	for name, data := range map[string]any{
		"lifecycle":            lifecycle.Status{State: lifecycle.StateReady},
		"acquisition":          acquisition.OperationStatus{},
		"locale":               settings.LocaleJapanese,
		"notification-failure": true,
		"pet-state":            app.PetOverlayState{},
	} {
		t.Run(name, func(t *testing.T) {
			raw, err := json.Marshal(data)
			if err != nil {
				t.Fatal(err)
			}
			if err := replayEvent(daemon.Event{Name: name, Data: raw}, processor.Emit); err != nil {
				t.Fatal(err)
			}
		})
	}
}
