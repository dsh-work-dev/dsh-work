package desktopclient

import (
	"context"
	"testing"

	"github.com/local/dsh-work/internal/embeddedbrowser"
	"github.com/wailsapp/wails/v3/pkg/application"
)

type overlayTestWindow struct{ application.Window }

func (overlayTestWindow) Name() string { return "workspace" }

func TestOccludedDocumentRejectsBrowserOverlayCommands(t *testing.T) {
	ctx := context.WithValue(context.Background(), application.WindowKey, overlayTestWindow{})
	service := &EmbeddedBrowserService{Enabled: true}
	gate := service.gate("workspace")
	gate.document = "active-document"
	gate.blocked = true
	const slot = "session/tab"
	dialog := embeddedbrowser.WebDialog{View: "owned-view"}
	for _, ready := range []bool{false, true} {
		if err := service.PresentDialog(ctx, gate.document, slot, dialog, ready); err == nil || err.Error() != "CEF_DIALOG_AUTHORIZATION_CHANGED" {
			t.Fatalf("occluded dialog presentation ready=%v returned %v", ready, err)
		}
	}
	if err := service.AnswerDialog(ctx, gate.document, slot, dialog, true, "fixture"); err == nil || err.Error() != "CEF_DIALOG_AUTHORIZATION_CHANGED" {
		t.Fatalf("occluded dialog answer returned %v", err)
	}
	for _, action := range []string{"suspend", "ready", "allow", "deny"} {
		if url, err := service.Popup(ctx, gate.document, slot, embeddedbrowser.WebPopup{View: "owned-view"}, action); url != "" || err == nil || err.Error() != "CEF_POPUP_AUTHORIZATION_CHANGED" {
			t.Fatalf("occluded popup %s returned %q, %v", action, url, err)
		}
	}
}
