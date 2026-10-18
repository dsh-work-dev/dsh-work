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

func TestApplyBundleStateMarksIncompatible(t *testing.T) {
	const name = "@scope/pkg"
	plugins := []dshmanager.PluginInfo{{Package: name}}
	bundles := []pluginManagerBundle{{
		Name: name, Installed: true, Enabled: false, Availability: "profile",
		Error: &pluginManagerErr{
			Code: "incompatible-version",
			Incompatible: []struct {
				Name           string            `json:"name"`
				Version        string            `json:"version"`
				RuntimeVersion string            `json:"runtimeVersion"`
				Requirements   map[string]string `json:"requirements"`
			}{{
				Name: name, Version: "1.0.20", RuntimeVersion: "0.2.1-rc.2",
				Requirements: map[string]string{"dsh": "^0.3.0"},
			}},
		},
	}}
	applyBundleState(plugins, bundles)
	got := plugins[0].Incompatible
	if got == nil || got.Version != "1.0.20" || got.DSHVersion != "0.2.1-rc.2" || got.Requirements["dsh"] != "^0.3.0" {
		t.Fatalf("Incompatible = %#v, want DSH's version and requirement", got)
	}
	if plugins[0].CanToggle {
		t.Fatal("incompatible plugin must not be toggleable")
	}
}
