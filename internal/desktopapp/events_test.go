package desktopapp

import (
	"encoding/json"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
)

func TestOneShotCommandPayloadsSurviveWailsValidation(t *testing.T) {
	processor := application.NewWailsEventProcessor(func(*application.CustomEvent) {})
	for name, raw := range map[string]string{
		"notification-failure": `true`,
		"background-stopping":  `true`,
	} {
		t.Run(name, func(t *testing.T) {
			data, known, err := commandData(name, json.RawMessage(raw))
			if err != nil || !known {
				t.Fatalf("commandData = %#v, known=%v, err=%v", data, known, err)
			}
			if err := processor.Emit(&application.CustomEvent{Name: name, Data: data}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
