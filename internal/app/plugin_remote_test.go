package app

import (
	"testing"

	"github.com/local/dsh-work/internal/dshmanager"
)

func TestApplyBundleState(t *testing.T) {
	const name = "@deepseek-ai/dsh-plugin-example"
	tests := []struct {
		name          string
		bundles       []pluginManagerBundle
		wantDisabled  bool
		wantCanToggle bool
	}{
		{name: "not reported by DSH", bundles: nil},
		{name: "not installed", bundles: []pluginManagerBundle{{Name: name, Installed: false}}},
		{
			name:          "installed and readable",
			bundles:       []pluginManagerBundle{{Name: name, Installed: true, Enabled: true, Availability: "profile"}},
			wantCanToggle: true,
		},
		{
			name:         "declared but files missing",
			bundles:      []pluginManagerBundle{{Name: name, Installed: true, Enabled: false, Availability: "missing"}},
			wantDisabled: true,
		},
		{
			name:    "read-only",
			bundles: []pluginManagerBundle{{Name: name, Installed: true, Enabled: true, Availability: "profile", ReadOnlyReason: "managed"}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plugins := []dshmanager.PluginInfo{{Package: name, Disabled: false, CanToggle: false}}
			applyBundleState(plugins, test.bundles)
			if plugins[0].Disabled != test.wantDisabled || plugins[0].CanToggle != test.wantCanToggle {
				t.Fatalf("Disabled=%v CanToggle=%v, want Disabled=%v CanToggle=%v", plugins[0].Disabled, plugins[0].CanToggle, test.wantDisabled, test.wantCanToggle)
			}
		})
	}
}
