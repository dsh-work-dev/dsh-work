package main

import (
	"context"
	"errors"
	"testing"

	"github.com/local/work/internal/lifecycle"
	worknotifications "github.com/local/work/internal/notifications"
)

func TestNativeNotificationDeliveryUsesStableFailure(t *testing.T) {
	err := (nativeNotificationDelivery{}).Send(context.Background(), worknotifications.Event{
		ID:    "event-1",
		Class: worknotifications.ClassError,
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
