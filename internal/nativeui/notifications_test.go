package nativeui

import (
	"context"
	"errors"
	"testing"

	"github.com/local/dsh-work/internal/lifecycle"
	"github.com/local/dsh-work/internal/notifications"
)

func TestNotificationDeliveryUsesStableFailure(t *testing.T) {
	err := NewNotificationDelivery(nil, nil).Send(context.Background(), notifications.Event{
		ID:    "event-1",
		Class: notifications.ClassError,
		Title: "Error",
	})
	var failure lifecycle.Failure
	if !errors.As(err, &failure) {
		t.Fatalf("Send() error = %v, want lifecycle failure", err)
	}
	if failure.Code != lifecycle.ErrorNotificationDeliveryFailed || failure.Detail == "" {
		t.Fatalf("failure = %#v, want stable code and actionable detail", failure)
	}
}
