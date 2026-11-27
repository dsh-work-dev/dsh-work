package nativeui

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/local/dsh-work/internal/lifecycle"
	"github.com/local/dsh-work/internal/logging"
	"github.com/local/dsh-work/internal/notifications"
	"github.com/wailsapp/wails/v3/pkg/application"
	wailsnotifications "github.com/wailsapp/wails/v3/pkg/services/notifications"
)

// NotificationService owns the optional Wails notification adapter without
// exposing it as a WebView-bound service.
type NotificationService struct {
	service           *wailsnotifications.NotificationService
	startupFailed     atomic.Bool
	requestPermission func() (bool, error)
	permissionOnce    sync.Once
}

func NewNotificationService(service *wailsnotifications.NotificationService) *NotificationService {
	host := &NotificationService{service: service}
	if runtime.GOOS == "darwin" && service != nil {
		host.requestPermission = service.RequestNotificationAuthorization
	}
	return host
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
		logging.Module(logging.ModNotify).Warn("notification service unavailable", logging.ErrAttrs(err, lifecycle.ErrorNotificationDeliveryFailed)...)
	}
	return nil
}

func (s *NotificationService) ServiceShutdown() error {
	if s == nil || s.service == nil {
		return nil
	}
	if err := s.service.ServiceShutdown(); err != nil {
		logging.Module(logging.ModNotify).Warn("notification service shutdown failed", logging.ErrAttrs(err, "")...)
	}
	return nil
}

func (s *NotificationService) Unavailable() bool {
	return s == nil || s.startupFailed.Load()
}

// RequestNotificationAuthorization starts the system prompt after Wails has
// initialized notifications. Its timeout must never delay a settings write.
func RequestNotificationAuthorization(s *NotificationService, enabled bool, onGranted, onFailure func()) {
	if !enabled || s.Unavailable() || s.requestPermission == nil {
		return
	}
	s.permissionOnce.Do(func() {
		go func() {
			granted, err := s.requestPermission()
			if err != nil {
				logging.Module(logging.ModNotify).Warn("notification authorization failed", logging.ErrAttrs(err, lifecycle.ErrorNotificationDeliveryFailed)...)
			}
			if granted && err == nil {
				if onGranted != nil {
					onGranted()
				}
			} else if onFailure != nil {
				onFailure()
			}
		}()
	})
}

type notificationSender interface {
	SendNotification(wailsnotifications.NotificationOptions) error
	SendNotificationWithActions(wailsnotifications.NotificationOptions) error
}

type notificationDelivery struct {
	service notificationSender
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
	options := wailsnotifications.NotificationOptions{
		ID:         event.ID,
		Title:      event.Title,
		Body:       event.Body,
		CategoryID: event.CategoryID,
		Data:       notificationData(event),
	}
	var err error
	if event.CategoryID != "" {
		err = d.service.SendNotificationWithActions(options)
	} else {
		err = d.service.SendNotification(options)
	}
	if err != nil {
		return notificationDeliveryFailure()
	}
	return nil
}

func notificationData(event notifications.Event) map[string]interface{} {
	data := make(map[string]interface{}, len(event.Data)+1)
	data["target"] = event.Target
	for key, value := range event.Data {
		data[key] = value
	}
	return data
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
