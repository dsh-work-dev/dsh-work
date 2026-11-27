package desktopapp

import "testing"

func TestPermissionPresentationReadinessDoesNotSurviveRequestOrHide(t *testing.T) {
	p := &browserPermissionWindow{}
	p.setRequestID("first")
	if p.markRendered("first") {
		t.Fatal("hidden window acquired ready content")
	}
	p.shown = true
	if !p.markRendered("first") || p.markRendered("first") {
		t.Fatal("current content readiness was not recorded once")
	}
	p.setRequestID("next")
	if p.renderedID != "" || p.markRendered("first") || !p.markRendered("next") {
		t.Fatal("late old request report changed replacement readiness")
	}
	p.setRequestID("")
	p.shown = false
	if p.renderedID != "" || p.markRendered("next") {
		t.Fatal("hidden/closed request retained ready content")
	}
}
