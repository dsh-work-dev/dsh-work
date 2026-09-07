package settings

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/local/dsh-work/internal/lifecycle"
	"github.com/local/dsh-work/internal/notifications"
)

const stateVersion = 2

// Locale is the dsh-work-owned language preference. It is deliberately separate
// from DSH's appearance preference: DSH owns theme, while dsh-work owns its own
// chrome and settings copy.
type Locale string

const (
	LocaleEnglish  Locale = "en"
	LocaleChinese  Locale = "zh-CN"
	LocaleJapanese Locale = "ja-JP"
	DefaultLocale         = LocaleChinese
)

func (l Locale) Valid() bool {
	return l == LocaleEnglish || l == LocaleChinese || l == LocaleJapanese
}

// Values is the versioned, platform-neutral dsh-work preference contract.
// CloseToTray is true by default so closing the last window keeps dsh-work and
// its managed DSH worker available from the notification area.
type Values struct {
	Version                  int                       `json:"version"`
	CloseToTray              bool                      `json:"closeToTray"`
	AutomaticRuntimeRollback bool                      `json:"automaticRuntimeRollback"`
	Locale                   Locale                    `json:"locale"`
	Notifications            notifications.Preferences `json:"notifications"`
}

func DefaultValues() Values {
	return Values{
		Version:                  stateVersion,
		CloseToTray:              true,
		AutomaticRuntimeRollback: true,
		Locale:                   DefaultLocale,
		Notifications:            notifications.DefaultPreferences(),
	}
}

func (m *Manager) SetAutomaticRuntimeRollback(ctx context.Context, enabled bool) (Values, error) {
	if err := contextError(ctx); err != nil {
		return Values{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	next := m.values
	next.AutomaticRuntimeRollback = enabled
	next.Version = stateVersion
	if err := m.store.Save(ctx, m.path, next); err != nil {
		return Values{}, err
	}
	m.values = next
	return next, nil
}

// Store owns persistence mechanics. Settings policy remains independent of
// the filesystem so contract tests and a future protected store can be used
// without changing the Host or frontend Interface.
type Store interface {
	Load(context.Context, string) (*Values, error)
	Save(context.Context, string, Values) error
}

// FileReplacer is the small platform seam needed to publish a completed
// settings file over an existing one. POSIX uses os.Rename; Windows supplies
// its native replace primitive from internal/platform/windows.
type FileReplacer interface {
	Replace(source, destination string) error
}

type Config struct {
	Path     string
	Store    Store
	Replacer FileReplacer
}

// Manager owns the small global preference set and serializes writes. This is
// intentionally a versioned app-settings Module, not a second DSH catalog.
type Manager struct {
	mu     sync.RWMutex
	path   string
	store  Store
	values Values
}

func New(config Config) (*Manager, error) {
	if config.Store == nil {
		config.Store = FileStore{Replacer: config.Replacer}
	}
	path, err := normalizePath(config.Path)
	if err != nil {
		return nil, err
	}
	loaded, err := config.Store.Load(context.Background(), path)
	if err != nil {
		return nil, err
	}
	values := DefaultValues()
	if loaded != nil {
		if loaded.Version != stateVersion {
			return nil, failure(lifecycle.ErrorSettingsStateInvalid, "dsh-work settings are invalid", "the persisted settings use an unsupported format")
		}
		values = *loaded
		if !values.Locale.Valid() {
			values.Locale = DefaultLocale
		}
	}
	return &Manager{path: path, store: config.Store, values: values}, nil
}

func (m *Manager) Snapshot(ctx context.Context) (Values, error) {
	if err := contextError(ctx); err != nil {
		return Values{}, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.values, nil
}

func (m *Manager) SetCloseToTray(ctx context.Context, enabled bool) (Values, error) {
	if err := contextError(ctx); err != nil {
		return Values{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	next := m.values
	next.CloseToTray = enabled
	next.Version = stateVersion
	if err := m.store.Save(ctx, m.path, next); err != nil {
		return Values{}, err
	}
	m.values = next
	return next, nil
}

func (m *Manager) SetLocale(ctx context.Context, locale Locale) (Values, error) {
	if err := contextError(ctx); err != nil {
		return Values{}, err
	}
	if !locale.Valid() {
		return Values{}, failure(lifecycle.ErrorSettingsStateInvalid, "Language is not supported", "choose one of the available dsh-work languages")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	next := m.values
	next.Locale = locale
	next.Version = stateVersion
	if err := m.store.Save(ctx, m.path, next); err != nil {
		return Values{}, err
	}
	m.values = next
	return next, nil
}

func (m *Manager) SetNotificationPreference(ctx context.Context, key notifications.PreferenceKey, enabled bool) (Values, error) {
	if err := contextError(ctx); err != nil {
		return Values{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	nextPreferences, ok := m.values.Notifications.Set(key, enabled)
	if !ok {
		return Values{}, failure(lifecycle.ErrorSettingsStateInvalid, "Notification preference is invalid", "choose a supported notification setting")
	}
	next := m.values
	next.Notifications = nextPreferences
	next.Version = stateVersion
	if err := m.store.Save(ctx, m.path, next); err != nil {
		return Values{}, err
	}
	m.values = next
	return next, nil
}

type FileStore struct {
	Replacer FileReplacer
}

func (FileStore) Load(ctx context.Context, path string) (*Values, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, failure(lifecycle.ErrorSettingsStateInvalid, "dsh-work settings could not be read", "the persisted settings are unavailable")
	}
	var raw struct {
		Version                  int             `json:"version"`
		CloseToTray              *bool           `json:"closeToTray"`
		AutomaticRuntimeRollback *bool           `json:"automaticRuntimeRollback"`
		Locale                   *Locale         `json:"locale"`
		Notifications            json.RawMessage `json:"notifications"`
	}
	if err := json.Unmarshal(data, &raw); err != nil || (raw.Version != 1 && raw.Version != stateVersion) {
		return nil, failure(lifecycle.ErrorSettingsStateInvalid, "dsh-work settings are invalid", "the persisted settings use an unsupported format")
	}
	values := DefaultValues()
	values.Version = stateVersion
	if raw.CloseToTray != nil {
		values.CloseToTray = *raw.CloseToTray
	}
	if raw.Locale != nil && raw.Locale.Valid() {
		values.Locale = *raw.Locale
	}
	if raw.Version >= 2 && raw.AutomaticRuntimeRollback != nil {
		values.AutomaticRuntimeRollback = *raw.AutomaticRuntimeRollback
	}
	if len(raw.Notifications) > 0 && string(raw.Notifications) != "null" {
		var candidate struct {
			Enabled             *bool `json:"enabled"`
			Completed           *bool `json:"completed"`
			InteractionRequired *bool `json:"interactionRequired"`
			Errors              *bool `json:"errors"`
			Lifecycle           *bool `json:"lifecycle"`
		}
		if err := json.Unmarshal(raw.Notifications, &candidate); err == nil {
			preferences := notifications.DefaultPreferences()
			if candidate.Enabled != nil {
				preferences.Enabled = *candidate.Enabled
			}
			if candidate.Completed != nil {
				preferences.Completed = *candidate.Completed
			}
			if candidate.InteractionRequired != nil {
				preferences.InteractionRequired = *candidate.InteractionRequired
			}
			if candidate.Errors != nil {
				preferences.Errors = *candidate.Errors
			}
			if candidate.Lifecycle != nil {
				preferences.Lifecycle = *candidate.Lifecycle
			}
			values.Notifications = preferences
		}
	}
	return &values, nil
}

func (s FileStore) Save(ctx context.Context, path string, values Values) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	values.Version = stateVersion
	data, err := json.MarshalIndent(values, "", "  ")
	if err != nil {
		return failure(lifecycle.ErrorSettingsStateInvalid, "dsh-work settings could not be encoded", "the setting change could not be persisted")
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return failure(lifecycle.ErrorSettingsStateInvalid, "dsh-work settings directory could not be created", "the setting change could not be persisted")
	}
	temporary, err := os.CreateTemp(directory, ".settings-*.tmp")
	if err != nil {
		return failure(lifecycle.ErrorSettingsStateInvalid, "dsh-work settings could not be written", "the setting change could not be persisted")
	}
	temporaryPath := temporary.Name()
	keepTemporary := false
	defer func() {
		_ = temporary.Close()
		if !keepTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return failure(lifecycle.ErrorSettingsStateInvalid, "dsh-work settings could not be secured", "the setting change could not be persisted")
	}
	if _, err := temporary.Write(data); err != nil {
		return failure(lifecycle.ErrorSettingsStateInvalid, "dsh-work settings could not be written", "the setting change could not be persisted")
	}
	if err := temporary.Sync(); err != nil {
		return failure(lifecycle.ErrorSettingsStateInvalid, "dsh-work settings could not be flushed", "the setting change could not be persisted")
	}
	if err := temporary.Close(); err != nil {
		return failure(lifecycle.ErrorSettingsStateInvalid, "dsh-work settings could not be closed", "the setting change could not be persisted")
	}
	var replaceErr error
	if s.Replacer != nil {
		replaceErr = s.Replacer.Replace(temporaryPath, path)
	} else {
		replaceErr = os.Rename(temporaryPath, path)
	}
	if err := replaceErr; err != nil {
		return failure(lifecycle.ErrorSettingsStateInvalid, "dsh-work settings could not be replaced", "the setting change could not be persisted")
	}
	keepTemporary = true
	return nil
}

func normalizePath(path string) (string, error) {
	if path == "" {
		root, err := os.UserConfigDir()
		if err != nil || root == "" {
			root, err = os.Getwd()
			if err != nil {
				return "", failure(lifecycle.ErrorSettingsStateInvalid, "dsh-work settings path is unavailable", "the settings directory could not be determined")
			}
			root = filepath.Join(root, ".dsh-work")
		} else {
			root = filepath.Join(root, "dsh-work")
		}
		path = filepath.Join(root, "settings.json")
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", failure(lifecycle.ErrorSettingsStateInvalid, "dsh-work settings path is invalid", "the settings path could not be normalized")
	}
	return filepath.Clean(absPath), nil
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return lifecycle.Failure{
			Code:          lifecycle.ErrorCancelled,
			Summary:       "The settings operation was cancelled.",
			Detail:        "the setting change did not complete",
			CorrelationID: lifecycle.NewCorrelationID(),
		}
	}
	return nil
}

func failure(code lifecycle.ErrorCode, summary, detail string) error {
	return lifecycle.Failure{
		Code:          code,
		Summary:       summary,
		Detail:        detail,
		CorrelationID: lifecycle.NewCorrelationID(),
	}
}
