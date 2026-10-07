package settings

import (
	"context"

	"github.com/local/dsh-work/internal/lifecycle"
)

// ThemeID names a built-in dsh-work theme. Themes are static and shipped with
// the frontend; the Host only stores which one the user chose.
type ThemeID string

const (
	ThemeMonochrome ThemeID = "monochrome"
	ThemeChatGPT    ThemeID = "chatgpt"
	ThemeClaude     ThemeID = "claude"
	ThemeGitHub     ThemeID = "github"
	ThemeLobeHub    ThemeID = "lobehub"
	ThemeSoft       ThemeID = "soft"
	ThemeSwiss      ThemeID = "swiss"
	ThemePaper      ThemeID = "paper"
	ThemeGlass      ThemeID = "glass"
	ThemeInk        ThemeID = "ink"
	ThemeClassic    ThemeID = "classic"
	ThemeTerminal   ThemeID = "terminal"
	ThemeNeon       ThemeID = "neon"
	ThemeBrutal     ThemeID = "brutal"
	ThemeBauhaus    ThemeID = "bauhaus"
	ThemeDeco       ThemeID = "deco"
	DefaultTheme            = ThemeMonochrome
)

// themeModes lists the light/dark modes each built-in theme supports. A theme
// may support only one; "system" is allowed when it supports both.
var themeModes = map[ThemeID][]AppearanceMode{
	ThemeMonochrome: {AppearanceSystem, AppearanceLight, AppearanceDark},
	ThemeChatGPT:    {AppearanceSystem, AppearanceLight, AppearanceDark},
	ThemeClaude:     {AppearanceSystem, AppearanceLight, AppearanceDark},
	ThemeGitHub:     {AppearanceSystem, AppearanceLight, AppearanceDark},
	ThemeLobeHub:    {AppearanceSystem, AppearanceLight, AppearanceDark},
	ThemeSoft:       {AppearanceSystem, AppearanceLight, AppearanceDark},
	ThemeSwiss:      {AppearanceSystem, AppearanceLight, AppearanceDark},
	ThemePaper:      {AppearanceSystem, AppearanceLight, AppearanceDark},
	ThemeGlass:      {AppearanceSystem, AppearanceLight, AppearanceDark},
	ThemeInk:        {AppearanceLight},
	ThemeClassic:    {AppearanceLight},
	ThemeTerminal:   {AppearanceDark},
	ThemeNeon:       {AppearanceDark},
	ThemeBrutal:     {AppearanceLight},
	ThemeBauhaus:    {AppearanceLight},
	ThemeDeco:       {AppearanceDark},
}

func (t ThemeID) Valid() bool {
	_, ok := themeModes[t]
	return ok
}

// AppearanceMode is the light/dark choice for dsh-work's own windows.
type AppearanceMode string

const (
	AppearanceSystem AppearanceMode = "system"
	AppearanceLight  AppearanceMode = "light"
	AppearanceDark   AppearanceMode = "dark"
)

func (m AppearanceMode) Valid() bool {
	return m == AppearanceSystem || m == AppearanceLight || m == AppearanceDark
}

// Appearance is the Host-owned look of the startup, Settings and Pet settings
// surfaces. DSH content keeps its own appearance.
type Appearance struct {
	Theme ThemeID        `json:"theme"`
	Mode  AppearanceMode `json:"mode"`
}

func DefaultAppearance() Appearance {
	return Appearance{Theme: DefaultTheme, Mode: AppearanceSystem}
}

// Valid reports whether the theme exists and supports the mode.
func (a Appearance) Valid() bool {
	for _, mode := range themeModes[a.Theme] {
		if mode == a.Mode {
			return true
		}
	}
	return false
}

// normalizeAppearance keeps a readable stored value and falls back field by
// field so an unknown theme from a newer build does not reset the mode.
func normalizeAppearance(appearance Appearance) Appearance {
	if !appearance.Theme.Valid() {
		appearance.Theme = DefaultTheme
	}
	if !appearance.Valid() {
		appearance.Mode = themeModes[appearance.Theme][0]
	}
	return appearance
}

// SetAppearance persists a validated appearance. Invalid input is rejected
// rather than normalized so a caller never believes an unsupported value took
// effect.
func (m *Manager) SetAppearance(ctx context.Context, appearance Appearance) (Values, error) {
	if err := contextError(ctx); err != nil {
		return Values{}, err
	}
	if !appearance.Valid() {
		return Values{}, failure(lifecycle.ErrorSettingsStateInvalid, "Appearance is not supported", "choose one of the available themes and modes")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	next := m.values
	next.Appearance = appearance
	next.Version = stateVersion
	if err := m.store.Save(ctx, m.path, next); err != nil {
		return Values{}, err
	}
	m.values = next
	m.appearanceImportPending = false
	return cloneValues(next), nil
}

// ImportAppearanceMode adopts a mode once for settings written before the
// Host owned appearance (they followed DSH's theme). It does nothing after the
// user or an earlier import has stored an appearance. A failed save leaves the
// import pending for the next start.
func (m *Manager) ImportAppearanceMode(ctx context.Context, mode AppearanceMode) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.appearanceImportPending {
		return nil
	}
	next := m.values
	if mode.Valid() {
		next.Appearance.Mode = mode
		next.Appearance = normalizeAppearance(next.Appearance)
	}
	next.Version = stateVersion
	if err := m.store.Save(ctx, m.path, next); err != nil {
		return err
	}
	m.values = next
	m.appearanceImportPending = false
	return nil
}
