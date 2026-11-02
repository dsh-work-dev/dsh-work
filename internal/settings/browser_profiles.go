package settings

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

// BrowserProfiles contains local setup identities, never execution grants or
// native-channel credentials. Profile directories are not routing identities.
type BrowserProfiles struct {
	SchemaVersion int              `json:"schemaVersion"`
	Selected      string           `json:"selected,omitempty"`
	Bindings      []BrowserBinding `json:"bindings"`
}

type BrowserBinding struct {
	ID          string             `json:"id"`
	Browser     string             `json:"browser"`
	ExtensionID string             `json:"extensionId"`
	Instance    string             `json:"instance"`
	Name        string             `json:"name"`
	Launch      *BrowserLaunchHint `json:"launch,omitempty"`
}

// A user-confirmed launch hint is not proof of directory-to-instance identity.
// The authenticated instance is still required before any browser route opens.
type BrowserLaunchHint struct {
	Executable        string `json:"executable"`
	UserDataDirectory string `json:"userDataDirectory"`
	ProfileDirectory  string `json:"profileDirectory"`
}

func (h BrowserLaunchHint) Valid() bool {
	return utf8.ValidString(h.Executable+h.UserDataDirectory+h.ProfileDirectory) && !strings.ContainsAny(h.Executable+h.UserDataDirectory, "\x00") && filepath.IsAbs(h.Executable) && filepath.IsAbs(h.UserDataDirectory) && h.ProfileDirectory != "" && h.ProfileDirectory != "." && h.ProfileDirectory != ".." && filepath.Base(h.ProfileDirectory) == h.ProfileDirectory && !strings.ContainsAny(h.ProfileDirectory, "/\\\x00")
}

func ValidBrowserID(s string) bool {
	if len(s) != 32 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func ValidExtensionID(s string) bool {
	if len(s) != 32 {
		return false
	}
	for _, c := range s {
		if c < 'a' || c > 'p' {
			return false
		}
	}
	return true
}
func ValidBrowserName(s string) bool {
	return utf8.ValidString(s) && strings.TrimSpace(s) == s && utf8.RuneCountInString(s) >= 1 && utf8.RuneCountInString(s) <= 60 && !strings.ContainsFunc(s, unicode.IsControl)
}
func (p BrowserProfiles) Validate() error {
	if p.SchemaVersion != 1 || len(p.Bindings) > 32 {
		return errors.New("BROWSER_BINDINGS_INVALID")
	}
	ids, instances := map[string]bool{}, map[string]bool{}
	selected := p.Selected == ""
	for _, b := range p.Bindings {
		key := b.Browser + "/" + b.ExtensionID + "/" + b.Instance
		if !ValidBrowserID(b.ID) || !ValidBrowserID(b.Instance) || !ValidExtensionID(b.ExtensionID) || (b.Browser != "chrome" && b.Browser != "edge") || !ValidBrowserName(b.Name) || ids[b.ID] || instances[key] {
			return errors.New("BROWSER_BINDINGS_INVALID")
		}
		if b.Launch != nil && !b.Launch.Valid() {
			return errors.New("BROWSER_LAUNCH_HINT_INVALID")
		}
		ids[b.ID], instances[key] = true, true
		selected = selected || b.ID == p.Selected
	}
	if !selected {
		return errors.New("BROWSER_BINDINGS_INVALID")
	}
	return nil
}
func cloneBrowserProfiles(p *BrowserProfiles) *BrowserProfiles {
	if p == nil {
		return nil
	}
	v := *p
	v.Bindings = append([]BrowserBinding(nil), p.Bindings...)
	for i := range v.Bindings {
		if h := v.Bindings[i].Launch; h != nil {
			copy := *h
			v.Bindings[i].Launch = &copy
		}
	}
	return &v
}
func (m *Manager) SetBrowserProfiles(ctx context.Context, p BrowserProfiles) (Values, error) {
	if err := contextError(ctx); err != nil {
		return Values{}, err
	}
	if err := p.Validate(); err != nil {
		return Values{}, err
	}
	m.mu.Lock()
	next := m.values
	next.BrowserProfiles = cloneBrowserProfiles(&p)
	if len(p.Bindings) == 0 && p.Selected == "" {
		next.BrowserProfiles = nil
	}
	if err := m.store.Save(ctx, m.path, next); err != nil {
		m.mu.Unlock()
		return Values{}, err
	}
	return m.commit(next), nil
}
