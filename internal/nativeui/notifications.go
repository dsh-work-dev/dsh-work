package nativeui

import (
	"context"
	"log"
	"sync/atomic"

	"github.com/local/dsh-work/internal/lifecycle"
	"github.com/local/dsh-work/internal/notifications"
	"github.com/wailsapp/wails/v3/pkg/application"
	wailsnotifications "github.com/wailsapp/wails/v3/pkg/services/notifications"
)

// NotificationService owns the optional Wails notification adapter without
// exposing it as a WebView-bound service.
type NotificationService struct {
	service       *wailsnotifications.NotificationService
	startupFailed atomic.Bool
}

func NewNotificationService(service *wailsnotifications.NotificationService) *NotificationService {
	return &NotificationService{service: service}
}

func (s *NotificationService) ServiceName() string {
	return "dsh-work desktop notifications"
}

func (s *NotificationService) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	if s == nil || s.service == nil {
		return nil
	}
	if err := s.service.ServiceStartup(ctx, options); err != nil {
		s.startupFailed.Store(true)
		log.Printf("%s: desktop notification service unavailable", lifecycle.ErrorNotificationDeliveryFailed)
	}
	return nil
}

func (s *NotificationService) ServiceShutdown() error {
	if s == nil || s.service == nil {
		return nil
	}
	if err := s.service.ServiceShutdown(); err != nil {
		log.Printf("%s: desktop notification service shutdown failed", lifecycle.ErrorNotificationDeliveryFailed)
	}
	return nil
}

func (s *NotificationService) Unavailable() bool {
	return s == nil || s.startupFailed.Load()
}

type notificationDelivery struct {
	service *wailsnotifications.NotificationService
	host    *NotificationService
}

func NewNotificationDelivery(service *wailsnotifications.NotificationService, host *NotificationService) notifications.Delivery {
	return notificationDelivery{service: service, host: host}
}

func (d notificationDelivery) Send(ctx context.Context, event notifications.Event) error {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	if d.service == nil || d.host.Unavailable() {
		return notificationDeliveryFailure()
	}
	if err := d.service.SendNotification(wailsnotifications.NotificationOptions{
		ID:    event.ID,
		Title: event.Title,
		Body:  event.Body,
		Data:  map[string]interface{}{"target": event.Target},
	}); err != nil {
		return notificationDeliveryFailure()
	}
	return nil
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
