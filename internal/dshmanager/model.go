package dshmanager

// HomeOwnership describes who owns the DSH home. A Work-owned home is safe
// for dsh-work to create and maintain; a user-owned home is discovered only.
type HomeOwnership string

const (
	HomeOwnershipWork HomeOwnership = "work"
	HomeOwnershipUser HomeOwnership = "user"
)

// RuntimeSource identifies how a DSH runtime entered the catalog.
type RuntimeSource string

const (
	RuntimeSourceManaged            RuntimeSource = "managed"
	RuntimeSourceDevelopmentFixture RuntimeSource = "development-fixture"
	RuntimeSourceSystem             RuntimeSource = "system"
)

// ThemePreference is the DSH-owned appearance preference consumed by Work's
// trusted surfaces. Work does not persist or edit this value.
type ThemePreference string

const (
	ThemePreferenceLight  ThemePreference = "light"
	ThemePreferenceDark   ThemePreference = "dark"
	ThemePreferenceSystem ThemePreference = "system"
)

func (p ThemePreference) Valid() bool {
	return p == ThemePreferenceLight || p == ThemePreferenceDark || p == ThemePreferenceSystem
}

// RuntimeInfo is the platform-neutral read model for one immutable DSH
// distribution. Path is the executable (or launcher) path, not a DSH home.
type RuntimeInfo struct {
	ID        string        `json:"id"`
	Version   string        `json:"version"`
	Path      string        `json:"path"`
	Source    RuntimeSource `json:"source"`
	Installed bool          `json:"installed"`
	Removable bool          `json:"removable"`
}

// HomeInfo is a DSH_HOME identity. Profiles and their plugin state live under
// a home and are never properties of a runtime.
type HomeInfo struct {
	ID        string        `json:"id"`
	Name      string        `json:"name"`
	Path      string        `json:"path"`
	Ownership HomeOwnership `json:"ownership"`
}

// ProfileRef is intentionally two-dimensional. A profile name is not unique
// across DSH homes, so every selection and plugin operation must carry both
// the home identity and the profile name.
type ProfileRef struct {
	HomeID string `json:"homeId"`
	Name   string `json:"name"`
}

type ProfileKind string

const (
	ProfileKindBuiltIn ProfileKind = "built-in"
	ProfileKindCustom  ProfileKind = "custom"
)

// ProfileDefinition is supplied by the selected DSH adapter. Keeping built-in
// profile knowledge at that boundary avoids making the manager duplicate a
// version-specific DSH profile catalog.
type ProfileDefinition struct {
	Name           string      `json:"name"`
	Kind           ProfileKind `json:"kind"`
	AutoInitialize bool        `json:"autoInitialize"`
}

// ProfileInfo is a read model. It does not compose or mutate DSH profiles;
// those semantics remain behind the DSH public CLI seam.
type ProfileInfo struct {
	Ref            ProfileRef   `json:"ref"`
	Path           string       `json:"path"`
	Exists         bool         `json:"exists"`
	Kind           ProfileKind  `json:"kind"`
	Launchable     bool         `json:"launchable"`
	Renamable      bool         `json:"renamable"`
	AutoInitialize bool         `json:"autoInitialize"`
	PluginCount    int          `json:"pluginCount"`
	Plugins        []PluginInfo `json:"plugins,omitempty"`
}

type PluginInfo struct {
	Name      string `json:"name"`
	Version   string `json:"version,omitempty"`
	Package   string `json:"package"`
	Spec      string `json:"spec,omitempty"`
	Installed bool   `json:"installed"`
}

type PluginTarget struct {
	RuntimeID string     `json:"runtimeId"`
	Profile   ProfileRef `json:"profile"`
}

type PluginInstallRequest struct {
	Target  PluginTarget `json:"target"`
	Package string       `json:"package"`
}

type PluginRemoveRequest struct {
	Target  PluginTarget `json:"target"`
	Package string       `json:"package"`
}

type PluginListRequest struct {
	Target PluginTarget `json:"target"`
}

type PluginResult struct {
	Profile         ProfileRef   `json:"profile"`
	Plugins         []PluginInfo `json:"plugins"`
	RestartRequired bool         `json:"restartRequired"`
}

// ProfileRenameRequest changes the directory-backed identity of a custom DSH
// profile. Built-in profile names belong to the DSH adapter and are immutable.
type ProfileRenameRequest struct {
	Profile ProfileRef `json:"profile"`
	NewName string     `json:"newName"`
}

// LaunchSelection is the persisted desired state and the active state
// projected to the DSH manager inside the Settings window. Workspace is
// deliberately separate from the profile so one profile can be used for
// multiple projects.
type LaunchSelection struct {
	RuntimeID string     `json:"runtimeId"`
	Profile   ProfileRef `json:"profile"`
	Workspace string     `json:"workspace"`
}

// LaunchRequest is the validation input used by both GUI and CLI callers.
type LaunchRequest struct {
	RuntimeID string     `json:"runtimeId"`
	Profile   ProfileRef `json:"profile"`
	Workspace string     `json:"workspace"`
}

// ResolvedLaunch carries the validated catalog records alongside the
// normalized selection. Host uses this as one immutable handoff into the DSH
// adapter, so it does not reconstruct home/profile relationships itself.
type ResolvedLaunch struct {
	Selection LaunchSelection `json:"selection"`
	Runtime   RuntimeInfo     `json:"runtime"`
	Home      HomeInfo        `json:"home"`
}

type Snapshot struct {
	Runtimes []RuntimeInfo    `json:"runtimes"`
	Homes    []HomeInfo       `json:"homes"`
	Profiles []ProfileInfo    `json:"profiles"`
	Desired  *LaunchSelection `json:"desired,omitempty"`
	Active   *LaunchSelection `json:"active,omitempty"`
	Theme    ThemePreference  `json:"theme"`
}
