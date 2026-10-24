package desktopapp

import (
	"github.com/local/dsh-work/internal/acquisition"
	"github.com/local/dsh-work/internal/app"
	"github.com/local/dsh-work/internal/daemon"
	"github.com/local/dsh-work/internal/desktopclient"
	"github.com/local/dsh-work/internal/lifecycle"
	"github.com/local/dsh-work/internal/settings"
	"github.com/wailsapp/wails/v3/pkg/application"
)

type OpenSessionCommand struct {
	ID              string `json:"id"`
	SessionID       string `json:"sessionId"`
	ParentSessionID string `json:"parentSessionId"`
}

type AnswerApprovalCommand struct {
	Instance  string `json:"instance"`
	SessionID string `json:"sessionId"`
	Key       string `json:"key"`
	Outcome   string `json:"outcome"`
}

func init() {
	application.RegisterEvent[lifecycle.Status]("lifecycle")
	application.RegisterEvent[string]("worker-url")
	application.RegisterEvent[string]("settings-section")
	application.RegisterEvent[struct{}]("settings-close")
	application.RegisterEvent[acquisition.OperationStatus]("acquisition")
	application.RegisterEvent[settings.Locale]("locale")
	application.RegisterEvent[settings.Appearance]("appearance")
	application.RegisterEvent[[]desktopclient.BuiltinComponentStatus]("builtin-components")
	application.RegisterEvent[bool]("notification-failure")
	application.RegisterEvent[OpenSessionCommand]("dsh-open-session")
	application.RegisterEvent[AnswerApprovalCommand]("dsh-answer-approval")
	application.RegisterEvent[app.PetOverlayState]("pet-state")
	application.RegisterEvent[daemon.UpdateSnapshot]("update-state")
	application.RegisterEvent[bool]("background-stopping")
}
