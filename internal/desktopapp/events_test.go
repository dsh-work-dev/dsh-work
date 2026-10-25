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
		"dsh-open-session":     `{"id":"7","sessionId":"child","parentSessionId":"parent"}`,
		"dsh-answer-approval":  `{"instance":"page-1","sessionId":"session-1","key":"approval:1","outcome":"allowed-once"}`,
	} {
		t.Run(name, func(t *testing.T) {
			data, known, err := commandData(name, json.RawMessage(raw))
			if err != nil || !known {
				t.Fatalf("commandData = %#v, known=%v, err=%v", data, known, err)
			}
			if err := processor.Emit(&application.CustomEvent{Name: name, Data: data}); err != nil {
				t.Fatal(err)
			}
			if name == "dsh-open-session" {
				command, ok := data.(OpenSessionCommand)
				if !ok || command.ID != "7" || command.SessionID != "child" || command.ParentSessionID != "parent" {
					t.Fatalf("open-session command = %#v", data)
				}
			}
			if name == "dsh-answer-approval" {
				command, ok := data.(AnswerApprovalCommand)
				if !ok || command.Instance != "page-1" || command.SessionID != "session-1" || command.Key != "approval:1" || command.Outcome != "allowed-once" {
					t.Fatalf("answer-approval command = %#v", data)
				}
			}
		})
	}
}
