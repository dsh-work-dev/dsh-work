package main

import (
	"context"
	"errors"
	"testing"

	"github.com/local/dsh-work/internal/lifecycle"
	dshworknotifications "github.com/local/dsh-work/internal/notifications"
)

func TestNativeNotificationDeliveryUsesStableFailure(t *testing.T) {
	err := (nativeNotificationDelivery{}).Send(context.Background(), dshworknotifications.Event{
		ID:    "event-1",
		Class: dshworknotifications.ClassError,
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
