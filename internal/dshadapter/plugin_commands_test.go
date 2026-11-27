package dshadapter

import "testing"

func TestPluginCommandsBuildProfileScopedDSHArguments(t *testing.T) {
	commands := NewPluginCommands()

	install, err := commands.Install("coding", "@example/plugin@1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := joinArgs(install), "plugin --profile coding add @example/plugin@1.2.3"; got != want {
		t.Fatalf("install args = %q, want %q", got, want)
	}

	remove, err := commands.Remove("coding", "@example/plugin")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := joinArgs(remove), "plugin --profile coding remove @example/plugin"; got != want {
		t.Fatalf("remove args = %q, want %q", got, want)
	}
	list, err := commands.List("coding")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := joinArgs(list), "plugin --profile coding list --depth 0 --json"; got != want {
		t.Fatalf("list args = %q", got)
	}
	// pnpm outdated rejects --registry; the Host passes the registry by environment.
	outdated, err := commands.Outdated("coding")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := joinArgs(outdated), "plugin --profile coding outdated --format json"; got != want {
		t.Fatalf("outdated args = %q", got)
	}
	update, err := commands.Update("coding", "@example/plugin", "https://registry.npmmirror.com/")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := joinArgs(update), "plugin --profile coding update @example/plugin --latest --registry https://registry.npmmirror.com/"; got != want {
		t.Fatalf("update args = %q", got)
	}
}

func TestPluginCommandsRejectUnsafeOrAmbiguousArguments(t *testing.T) {
	commands := NewPluginCommands()
	for _, test := range []struct {
		name string
		call func() ([]string, error)
	}{
		{name: "profile traversal", call: func() ([]string, error) { return commands.Install("..", "@example/plugin") }},
		{name: "profile separator", call: func() ([]string, error) { return commands.Install("coding\\nested", "@example/plugin") }},
		{name: "package flag", call: func() ([]string, error) { return commands.Install("coding", "--registry=https://example.invalid") }},
		{name: "package whitespace", call: func() ([]string, error) { return commands.Remove("coding", "@example/plugin latest") }},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := test.call(); err == nil {
				t.Fatal("command unexpectedly accepted unsafe argument")
			}
		})
	}
}

func TestPluginVersionApprovalMatchesOnlyGeneratedArguments(t *testing.T) {
	commands := NewPluginCommands()
	approved, err := commands.AllowVersion("web", "@example/plugin@1.2.3", "0.2.2-rc.1")
	if err != nil || !commands.IsVersionApproval(approved) {
		t.Fatalf("generated approval rejected: %v (%v)", approved, err)
	}
	for _, args := range [][]string{
		approved[:7],
		append(append([]string{}, approved...), "--registry=https://example.invalid"),
		{"plugin", "--profile", "web", "revoke-version", "@example/plugin@1.2.3", "--dsh-version", "0.2.2-rc.1", "--accept-risk"},
		{"plugin", "--profile", "..", "allow-version", "@example/plugin@1.2.3", "--dsh-version", "0.2.2-rc.1", "--accept-risk"},
		{"plugin", "--profile", "web", "allow-version", "--help", "--dsh-version", "0.2.2-rc.1", "--accept-risk"},
	} {
		if commands.IsVersionApproval(args) {
			t.Fatalf("invalid approval accepted: %v", args)
		}
	}
}

func TestPackageMaintenanceMatchesManagerOperations(t *testing.T) {
	commands := NewPluginCommands()
	for _, build := range []func() ([]string, error){
		func() ([]string, error) { return commands.Install("web", "is-number@7.0.0") },
		func() ([]string, error) {
			return commands.InstallAt("web", "is-number@7.0.0", "https://example.invalid/")
		},
		func() ([]string, error) { return commands.List("web") },
		func() ([]string, error) { return commands.Outdated("web") },
		func() ([]string, error) { return commands.Update("web", "is-number", "") },
		func() ([]string, error) { return commands.Update("web", "is-number", "https://example.invalid/") },
		func() ([]string, error) { return commands.Remove("web", "is-number") },
		func() ([]string, error) { return commands.RemoveVersions("web", []string{"is-number", "is-odd"}) },
		func() ([]string, error) { return commands.Prepare("web") },
		func() ([]string, error) { return commands.ForceInstall("web", false) },
		func() ([]string, error) { return commands.ForceInstall("web", true) },
	} {
		args, err := build()
		if err != nil || !commands.IsPackageMaintenance(args) {
			t.Fatalf("generated maintenance command rejected: %v (%v)", args, err)
		}
		if commands.IsPackageMaintenance(append(args, "--ignore-scripts=false")) {
			t.Fatalf("unexpected extra option accepted: %v", args)
		}
	}
	for _, args := range [][]string{
		{"plugin", "--profile", "web", "exec", "node"},
		{"plugin", "--profile", "web", "run", "prepare"},
		{"plugin", "--profile", "web", "add", "is-number", "--dir", "https://example.invalid/"},
		{"plugin", "--profile", "..", "install"},
		{"plugin", "--profile", "--help", "list", "--depth", "0", "--json"},
	} {
		if commands.IsPackageMaintenance(args) {
			t.Fatalf("unexpected maintenance command accepted: %v", args)
		}
	}
}

func joinArgs(args []string) string {
	result := ""
	for index, arg := range args {
		if index > 0 {
			result += " "
		}
		result += arg
	}
	return result
}
