// Package browserhost defines the install-owned native host boundary.
package browserhost

import (
	"context"
	"errors"

	"github.com/local/dsh-work/internal/settings"
)

const Name = "com.dsh_work.browser_dev"
const Origin = "chrome-extension://fpemabjfldbpdmjfdofdnfdaccafobod/"

type Registration struct {
	Browser    string `json:"browser"`
	Registered bool   `json:"registered"`
	Failure    string `json:"failure,omitempty"`
}
type Config struct {
	Directory  string
	Executable string
	Identity   string
	Replacer   settings.FileReplacer
}
type Registrar interface {
	Ensure(context.Context) []Registration
	Remove(context.Context) error
	Refresh(context.Context) error
}

var ErrUnsupported = errors.New("BROWSER_NATIVE_HOST_UNSUPPORTED")

func New(config Config) Registrar { return newRegistrar(config) }

// ParentBrowser identifies the actual launching browser process, not the first
// connected profile or a family string supplied by extension JavaScript.
type ParentInfo struct{ Browser, Executable string }

func ParentBrowser() (string, error)           { p, err := parentBrowserInfo(); return p.Browser, err }
func DetectParentBrowser() (ParentInfo, error) { return parentBrowserInfo() }
