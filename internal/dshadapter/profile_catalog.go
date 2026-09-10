package dshadapter

import "github.com/local/dsh-work/internal/dshmanager"

// BuiltInProfiles exposes the profile catalog owned by this DSH adapter.
// Profile names and initialization behavior are DSH contract data, not
// generic manager policy.
func (a *Adapter) BuiltInProfiles() []dshmanager.ProfileDefinition {
	return []dshmanager.ProfileDefinition{
		{Name: "web", Kind: dshmanager.ProfileKindBuiltIn, AutoInitialize: true},
		{Name: "headless", DesktopUnsupported: true, Kind: dshmanager.ProfileKindBuiltIn, AutoInitialize: true},
		{Name: "sdk", DesktopUnsupported: true, Kind: dshmanager.ProfileKindBuiltIn, AutoInitialize: true},
		{Name: "sdk-minimal", DesktopUnsupported: true, Kind: dshmanager.ProfileKindBuiltIn, AutoInitialize: true},
		{Name: "acp", DesktopUnsupported: true, Kind: dshmanager.ProfileKindBuiltIn, AutoInitialize: true},
	}
}
