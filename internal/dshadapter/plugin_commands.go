package dshadapter

import (
	"fmt"
	"strings"
	"unicode"
)

// PluginCommands is the pinned DSH CLI grammar used by the runtime manager.
// Keeping it next to the version-aware DSH adapter prevents CLI details from
// leaking into the manager, Host or frontend.
type PluginCommands struct{}

func NewPluginCommands() PluginCommands {
	return PluginCommands{}
}

func (PluginCommands) Install(profile, packageSpec string) ([]string, error) {
	if !validProfileName(profile) || !validPackageSpec(packageSpec) {
		return nil, fmt.Errorf("invalid DSH plugin install target")
	}
	return []string{"plugin", "--profile", profile, "add", strings.TrimSpace(packageSpec)}, nil
}

func (PluginCommands) Remove(profile, packageName string) ([]string, error) {
	if !validProfileName(profile) || !validPackageSpec(packageName) {
		return nil, fmt.Errorf("invalid DSH plugin remove target")
	}
	return []string{"plugin", "--profile", profile, "remove", strings.TrimSpace(packageName)}, nil
}

func validPackageSpec(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && len(value) <= 256 && !strings.HasPrefix(value, "-") &&
		!strings.ContainsRune(value, '\x00') && strings.IndexFunc(value, unicode.IsSpace) < 0
}
