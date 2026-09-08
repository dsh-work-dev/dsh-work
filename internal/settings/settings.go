package settings

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/local/dsh-work/internal/lifecycle"
	"github.com/local/dsh-work/internal/notifications"
)

const stateVersion = 2

const petPreferenceVersion = 2

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

// PetVisibilityIntent is the persisted user choice. It is intentionally
// separate from the runtime's effective visibility, which can be paused when
// the selected package is unavailable or the platform cannot host an overlay.
type PetVisibilityIntent string

const (
	PetVisibilityVisible PetVisibilityIntent = "visible"
	PetVisibilityHidden  PetVisibilityIntent = "hidden"
)

func (i PetVisibilityIntent) Valid() bool {
	return i == PetVisibilityVisible || i == PetVisibilityHidden
}

// PetPosition stores a monitor-aware logical anchor rather than an absolute
// screen coordinate. Host policy clamps it to the current work area.
type PetPosition struct {
	MonitorID string  `json:"monitorId,omitempty"`
	AnchorX   float64 `json:"anchorX"`
	AnchorY   float64 `json:"anchorY"`
	Width     int     `json:"width"`
	Height    int     `json:"height"`
	Scale     float64 `json:"scale"`
}

func DefaultPetPosition() PetPosition {
	return PetPosition{AnchorX: 1, AnchorY: 1, Width: 144, Height: 156, Scale: 1}
}

// PetPreference is the versioned Host-owned Pet preference. A missing
// selected key is represented by nil and is never replaced by discovery.
type PetPreference struct {
	SchemaVersion    int                 `json:"schemaVersion"`
	SelectedKey      *string             `json:"selectedKey"`
	VisibilityIntent PetVisibilityIntent `json:"visibilityIntent"`
	Position         PetPosition         `json:"position"`
}

func DefaultPetPreference() PetPreference {
	return PetPreference{
		SchemaVersion:    petPreferenceVersion,
		VisibilityIntent: PetVisibilityHidden,
		Position:         DefaultPetPosition(),
	}
}

func clonePetPreference(preference PetPreference) PetPreference {
	copy := preference
	if preference.SelectedKey != nil {
		selected := *preference.SelectedKey
		copy.SelectedKey = &selected
	}
	return copy
}

func normalizePetPreference(preference PetPreference) PetPreference {
	defaults := DefaultPetPreference()
	if preference.SchemaVersion == 1 {
		// Version 1 used 192x208 as 100%. Keep an existing user's chosen
		// visual size while moving the baseline to 75% of that window.
		if preference.Position.Width > 0 && preference.Position.Width <= 4096 {
			preference.Position.Width = minDimension(int(math.Round(float64(preference.Position.Width)*0.75)), defaults.Position.Width*2)
		}
		if preference.Position.Height > 0 && preference.Position.Height <= 4096 {
			preference.Position.Height = minDimension(int(math.Round(float64(preference.Position.Height)*0.75)), defaults.Position.Height*2)
		}
	}
	preference.SchemaVersion = petPreferenceVersion
	if !preference.VisibilityIntent.Valid() {
		preference.VisibilityIntent = defaults.VisibilityIntent
	}
	if preference.Position.Width <= 0 || preference.Position.Width > 4096 {
		preference.Position.Width = defaults.Position.Width
	}
	if preference.Position.Height <= 0 || preference.Position.Height > 4096 {
		preference.Position.Height = defaults.Position.Height
	}
	if !finite(preference.Position.Scale) || preference.Position.Scale <= 0 || preference.Position.Scale > 8 {
		preference.Position.Scale = defaults.Position.Scale
	}
	if !finite(preference.Position.AnchorX) || preference.Position.AnchorX < 0 || preference.Position.AnchorX > 1 {
		preference.Position.AnchorX = defaults.Position.AnchorX
	}
	if !finite(preference.Position.AnchorY) || preference.Position.AnchorY < 0 || preference.Position.AnchorY > 1 {
		preference.Position.AnchorY = defaults.Position.AnchorY
	}
	if !safeMonitorID(preference.Position.MonitorID) {
		preference.Position.MonitorID = ""
	}
	if preference.SelectedKey != nil {
		selected := *preference.SelectedKey
		if !safePreferenceKey(selected) {
			preference.SelectedKey = nil
		}
	}
	if preference.SelectedKey == nil {
		preference.VisibilityIntent = PetVisibilityHidden
	}
	return clonePetPreference(preference)
}

func minDimension(value, maximum int) int {
	if value > maximum {
		return maximum
	}
	return value
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func safeMonitorID(value string) bool {
	if value == "" {
		return true
	}
	if !utf8.ValidString(value) || len([]rune(value)) > 128 {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}

func safePreferenceKey(value string) bool {
	if value == "" || len(value) > 256 || !utf8.ValidString(value) || strings.ContainsAny(value, `/\\`) || strings.Contains(value, "://") {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
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
	Pet                      PetPreference             `json:"pet"`
}

func DefaultValues() Values {
	return Values{
		Version:                  stateVersion,
		CloseToTray:              true,
		AutomaticRuntimeRollback: true,
		Locale:                   DefaultLocale,
		Notifications:            notifications.DefaultPreferences(),
		Pet:                      DefaultPetPreference(),
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
		values.Pet = normalizePetPreference(values.Pet)
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
	return cloneValues(m.values), nil
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
	return cloneValues(next), nil
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
	return cloneValues(next), nil
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
	return cloneValues(next), nil
}

// SetPetPreference persists the complete Pet preference as one atomic settings
// update. Selection transactions use this seam only after their source and
// renderer preflight has succeeded.
func (m *Manager) SetPetPreference(ctx context.Context, preference PetPreference) (Values, error) {
	if err := contextError(ctx); err != nil {
		return Values{}, err
	}
	preference = normalizePetPreference(preference)
	m.mu.Lock()
	defer m.mu.Unlock()
	next := m.values
	next.Pet = preference
	next.Version = stateVersion
	if err := m.store.Save(ctx, m.path, next); err != nil {
		return Values{}, err
	}
	m.values = next
	return cloneValues(next), nil
}

func cloneValues(values Values) Values {
	values.Pet = clonePetPreference(values.Pet)
	return values
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
		Pet                      json.RawMessage `json:"pet"`
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
	if len(raw.Pet) > 0 && string(raw.Pet) != "null" {
		var candidate PetPreference
		if err := json.Unmarshal(raw.Pet, &candidate); err == nil {
			values.Pet = normalizePetPreference(candidate)
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
