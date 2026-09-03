package main

import (
	"context"
	"log"
	"sync/atomic"

	"github.com/local/work/internal/lifecycle"
	worknotifications "github.com/local/work/internal/notifications"
	worksettings "github.com/local/work/internal/settings"
	"github.com/wailsapp/wails/v3/pkg/application"
	wailsnotifications "github.com/wailsapp/wails/v3/pkg/services/notifications"
)

// nativeNotificationService starts the optional Wails adapter without binding
// its public notification methods to a WebView. Work is the only caller of
// the native delivery API; startup failure is retained so delivery can fail
// safely without making the Work shell fail to start.
type nativeNotificationService struct {
	service       *wailsnotifications.NotificationService
	startupFailed atomic.Bool
}

func (s *nativeNotificationService) ServiceName() string {
	return "Work desktop notifications"
}

func (s *nativeNotificationService) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	if s == nil || s.service == nil {
		return nil
	}
	if err := s.service.ServiceStartup(ctx, options); err != nil {
		s.startupFailed.Store(true)
		log.Printf("%s: desktop notification service unavailable", lifecycle.ErrorNotificationDeliveryFailed)
	}
	return nil
}

func (s *nativeNotificationService) ServiceShutdown() error {
	if s == nil || s.service == nil {
		return nil
	}
	if err := s.service.ServiceShutdown(); err != nil {
		log.Printf("%s: desktop notification service shutdown failed", lifecycle.ErrorNotificationDeliveryFailed)
	}
	// Desktop delivery is optional; an adapter cleanup error must not mask
	// Work's managed host shutdown.
	return nil
}

type nativeNotificationDelivery struct {
	service *wailsnotifications.NotificationService
	host    *nativeNotificationService
}

func (d nativeNotificationDelivery) Send(ctx context.Context, event worknotifications.Event) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if d.service == nil || (d.host != nil && d.host.startupFailed.Load()) {
		return notificationDeliveryFailure()
	}
	if err := d.service.SendNotification(wailsnotifications.NotificationOptions{
		ID:    event.ID,
		Title: event.Title,
		Body:  event.Body,
		Data: map[string]interface{}{
			"target": event.Target,
		},
	}); err != nil {
		return notificationDeliveryFailure()
	}
	return nil
}

type nativeNotificationCopy struct {
	title string
	body  string
}

func nativeNotificationCopyFor(locale worksettings.Locale) nativeNotificationCopy {
	copy := nativeLocaleCopyFor(locale)
	return nativeNotificationCopy{title: copy.workspaceFailureTitle, body: copy.workspaceFailureBody}
}

func nativeLifecycleNotificationCopyFor(locale worksettings.Locale, state lifecycle.State) nativeNotificationCopy {
	copy := nativeLocaleCopyFor(locale)
	return nativeNotificationCopy{title: copy.lifecycleTitles[state], body: copy.lifecycleBodies[state]}
}

func notificationDeliveryFailure() error {
	return lifecycle.Failure{
		Code:          lifecycle.ErrorNotificationDeliveryFailed,
		Summary:       "Desktop notification delivery failed.",
		Retryable:     false,
		CorrelationID: lifecycle.NewCorrelationID(),
		Detail:        "Check system notification permissions.",
	}
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}
