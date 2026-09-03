package notifications

import (
	"context"
	"errors"
	"strconv"
	"testing"
)

type recordingDelivery struct {
	events []Event
	err    error
}

func (d *recordingDelivery) Send(_ context.Context, event Event) error {
	d.events = append(d.events, event)
	return d.err
}

func TestDefaultPreferences(t *testing.T) {
	got := DefaultPreferences()
	want := Preferences{
		Enabled:             true,
		Completed:           true,
		InteractionRequired: true,
		Errors:              true,
		Lifecycle:           false,
	}
	if got != want {
		t.Fatalf("DefaultPreferences() = %#v, want %#v", got, want)
	}
}

func TestRouterUsesGlobalAndClassPreferences(t *testing.T) {
	delivery := &recordingDelivery{}
	router := NewRouter(delivery, func() bool { return false })

	if err := router.Publish(context.Background(), Event{ID: "error-1", Class: ClassError, Title: "Error"}); err != nil {
		t.Fatalf("Publish(error) error = %v", err)
	}
	if err := router.Publish(context.Background(), Event{ID: "status-1", Class: ClassLifecycle, Title: "Status"}); err != nil {
		t.Fatalf("Publish(lifecycle) error = %v", err)
	}
	if len(delivery.events) != 1 || delivery.events[0].ID != "error-1" {
		t.Fatalf("delivered events = %#v, want only error-1", delivery.events)
	}

	router.SetPreferences(Preferences{Enabled: true, Errors: false})
	if err := router.Publish(context.Background(), Event{ID: "error-2", Class: ClassError, Title: "Error"}); err != nil {
		t.Fatalf("Publish(disabled error) error = %v", err)
	}
	if len(delivery.events) != 1 {
		t.Fatalf("disabled error was delivered: %#v", delivery.events)
	}

	router.SetPreferences(Preferences{Enabled: false, Errors: true})
	if err := router.Publish(context.Background(), Event{ID: "error-3", Class: ClassError, Title: "Error"}); err != nil {
		t.Fatalf("Publish(disabled global) error = %v", err)
	}
	if len(delivery.events) != 1 {
		t.Fatalf("disabled global notification was delivered: %#v", delivery.events)
	}
}

func TestRouterDeliversLifecycleWhenEnabledInBackground(t *testing.T) {
	delivery := &recordingDelivery{}
	router := NewRouter(delivery, func() bool { return false })
	router.SetPreferences(Preferences{
		Enabled:   true,
		Lifecycle: true,
	})

	if err := router.Publish(context.Background(), Event{ID: "started", Class: ClassLifecycle, Title: "Started"}); err != nil {
		t.Fatalf("Publish(lifecycle) error = %v", err)
	}
	if len(delivery.events) != 1 || delivery.events[0].ID != "started" {
		t.Fatalf("lifecycle events = %#v, want started", delivery.events)
	}
}

func TestRouterSuppressesForegroundEventsUnlessDeclaredEligible(t *testing.T) {
	delivery := &recordingDelivery{}
	router := NewRouter(delivery, func() bool { return true })

	for _, event := range []Event{
		{ID: "completed-1", Class: ClassCompleted, Title: "Completed"},
		{ID: "error-1", Class: ClassError, Title: "Error"},
		{ID: "attention-1", Class: ClassActionRequired, Title: "Attention"},
	} {
		if err := router.Publish(context.Background(), event); err != nil {
			t.Fatalf("Publish(%s) error = %v", event.ID, err)
		}
	}
	if len(delivery.events) != 0 {
		t.Fatalf("foreground events = %#v, want none", delivery.events)
	}

	if err := router.Publish(context.Background(), Event{
		ID:                        "attention-2",
		Class:                     ClassActionRequired,
		Title:                     "Attention",
		AllowWhenWorkspaceFocused: true,
	}); err != nil {
		t.Fatalf("Publish(foreground eligible) error = %v", err)
	}
	if len(delivery.events) != 1 || delivery.events[0].ID != "attention-2" {
		t.Fatalf("foreground eligible events = %#v, want attention-2", delivery.events)
	}

	if err := router.Publish(context.Background(), Event{
		ID:                        "completed-2",
		Class:                     ClassCompleted,
		Title:                     "Completed",
		AllowWhenWorkspaceFocused: true,
	}); err != nil {
		t.Fatalf("Publish(eligible completed) error = %v", err)
	}
	if err := router.Publish(context.Background(), Event{
		ID:                        "lifecycle-2",
		Class:                     ClassLifecycle,
		Title:                     "Lifecycle",
		AllowWhenWorkspaceFocused: true,
	}); err != nil {
		t.Fatalf("Publish(eligible lifecycle) error = %v", err)
	}
	if len(delivery.events) != 1 {
		t.Fatalf("eligible routine events = %#v, want none", delivery.events)
	}
}

func TestRouterDeduplicatesLogicalEvents(t *testing.T) {
	delivery := &recordingDelivery{}
	router := NewRouter(delivery, func() bool { return false })
	event := Event{ID: "same", Class: ClassError, Title: "Error"}

	if err := router.Publish(context.Background(), event); err != nil {
		t.Fatalf("first Publish() error = %v", err)
	}
	if err := router.Publish(context.Background(), event); err != nil {
		t.Fatalf("second Publish() error = %v", err)
	}
	if len(delivery.events) != 1 {
		t.Fatalf("deduplicated events = %#v, want one event", delivery.events)
	}
}

func TestRouterKeepsDeduplicationWhenDeliveryFails(t *testing.T) {
	delivery := &recordingDelivery{err: errors.New("native unavailable")}
	router := NewRouter(delivery, func() bool { return false })
	event := Event{ID: "same", Class: ClassError, Title: "Error"}

	if err := router.Publish(context.Background(), event); err == nil {
		t.Fatal("first Publish() error = nil, want delivery error")
	}
	if err := router.Publish(context.Background(), event); err != nil {
		t.Fatalf("second Publish() error = %v, want deduplicated no-op", err)
	}
	if len(delivery.events) != 1 {
		t.Fatalf("failed event was retried: %#v", delivery.events)
	}
}

func TestRouterRejectsOverlongEventIDsInsteadOfTruncatingThem(t *testing.T) {
	router := NewRouter(&recordingDelivery{}, func() bool { return false })
	if err := router.Publish(context.Background(), Event{
		ID:    string(make([]rune, 161)),
		Class: ClassError,
		Title: "Error",
	}); !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("Publish(overlong ID) error = %v, want %v", err, ErrInvalidEvent)
	}
}

func TestRouterReportsDeliveryFailure(t *testing.T) {
	delivery := &recordingDelivery{err: errors.New("native unavailable")}
	router := NewRouter(delivery, func() bool { return false })
	var failed Event
	router.SetDeliveryFailureHandler(func(event Event) { failed = event })
	event := Event{ID: "error-1", Class: ClassError, Title: "Error"}

	if err := router.Publish(context.Background(), event); err == nil {
		t.Fatal("Publish() error = nil, want delivery error")
	}
	if failed.ID != event.ID {
		t.Fatalf("failure event = %#v, want %#v", failed, event)
	}
}

func TestRouterDoesNotEvictRememberedEventIDs(t *testing.T) {
	delivery := &recordingDelivery{}
	router := NewRouter(delivery, func() bool { return false })
	for index := 0; index < maxRememberedEvents; index++ {
		if err := router.Publish(context.Background(), Event{
			ID:    "event-" + strconv.Itoa(index),
			Class: ClassError,
			Title: "Error",
		}); err != nil {
			t.Fatalf("Publish(%d) error = %v", index, err)
		}
	}
	if err := router.Publish(context.Background(), Event{ID: "event-new", Class: ClassError, Title: "Error"}); err != nil {
		t.Fatalf("Publish(new event) error = %v", err)
	}
	if err := router.Publish(context.Background(), Event{ID: "event-0", Class: ClassError, Title: "Error"}); err != nil {
		t.Fatalf("Publish(replayed event) error = %v", err)
	}
	if len(delivery.events) != maxRememberedEvents {
		t.Fatalf("delivered events = %d, want %d", len(delivery.events), maxRememberedEvents)
	}
}

func TestRouterHonorsCancellationBeforeDelivery(t *testing.T) {
	delivery := &recordingDelivery{}
	router := NewRouter(delivery, func() bool { return false })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := router.Publish(ctx, Event{ID: "cancelled", Class: ClassError, Title: "Error"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Publish(cancelled) error = %v, want context.Canceled", err)
	}
	if len(delivery.events) != 0 {
		t.Fatalf("cancelled event was delivered: %#v", delivery.events)
	}
}
