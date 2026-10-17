package app

import (
	"context"

	"github.com/local/dsh-work/internal/lifecycle"
	"github.com/local/dsh-work/internal/notifications"
	"github.com/local/dsh-work/internal/settings"
)

// SettingsService is the trusted top-level Settings surface. It exposes only
// real persisted preferences; unsupported future toggles are not represented
// as fake frontend controls. DSH manager controls share this Settings surface
// but remain a separate service boundary.
type SettingsService struct {
	manager *settings.Manager

	onLocaleChanged          func(settings.Locale)
	onNotificationsChanged   func(notifications.Preferences)
	onRuntimeRollbackChanged func(bool)
	onAppearanceChanged      func(settings.Appearance)
	onUpdateChannelChanged   func(context.Context, settings.UpdateChannel) error
	onVerboseLoggingChanged  func(bool)
}

// SetAppearanceChanged installs the publisher that pushes a saved appearance
// to every dsh-work window.
func SetAppearanceChanged(service *SettingsService, changed func(settings.Appearance)) {
	if service != nil {
		service.onAppearanceChanged = changed
	}
}

// SetUpdateChannelChanged installs the switch that moves the running updater to
// the requested channel. It runs before the choice is persisted, so a switch
// the updater refuses (for example while an update is installing) leaves the
// saved channel untouched.
func SetUpdateChannelChanged(service *SettingsService, changed func(context.Context, settings.UpdateChannel) error) {
	if service != nil {
		service.onUpdateChannelChanged = changed
	}
}

// SetVerboseLoggingChanged installs the hook that applies a saved verbose-logging choice.
func SetVerboseLoggingChanged(service *SettingsService, changed func(bool)) {
	if service != nil {
		service.onVerboseLoggingChanged = changed
	}
}

func NewSettingsService(manager *settings.Manager, onLocaleChanged func(settings.Locale), onNotificationsChanged func(notifications.Preferences), rollbackChanged ...func(bool)) *SettingsService {
	service := &SettingsService{
		manager: manager,

		onLocaleChanged:        onLocaleChanged,
		onNotificationsChanged: onNotificationsChanged,
	}
	if len(rollbackChanged) > 0 {
		service.onRuntimeRollbackChanged = rollbackChanged[0]
	}
	return service
}

func (s *SettingsService) SetAutomaticRuntimeRollback(ctx context.Context, enabled bool) (settings.Values, error) {
	if s == nil || s.manager == nil {
		return settings.Values{}, settingsUnavailable()
	}
	if !isTrustedWindow(ctx, "workspace") {
		return settings.Values{}, trustedSurfaceRequired("dsh-work settings are available only in dsh-work Settings.")
	}
	ctx, cancel := managerContext(ctx)
	defer cancel()
	values, err := s.manager.SetAutomaticRuntimeRollback(ctx, enabled)
	if err != nil {
		return settings.Values{}, err
	}
	if s.onRuntimeRollbackChanged != nil {
		s.onRuntimeRollbackChanged(values.AutomaticRuntimeRollback)
	}
	return values, nil
}

func (s *SettingsService) SetVerboseLogging(ctx context.Context, enabled bool) (settings.Values, error) {
	if s == nil || s.manager == nil {
		return settings.Values{}, settingsUnavailable()
	}
	if !isTrustedWindow(ctx, "workspace") {
		return settings.Values{}, trustedSurfaceRequired("dsh-work settings are available only in dsh-work Settings.")
	}
	ctx, cancel := managerContext(ctx)
	defer cancel()
	values, err := s.manager.SetVerboseLogging(ctx, enabled)
	if err != nil {
		return settings.Values{}, err
	}
	if s.onVerboseLoggingChanged != nil {
		s.onVerboseLoggingChanged(values.VerboseLogging)
	}
	return values, nil
}

func (s *SettingsService) GetSettings(ctx context.Context) (settings.Values, error) {
	if s == nil || s.manager == nil {
		return settings.Values{}, settingsUnavailable()
	}
	if !isTrustedWindow(ctx, "workspace") {
		return settings.Values{}, trustedSurfaceRequired("dsh-work settings are available only in dsh-work Settings.")
	}
	ctx, cancel := managerContext(ctx)
	defer cancel()
	return s.manager.Snapshot(ctx)
}

func (s *SettingsService) SetLocale(ctx context.Context, locale string) (settings.Values, error) {
	if s == nil || s.manager == nil {
		return settings.Values{}, settingsUnavailable()
	}
	if !isTrustedWindow(ctx, "workspace") {
		return settings.Values{}, trustedSurfaceRequired("dsh-work settings are available only in dsh-work Settings.")
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

func (s *SettingsService) SetNotificationPreference(ctx context.Context, key string, enabled bool) (settings.Values, error) {
	if s == nil || s.manager == nil {
		return settings.Values{}, settingsUnavailable()
	}
	if !isTrustedWindow(ctx, "workspace") {
		return settings.Values{}, trustedSurfaceRequired("dsh-work settings are available only in dsh-work Settings.")
	}
	ctx, cancel := managerContext(ctx)
	defer cancel()
	values, err := s.manager.SetNotificationPreference(ctx, notifications.PreferenceKey(key), enabled)
	if err != nil {
		return settings.Values{}, err
	}
	if s.onNotificationsChanged != nil {
		s.onNotificationsChanged(values.Notifications)
	}
	return values, nil
}

// SetAppearance saves the theme and light/dark mode of dsh-work's own windows.
// The change is published only after it is persisted.
func (s *SettingsService) SetAppearance(ctx context.Context, theme string, mode string) (settings.Values, error) {
	if s == nil || s.manager == nil {
		return settings.Values{}, settingsUnavailable()
	}
	if !isTrustedWindow(ctx, "workspace") {
		return settings.Values{}, trustedSurfaceRequired("dsh-work settings are available only in dsh-work Settings.")
	}
	ctx, cancel := managerContext(ctx)
	defer cancel()
	values, err := s.manager.SetAppearance(ctx, settings.Appearance{Theme: settings.ThemeID(theme), Mode: settings.AppearanceMode(mode)})
	if err != nil {
		return settings.Values{}, err
	}
	if s.onAppearanceChanged != nil {
		s.onAppearanceChanged(values.Appearance)
	}
	return values, nil
}

// SetUpdateChannel saves the release channel dsh-work updates from. The running
// updater switches first, so the choice of a channel that cannot be used yet is
// never saved.
func (s *SettingsService) SetUpdateChannel(ctx context.Context, channel string) (settings.Values, error) {
	if s == nil || s.manager == nil {
		return settings.Values{}, settingsUnavailable()
	}
	if !isTrustedWindow(ctx, "workspace") {
		return settings.Values{}, trustedSurfaceRequired("dsh-work settings are available only in dsh-work Settings.")
	}
	ctx, cancel := managerContext(ctx)
	defer cancel()
	saved, err := s.manager.Snapshot(ctx)
	if err != nil {
		return settings.Values{}, err
	}
	requested := settings.UpdateChannel(channel)
	if !requested.Valid() {
		return settings.Values{}, lifecycle.Failure{
			Code:          lifecycle.ErrorSettingsStateInvalid,
			Summary:       "Update channel is not supported",
			Detail:        "choose stable, beta or nightly",
			CorrelationID: lifecycle.NewCorrelationID(),
		}
	}
	if requested == saved.UpdateChannel {
		return saved, nil
	}
	if s.onUpdateChannelChanged != nil {
		if err := s.onUpdateChannelChanged(ctx, requested); err != nil {
			return settings.Values{}, err
		}
	}
	values, err := s.manager.SetUpdateChannel(ctx, requested)
	if err != nil {
		if s.onUpdateChannelChanged != nil {
			// The updater already moved; put it back on the channel that is
			// still saved.
			_ = s.onUpdateChannelChanged(ctx, saved.UpdateChannel)
		}
		return settings.Values{}, err
	}
	return values, nil
}

func settingsUnavailable() error {
	return lifecycle.Failure{
		Code:          lifecycle.ErrorSettingsUnavailable,
		Summary:       "dsh-work settings are unavailable.",
		Retryable:     true,
		CorrelationID: lifecycle.NewCorrelationID(),
		Detail:        "Restart dsh-work and try again.",
	}
}
