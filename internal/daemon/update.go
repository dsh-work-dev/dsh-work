package daemon

// UpdatePhase is the user-visible phase of the daemon-owned application
// updater. The desktop UI renders this state; it does not infer updater state
// from local process or temporary-file details.
type UpdatePhase string

const (
	UpdateUnconfigured UpdatePhase = "unconfigured"
	UpdateIdle         UpdatePhase = "idle"
	UpdateChecking     UpdatePhase = "checking"
	UpdateUpToDate     UpdatePhase = "up-to-date"
	UpdateAvailable    UpdatePhase = "available"
	UpdateDownloading  UpdatePhase = "downloading"
	UpdateVerifying    UpdatePhase = "verifying"
	UpdateReady        UpdatePhase = "ready"
	UpdateInstalling   UpdatePhase = "installing"
	UpdateError        UpdatePhase = "error"
)

// UpdateInstallMode tells the UI which explicit install action is available.
// This release unit is replaced by running the signed installer, which keeps the
// graphical and command-line executables on one version.
type UpdateInstallMode string

const (
	UpdateInstallNone      UpdateInstallMode = ""
	UpdateInstallInstaller UpdateInstallMode = "installer"
)

// UpdateSnapshot is safe to expose over the trusted local IPC boundary. It
// deliberately omits provider internals, staging paths and raw errors.
type UpdateSnapshot struct {
	Phase           UpdatePhase       `json:"phase"`
	CurrentVersion  string            `json:"currentVersion"`
	BuildChannel    string            `json:"buildChannel,omitempty"`
	SelectedChannel string            `json:"selectedChannel,omitempty"`
	TargetVersion   string            `json:"targetVersion,omitempty"`
	ReceivedBytes   int64             `json:"receivedBytes,omitempty"`
	TotalBytes      int64             `json:"totalBytes,omitempty"`
	LastCheckedAt   string            `json:"lastCheckedAt,omitempty"`
	ErrorCode       string            `json:"errorCode,omitempty"`
	InstallMode     UpdateInstallMode `json:"installMode"`
}

type UpdateAction string

const (
	UpdateActionCheck    UpdateAction = "check"
	UpdateActionDownload UpdateAction = "download"
	UpdateActionInstall  UpdateAction = "install"
)
