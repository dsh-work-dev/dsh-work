package desktopapp

import (
	"encoding/json"

	"github.com/local/dsh-work/internal/acquisition"
	"github.com/local/dsh-work/internal/app"
	"github.com/local/dsh-work/internal/daemon"
	"github.com/local/dsh-work/internal/lifecycle"
	"github.com/local/dsh-work/internal/settings"
	"github.com/wailsapp/wails/v3/pkg/application"
)

func init() {
	application.RegisterEvent[lifecycle.Status]("lifecycle")
	application.RegisterEvent[acquisition.OperationStatus]("acquisition")
	application.RegisterEvent[settings.Locale]("locale")
	application.RegisterEvent[bool]("notification-failure")
	application.RegisterEvent[app.PetOverlayState]("pet-state")
}

func replayEvent(event daemon.Event, emit func(*application.CustomEvent) error) error {
	switch event.Name {
	case "lifecycle":
		return replayTypedEvent[lifecycle.Status](event, emit)
	case "acquisition":
		return replayTypedEvent[acquisition.OperationStatus](event, emit)
	case "locale":
		return replayTypedEvent[settings.Locale](event, emit)
	case "notification-failure":
		return replayTypedEvent[bool](event, emit)
	case "pet-state":
		return replayTypedEvent[app.PetOverlayState](event, emit)
	default:
		return nil
	}
}

func replayTypedEvent[T any](event daemon.Event, emit func(*application.CustomEvent) error) error {
	var data T
	if err := json.Unmarshal(event.Data, &data); err != nil {
		return err
	}
	return emit(&application.CustomEvent{Name: event.Name, Data: data})
}
