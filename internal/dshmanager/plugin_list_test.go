package dshmanager

import "testing"

func TestApplyPluginListJSONReadsPnpmArray(t *testing.T) {
	plugins := []PluginInfo{{Package: "billion-context"}}
	applyPluginListJSON(plugins, `[{"name":"dsh-profile-web","private":true,"dependencies":{"billion-context":{"from":"billion-context","version":"0.1.199","resolved":"https://registry.npmjs.org/billion-context/-/billion-context-0.1.199.tgz"}}}]`)
	if got := plugins[0].Version; got != "0.1.199" {
		t.Fatalf("array version = %q, want 0.1.199", got)
	}
	if got := plugins[0].CurrentVersion; got != "0.1.199" {
		t.Fatalf("array current version = %q, want 0.1.199", got)
	}

	singleObjectPlugins := []PluginInfo{{Package: "x"}}
	applyPluginListJSON(singleObjectPlugins, `{"dependencies":{"x":{"version":"1.2.3"}}}`)
	if got := singleObjectPlugins[0].Version; got != "1.2.3" {
		t.Fatalf("single-object version = %q, want 1.2.3", got)
	}
}
