package app

import (
	"context"

	"github.com/local/work/internal/lifecycle"
	"github.com/local/work/internal/settings"
)

// SettingsService is the trusted top-level Settings surface. It exposes only
// real persisted preferences; unsupported future toggles are not represented
// as fake frontend controls. DSH manager controls share this Settings window
// but remain a separate service boundary.
type SettingsService struct {
	manager              *settings.Manager
	onCloseToTrayChanged func(bool)
	onLocaleChanged      func(settings.Locale)
}

func NewSettingsService(manager *settings.Manager, onCloseToTrayChanged func(bool), onLocaleChanged func(settings.Locale)) *SettingsService {
	return &SettingsService{
		manager:              manager,
		onCloseToTrayChanged: onCloseToTrayChanged,
		onLocaleChanged:      onLocaleChanged,
	}
}

func (s *SettingsService) GetSettings(ctx context.Context) (settings.Values, error) {
	if s == nil || s.manager == nil {
		return settings.Values{}, settingsUnavailable()
	}
	if !isTrustedWindow(ctx, "settings") {
		return settings.Values{}, trustedSurfaceRequired("Work settings are available only in the Settings window.")
	}
	ctx, cancel := managerContext(ctx)
	defer cancel()
	return s.manager.Snapshot(ctx)
}

func (s *SettingsService) SetCloseToTray(ctx context.Context, enabled bool) (settings.Values, error) {
	if s == nil || s.manager == nil {
		return settings.Values{}, settingsUnavailable()
	}
	if !isTrustedWindow(ctx, "settings") {
		return settings.Values{}, trustedSurfaceRequired("Work settings are available only in the Settings window.")
	}
	ctx, cancel := managerContext(ctx)
	defer cancel()
	values, err := s.manager.SetCloseToTray(ctx, enabled)
	if err != nil {
		return settings.Values{}, err
	}
	if s.onCloseToTrayChanged != nil {
		s.onCloseToTrayChanged(values.CloseToTray)
	}
	return values, nil
}

func (s *SettingsService) SetLocale(ctx context.Context, locale string) (settings.Values, error) {
	if s == nil || s.manager == nil {
		return settings.Values{}, settingsUnavailable()
	}
	if !isTrustedWindow(ctx, "settings") {
		return settings.Values{}, trustedSurfaceRequired("Work settings are available only in the Settings window.")
	}
	ctx, cancel := managerContext(ctx)
	defer cancel()
	values, err := s.manager.SetLocale(ctx, settings.Locale(locale))
	if err != nil {
		return settings.Values{}, err
	}
	if s.onLocaleChanged != nil {
		s.onLocaleChanged(values.Locale)
	}
	return values, nil
}

func settingsUnavailable() error {
	return lifecycle.Failure{
		Code:          lifecycle.ErrorSettingsUnavailable,
		Summary:       "Work settings are unavailable.",
		Retryable:     true,
		CorrelationID: lifecycle.NewCorrelationID(),
		Detail:        "Restart Work and try again.",
	}
}
