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
