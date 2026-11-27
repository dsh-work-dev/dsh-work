package dshadapter

import (
	"fmt"
	"slices"
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

func (PluginCommands) AllowVersion(profile, packageVersion, dshVersion string) ([]string, error) {
	if !validProfileName(profile) || !validPackageSpec(packageVersion) || !validPackageSpec(dshVersion) {
		return nil, fmt.Errorf("invalid DSH plugin exemption target")
	}
	return []string{"plugin", "--profile", profile, "allow-version", packageVersion, "--dsh-version", dshVersion, "--accept-risk"}, nil
}

// IsVersionApproval identifies the exact DSH-owned approval command. It does
// not admit any of the plugin subcommands that DSH forwards to pnpm.
func (c PluginCommands) IsVersionApproval(args []string) bool {
	if len(args) != 8 {
		return false
	}
	expected, err := c.AllowVersion(args[2], args[4], args[6])
	return err == nil && slices.Equal(args, expected)
}

// IsPackageMaintenance identifies the pnpm operations assembled by the manager.
// Each accepted option belongs to the operation that generates it.
func (c PluginCommands) IsPackageMaintenance(args []string) bool {
	if len(args) < 4 || args[0] != "plugin" || args[1] != "--profile" || !validProfileName(args[2]) || strings.HasPrefix(args[2], "-") {
		return false
	}
	profile, params := args[2], args[4:]
	matches := func(expected []string, err error) bool {
		return err == nil && slices.Equal(args, expected)
	}
	switch args[3] {
	case "add":
		switch len(params) {
		case 1:
			return matches(c.Install(profile, params[0]))
		case 3:
			return matches(c.InstallAt(profile, params[0], params[2]))
		}
	case "update":
		switch len(params) {
		case 2:
			return matches(c.Update(profile, params[0], ""))
		case 4:
			return matches(c.Update(profile, params[0], params[3]))
		}
	case "list":
		return matches(c.List(profile))
	case "outdated":
		return matches(c.Outdated(profile))
	case "install":
		return matches(c.Prepare(profile)) || matches(c.ForceInstall(profile, false)) || matches(c.ForceInstall(profile, true))
	case "remove":
		if len(params) == 1 {
			return matches(c.Remove(profile, params[0]))
		}
		if len(params) >= 3 {
			return matches(c.RemoveVersions(profile, params[:len(params)-2]))
		}
	}
	return false
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
