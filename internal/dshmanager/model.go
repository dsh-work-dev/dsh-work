package dshmanager

import "github.com/local/dsh-work/internal/lifecycle"

// DataDirectoryOwnership describes who owns the DSH data directory. A
// dsh-work-owned directory is safe for dsh-work to create and maintain; a
// user-owned directory is discovered only.
type DataDirectoryOwnership string

const (
	DataDirectoryOwnershipDSHWork DataDirectoryOwnership = "dsh-work"
	DataDirectoryOwnershipUser    DataDirectoryOwnership = "user"
)

// RuntimeSource identifies how a DSH runtime entered the catalog.
type RuntimeSource string

const (
	RuntimeSourceManaged            RuntimeSource = "managed"
	RuntimeSourceDevelopmentFixture RuntimeSource = "development-fixture"
	RuntimeSourceSystem             RuntimeSource = "system"
)

// RuntimeToolchain identifies the toolchain used to materialize a managed
// DSH runtime. dsh-work never installs a managed pnpm toolchain.
type RuntimeToolchain string

const (
	RuntimeToolchainNone           RuntimeToolchain = "none"
	RuntimeToolchainSystemPNPM     RuntimeToolchain = "system-pnpm"
	RuntimeToolchainSystemNPM      RuntimeToolchain = "system-npm"
	RuntimeToolchainManagedNodeNPM RuntimeToolchain = "managed-node-npm"
)

func (t RuntimeToolchain) Valid() bool {
	return t == RuntimeToolchainNone || t == RuntimeToolchainSystemPNPM || t == RuntimeToolchainSystemNPM || t == RuntimeToolchainManagedNodeNPM
}

// RuntimeArtifactSource identifies the registry or carrier source used for a
// managed runtime. It is separate from RuntimeSource, which describes catalog
// ownership (managed, system or development fixture).
type RuntimeArtifactSource string

const (
	RuntimeArtifactSourceNone     RuntimeArtifactSource = "none"
	RuntimeArtifactSourceLocal    RuntimeArtifactSource = "local"
	RuntimeArtifactSourceOfficial RuntimeArtifactSource = "official"
	RuntimeArtifactSourceMirror   RuntimeArtifactSource = "mirror"
)

func (s RuntimeArtifactSource) Valid() bool {
	return s == RuntimeArtifactSourceNone || s == RuntimeArtifactSourceLocal || s == RuntimeArtifactSourceOfficial || s == RuntimeArtifactSourceMirror
}

type NodeOwnership string

const (
	NodeOwnershipSystem  NodeOwnership = "system"
	NodeOwnershipManaged NodeOwnership = "managed"
)

// NodeReleaseInfo is the cached result of an explicit official release
// refresh. Reading Snapshot never refreshes this record.
type NodeReleaseInfo struct {
	Version      string                `json:"version"`
	Platform     string                `json:"platform"`
	Architecture string                `json:"architecture"`
	Filename     string                `json:"filename"`
	SHA256       string                `json:"sha256"`
	Source       RuntimeArtifactSource `json:"source"`
	FallbackUsed bool                  `json:"fallbackUsed"`
	ObservedAt   string                `json:"observedAt"`
}

// DSHReleaseInfo is cached public-registry metadata produced only by an
// explicit refresh. It describes availability, not product compatibility.
type DSHReleaseInfo struct {
	Version      string                `json:"version"`
	Tags         []string              `json:"tags,omitempty"`
	PublishedAt  string                `json:"publishedAt,omitempty"`
	Source       RuntimeArtifactSource `json:"source"`
	FallbackUsed bool                  `json:"fallbackUsed"`
	ObservedAt   string                `json:"observedAt"`
}

// NodeInstallationInfo is one immutable managed installation or the current
// system observation. Paths remain trusted-Settings data and are never copied
// into acquisition provenance.
type NodeInstallationInfo struct {
	ID            string                `json:"id"`
	Version       string                `json:"version"`
	Platform      string                `json:"platform"`
	Architecture  string                `json:"architecture"`
	NodePath      string                `json:"nodePath"`
	NPMPath       string                `json:"npmPath"`
	PNPMPath      string                `json:"pnpmPath,omitempty"`
	Ownership     NodeOwnership         `json:"ownership"`
	InstallSource RuntimeArtifactSource `json:"installSource"`
	SHA256        string                `json:"sha256,omitempty"`
	Installed     bool                  `json:"installed"`
	Removable     bool                  `json:"removable"`
	Verified      bool                  `json:"verified"`
}

// RuntimeInstallObserver receives a safe progress projection. The observer is
// optional and is never a source of lifecycle truth.
type RuntimeInstallObserver func(lifecycle.RuntimePreparation)

// ThemePreference is the DSH-owned appearance preference consumed by dsh-work's
// trusted surfaces. dsh-work does not persist or edit this value.
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
// distribution. Path is the executable (or launcher) path, not a DSH data
// directory.
type RuntimeInfo struct {
	ID            string                `json:"id"`
	Version       string                `json:"version"`
	Path          string                `json:"path"`
	Source        RuntimeSource         `json:"source"`
	Toolchain     RuntimeToolchain      `json:"toolchain,omitempty"`
	InstallSource RuntimeArtifactSource `json:"installSource,omitempty"`
	ToolchainPath string                `json:"toolchainPath,omitempty"`
	Installed     bool                  `json:"installed"`
	Removable     bool                  `json:"removable"`
}

// DataDirectoryInfo is a DSH_HOME identity. Profiles and their plugin state
// live under a data directory and are never properties of a runtime.
type DataDirectoryInfo struct {
	ID        string                 `json:"id"`
	Name      string                 `json:"name"`
	Path      string                 `json:"path"`
	Ownership DataDirectoryOwnership `json:"ownership"`
}

// ProfileRef is intentionally two-dimensional. A profile name is not unique
// across DSH data directories, so every selection and plugin operation must
// carry both the data-directory identity and the profile name.
type ProfileRef struct {
	DataDirectoryID string `json:"dataDirectoryId"`
	Name            string `json:"name"`
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
	Deletable      bool         `json:"deletable"`
	AutoInitialize bool         `json:"autoInitialize"`
	PluginCount    int          `json:"pluginCount"`
	Plugins        []PluginInfo `json:"plugins,omitempty"`
}

type PluginInfo struct {
	Name             string                `json:"name"`
	Version          string                `json:"version,omitempty"`
	Package          string                `json:"package"`
	Spec             string                `json:"spec,omitempty"`
	Installed        bool                  `json:"installed"`
	SourceKind       PluginSourceKind      `json:"sourceKind"`
	SuccessfulRoute  RuntimeArtifactSource `json:"successfulRoute,omitempty"`
	CurrentVersion   string                `json:"currentVersion,omitempty"`
	AvailableVersion string                `json:"availableVersion,omitempty"`
	UpdateCheck      PluginUpdateCheck     `json:"updateCheck"`
}

type PluginSourceKind string

const (
	PluginSourcePublicRegistry  PluginSourceKind = "public-registry"
	PluginSourcePrivateRegistry PluginSourceKind = "private-registry"
	PluginSourceGit             PluginSourceKind = "git"
	PluginSourceURL             PluginSourceKind = "url"
	PluginSourceLocal           PluginSourceKind = "local"
	PluginSourceUnknown         PluginSourceKind = "unknown"
)

type PluginUpdateCheck string

const (
	PluginUpdateUnknown     PluginUpdateCheck = "unknown"
	PluginUpdateCurrent     PluginUpdateCheck = "current"
	PluginUpdateAvailable   PluginUpdateCheck = "available"
	PluginUpdateUnavailable PluginUpdateCheck = "unavailable"
)

type PluginTarget struct {
	// Plugin state is scoped only by the DSH data directory and profile. The
	// manager resolves the current runtime at the mutation boundary.
	Profile ProfileRef `json:"profile"`
}

type PluginInstallRequest struct {
	Target  PluginTarget `json:"target"`
	Package string       `json:"package"`
}

type PluginRemoveRequest struct {
	Target  PluginTarget `json:"target"`
	Package string       `json:"package"`
}

type PluginUpgradeRequest struct {
	Target  PluginTarget `json:"target"`
	Package string       `json:"package"`
}

// PluginProvenanceRecord is bounded app-owned evidence for a successful
// top-level mutation. It intentionally stores no registry URL or credential.
type PluginProvenanceRecord struct {
	Profile         ProfileRef            `json:"profile"`
	Package         string                `json:"package"`
	RequestedSpec   string                `json:"requestedSpec,omitempty"`
	SourceKind      PluginSourceKind      `json:"sourceKind"`
	SuccessfulRoute RuntimeArtifactSource `json:"successfulRoute,omitempty"`
	RecordedAt      string                `json:"recordedAt"`
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

// ProfileCloneRequest copies an existing profile, including built-in profiles,
// into a new unique profile directory. The manager owns the filesystem copy
// because DSH does not expose a public clone command; generated state is
// omitted and reconciled by DSH on the clone's next use.
type ProfileCloneRequest struct {
	Profile ProfileRef `json:"profile"`
}

// ProfileCloneResult reports the new profile identity together with the
// refreshed manager read model. It deliberately does not expose filesystem
// paths to the UI.
type ProfileCloneResult struct {
	Profile  ProfileRef `json:"profile"`
	Snapshot Snapshot   `json:"snapshot"`
}

// ProfileDeleteRequest removes one existing custom profile directory. Built-in
// profiles, selected contexts and a retained failed-switch target are
// protected by the manager.
type ProfileDeleteRequest struct {
	Profile ProfileRef `json:"profile"`
}

// ProfileBackupRequest selects the profile whose user-authored state should
// be archived. Generated dependency trees and caches are excluded by the
// backup implementation.
type ProfileBackupRequest struct {
	Profile ProfileRef `json:"profile"`
}

type ProfileBackupResult struct {
	Profile   ProfileRef `json:"profile"`
	FileName  string     `json:"fileName"`
	Size      int64      `json:"size"`
	CreatedAt string     `json:"createdAt"`
}

type NodeSelectionKind string

const (
	NodeSelectionSystem  NodeSelectionKind = "system"
	NodeSelectionManaged NodeSelectionKind = "managed"
)

type NodeSelection struct {
	Kind           NodeSelectionKind `json:"kind"`
	InstallationID string            `json:"installationId,omitempty"`
}

// RunContext is the complete dsh-work selection that defines one DSH Worker
// generation. Runtime, DSH data directory and profile are one unit and cannot
// be persisted or switched independently.
type RunContext struct {
	RuntimeID string        `json:"runtimeId"`
	Node      NodeSelection `json:"node"`
	Profile   ProfileRef    `json:"profile"`
}

// LaunchRequest is the validation input used by both GUI and CLI callers.
type LaunchRequest struct {
	RuntimeID string        `json:"runtimeId"`
	Node      NodeSelection `json:"node"`
	Profile   ProfileRef    `json:"profile"`
}

type ResolvedNode struct {
	Selection        NodeSelection     `json:"selection"`
	Version          string            `json:"version"`
	NodePath         string            `json:"nodePath"`
	NPMPath          string            `json:"npmPath,omitempty"`
	PNPMPath         string            `json:"pnpmPath,omitempty"`
	ChildEnvironment map[string]string `json:"childEnvironment,omitempty"`
}

// ResolvedLaunch carries the validated catalog records alongside the
// normalized target. Host uses this as one immutable handoff into the DSH
// adapter, so it does not reconstruct data-directory/profile relationships
// itself.
type ResolvedLaunch struct {
	Target        RunContext        `json:"target"`
	Runtime       RuntimeInfo       `json:"runtime"`
	Node          ResolvedNode      `json:"node"`
	DataDirectory DataDirectoryInfo `json:"dataDirectory"`
}

type SwitchAttemptStage string

const (
	SwitchAttemptResolving SwitchAttemptStage = "resolving"
	SwitchAttemptCandidate SwitchAttemptStage = "candidate"
	SwitchAttemptRollback  SwitchAttemptStage = "rollback"
)

type RollbackOutcome string

const (
	RollbackNotNeeded   RollbackOutcome = "not-needed"
	RollbackRestored    RollbackOutcome = "restored"
	RollbackDisabled    RollbackOutcome = "disabled"
	RollbackFailed      RollbackOutcome = "failed"
	RollbackUnavailable RollbackOutcome = "unavailable"
)

// SwitchAttempt is the one bounded terminal result retained for recovery. It
// is process-local and never mutates an immutable runtime installation.
type SwitchAttempt struct {
	Target            RunContext         `json:"target"`
	Stage             SwitchAttemptStage `json:"stage"`
	Failure           lifecycle.Failure  `json:"failure"`
	AutomaticRollback bool               `json:"automaticRollback"`
	Rollback          RollbackOutcome    `json:"rollback"`
	RollbackFailure   *lifecycle.Failure `json:"rollbackFailure,omitempty"`
	CompletedAt       string             `json:"completedAt"`
}

type Snapshot struct {
	Runtimes          []RuntimeInfo          `json:"runtimes"`
	DSHReleases       []DSHReleaseInfo       `json:"dshReleases"`
	Nodes             []NodeInstallationInfo `json:"nodes"`
	SystemNode        *ResolvedNode          `json:"systemNode,omitempty"`
	LatestNode        *NodeReleaseInfo       `json:"latestNode,omitempty"`
	DataDirectories   []DataDirectoryInfo    `json:"dataDirectories"`
	Profiles          []ProfileInfo          `json:"profiles"`
	Configured        *RunContext            `json:"configured,omitempty"`
	Current           *RunContext            `json:"current,omitempty"`
	KnownGood         *RunContext            `json:"knownGood,omitempty"`
	LastSwitchAttempt *SwitchAttempt         `json:"lastSwitchAttempt,omitempty"`
	Theme             ThemePreference        `json:"theme"`
}
