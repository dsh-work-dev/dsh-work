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

func (PluginCommands) List(profile string) ([]string, error) {
	if !validProfileName(profile) {
		return nil, fmt.Errorf("invalid DSH plugin list target")
	}
	return []string{"plugin", "--profile", profile, "list", "--depth", "0", "--json"}, nil
}

// Outdated takes no registry flag: pnpm outdated rejects --registry, so the
// Host passes the registry through the npm config environment.
func (PluginCommands) Outdated(profile string) ([]string, error) {
	if !validProfileName(profile) {
		return nil, fmt.Errorf("invalid DSH plugin outdated target")
	}
	return []string{"plugin", "--profile", profile, "outdated", "--format", "json"}, nil
}

func (PluginCommands) Update(profile, packageName, registry string) ([]string, error) {
	if !validProfileName(profile) || !validPackageSpec(packageName) {
		return nil, fmt.Errorf("invalid DSH plugin update target")
	}
	// --latest moves past the declared range (a `^0.0.x` range admits only that
	// patch), matching the latest version the outdated check offers.
	return appendRegistry([]string{"plugin", "--profile", profile, "update", strings.TrimSpace(packageName), "--latest"}, registry)
}

func (PluginCommands) InstallAt(profile, packageSpec, registry string) ([]string, error) {
	args, err := (PluginCommands{}).Install(profile, packageSpec)
	if err != nil {
		return nil, err
	}
	return appendRegistry(args, registry)
}

// Prepare returns the public DSH command that reconciles a profile's declared
// dependencies and bundle manifest. DSH owns the package-manager details;
// dsh-work only decides when a profile needs this explicit preparation.
func (PluginCommands) Prepare(profile string) ([]string, error) {
	if !validProfileName(profile) {
		return nil, fmt.Errorf("invalid DSH plugin preparation target")
	}
	return []string{"plugin", "--profile", profile, "install"}, nil
}

func appendRegistry(args []string, registry string) ([]string, error) {
	registry = strings.TrimSpace(registry)
	if registry == "" {
		return args, nil
	}
	if !strings.HasPrefix(registry, "https://") || strings.ContainsAny(registry, " \t\r\n") {
		return nil, fmt.Errorf("invalid plugin registry")
	}
	return append(args, "--registry", registry), nil
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
