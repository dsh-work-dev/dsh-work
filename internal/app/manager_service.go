package app

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/local/dsh-work/internal/acquisition"
	"github.com/local/dsh-work/internal/dshmanager"
	"github.com/local/dsh-work/internal/lifecycle"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// ManagerService is the trusted Settings-window binding for the nested DSH
// manager. It exposes catalog, Run-context, runtime/data-directory and
// profile-plugin operations; profile composition and plugin mutation still go
// through DSH's public CLI seam, never direct file edits.
type ManagerService struct {
	manager            *dshmanager.Manager
	host               *Host
	publishAcquisition func(acquisition.OperationStatus)
	runtimeMu          sync.Mutex
	runtimeCancel      context.CancelFunc
}

const managerOperationTimeout = 2 * time.Minute
const runtimeOperationTimeout = 15 * time.Minute

func NewManagerService(manager *dshmanager.Manager, host ...*Host) *ManagerService {
	service := &ManagerService{manager: manager}
	if len(host) > 0 {
		service.host = host[0]
	}
	return service
}

// NewManagerServiceWithRuntimeProgress keeps the Wails event publisher at the
// trusted desktop composition boundary. The manager binding still returns the
// durable snapshot; this callback only projects acquisition progress.
func NewManagerServiceWithRuntimeProgress(manager *dshmanager.Manager, host *Host, publish func(acquisition.OperationStatus)) *ManagerService {
	return &ManagerService{manager: manager, host: host, publishAcquisition: publish}
}

func (s *ManagerService) GetSnapshot(ctx context.Context) (dshmanager.Snapshot, error) {
	if s == nil || s.manager == nil {
		return dshmanager.Snapshot{}, managerUnavailable()
	}
	if !isTrustedWindow(ctx, "settings") {
		return dshmanager.Snapshot{}, trustedSurfaceRequired("DSH management is available only in the Settings window.")
	}
	ctx, cancel := managerContext(ctx)
	defer cancel()
	return s.manager.Snapshot(ctx)
}

// GetTheme reads the selected DSH data directory's appearance preference. Settings uses
// it while both trusted windows are open so its surface follows changes made
// by DSH.
func (s *ManagerService) GetTheme(ctx context.Context) dshmanager.ThemePreference {
	if s == nil || s.manager == nil || !isTrustedWindow(ctx, "settings") {
		return dshmanager.ThemePreferenceSystem
	}
	ctx, cancel := managerContext(ctx)
	defer cancel()
	theme, err := s.manager.Theme(ctx)
	if err != nil || !theme.Valid() {
		return dshmanager.ThemePreferenceSystem
	}
	return theme
}

func (s *ManagerService) SetRunContext(ctx context.Context, target dshmanager.RunContext) (dshmanager.Snapshot, error) {
	if s == nil || s.manager == nil {
		return dshmanager.Snapshot{}, managerUnavailable()
	}
	if !isTrustedWindow(ctx, "settings") {
		return dshmanager.Snapshot{}, trustedSurfaceRequired("DSH management is available only in the Settings window.")
	}
	ctx, cancel := managerContext(ctx)
	defer cancel()
	if s.host != nil {
		return s.host.SwitchRunContext(ctx, target)
	}
	return s.manager.SetConfigured(ctx, target)
}

func (s *ManagerService) RetryLastSwitch(ctx context.Context) (dshmanager.Snapshot, error) {
	if s == nil || s.host == nil {
		return dshmanager.Snapshot{}, managerUnavailable()
	}
	if !isTrustedWindow(ctx, "settings") {
		return dshmanager.Snapshot{}, trustedSurfaceRequired("Run context recovery is available only in the Settings window.")
	}
	ctx, cancel := contextWithTimeout(ctx, runtimeOperationTimeout)
	defer cancel()
	return s.host.RetryLastSwitch(ctx)
}

func (s *ManagerService) RestoreKnownGood(ctx context.Context) (dshmanager.Snapshot, error) {
	if s == nil || s.host == nil {
		return dshmanager.Snapshot{}, managerUnavailable()
	}
	if !isTrustedWindow(ctx, "settings") {
		return dshmanager.Snapshot{}, trustedSurfaceRequired("Run context recovery is available only in the Settings window.")
	}
	ctx, cancel := contextWithTimeout(ctx, runtimeOperationTimeout)
	defer cancel()
	return s.host.RestoreKnownGood(ctx)
}

func (s *ManagerService) InstallRuntime(ctx context.Context, version string) (dshmanager.Snapshot, error) {
	if s == nil || s.manager == nil {
		return dshmanager.Snapshot{}, managerUnavailable()
	}
	if !isTrustedWindow(ctx, "settings") {
		return dshmanager.Snapshot{}, trustedSurfaceRequired("DSH management is available only in the Settings window.")
	}
	ctx, finish, err := s.beginRuntimeOperation(ctx, "A DSH runtime preparation is already in progress.")
	if err != nil {
		return dshmanager.Snapshot{}, err
	}
	defer finish()
	operationID := lifecycle.NewCorrelationID()
	artifact := acquisition.ArtifactIdentity{Kind: acquisition.ArtifactDSH, Name: "@deepseek-ai/dsh", Version: version}
	var lastPreparation lifecycle.RuntimePreparation
	snapshot, err := s.manager.InstallRuntimeWithProgress(ctx, version, func(preparation lifecycle.RuntimePreparation) {
		s.publishObservedPreparation(operationID, artifact, &lastPreparation, preparation)
	})
	s.publishTerminalPreparation(operationID, artifact, version, lastPreparation, err)
	return snapshot, err
}

func (s *ManagerService) RefreshLatestNode(ctx context.Context) (dshmanager.Snapshot, error) {
	if s == nil || s.manager == nil {
		return dshmanager.Snapshot{}, managerUnavailable()
	}
	if !isTrustedWindow(ctx, "settings") {
		return dshmanager.Snapshot{}, trustedSurfaceRequired("Node release refresh is available only in the Settings window.")
	}
	ctx, cancel := contextWithTimeout(ctx, runtimeOperationTimeout)
	defer cancel()
	return s.manager.RefreshLatestNode(ctx)
}

func (s *ManagerService) RefreshDSHReleases(ctx context.Context) (dshmanager.Snapshot, error) {
	if s == nil || s.manager == nil {
		return dshmanager.Snapshot{}, managerUnavailable()
	}
	if !isTrustedWindow(ctx, "settings") {
		return dshmanager.Snapshot{}, trustedSurfaceRequired("DSH release refresh is available only in the Settings window.")
	}
	ctx, cancel := contextWithTimeout(ctx, runtimeOperationTimeout)
	defer cancel()
	return s.manager.RefreshDSHReleases(ctx)
}

func (s *ManagerService) InstallLatestNode(ctx context.Context) (dshmanager.Snapshot, error) {
	if s == nil || s.manager == nil {
		return dshmanager.Snapshot{}, managerUnavailable()
	}
	if !isTrustedWindow(ctx, "settings") {
		return dshmanager.Snapshot{}, trustedSurfaceRequired("Node installation is available only in the Settings window.")
	}
	ctx, finish, err := s.beginRuntimeOperation(ctx, "An acquisition is already in progress.")
	if err != nil {
		return dshmanager.Snapshot{}, err
	}
	defer finish()
	operationID := lifecycle.NewCorrelationID()
	artifact := acquisition.ArtifactIdentity{Kind: acquisition.ArtifactNode, Name: "node"}
	var lastPreparation lifecycle.RuntimePreparation
	snapshot, err := s.manager.InstallLatestNode(ctx, func(preparation lifecycle.RuntimePreparation) {
		s.publishObservedPreparation(operationID, artifact, &lastPreparation, preparation)
	})
	version := lastPreparation.TargetVersion
	if snapshot.LatestNode != nil {
		version = snapshot.LatestNode.Version
	}
	s.publishTerminalPreparation(operationID, artifact, version, lastPreparation, err)
	return snapshot, err
}

func (s *ManagerService) beginRuntimeOperation(ctx context.Context, busySummary string) (context.Context, func(), error) {
	operationContext, cancel := contextWithTimeout(ctx, runtimeOperationTimeout)
	s.runtimeMu.Lock()
	if s.runtimeCancel != nil {
		s.runtimeMu.Unlock()
		cancel()
		return nil, nil, lifecycle.Failure{
			Code: lifecycle.ErrorManagerOperationBusy, Summary: busySummary,
			Retryable: true, CorrelationID: lifecycle.NewCorrelationID(),
		}
	}
	s.runtimeCancel = cancel
	s.runtimeMu.Unlock()
	return operationContext, func() {
		s.runtimeMu.Lock()
		s.runtimeCancel = nil
		s.runtimeMu.Unlock()
		cancel()
	}, nil
}

func (s *ManagerService) publishTerminalPreparation(operationID string, artifact acquisition.ArtifactIdentity, targetVersion string, last lifecycle.RuntimePreparation, err error) {
	if s.publishAcquisition == nil {
		return
	}
	// A DSH install may first acquire Node. If that nested step fails, keep the
	// terminal event on the Node card rather than misreporting it as a DSH
	// package failure. The nested Node step is the last observed preparation at
	// this point, while the DSH step has not started yet.
	if last.State == lifecycle.RuntimePreparationAcquiringNode || last.Operation == lifecycle.RuntimePreparationOperationDownloadNode {
		artifact = acquisition.ArtifactIdentity{Kind: acquisition.ArtifactNode, Name: "node", Version: last.TargetVersion}
		if last.TargetVersion != "" {
			targetVersion = last.TargetVersion
		}
	}
	source := last.Source
	if source == "" {
		source = lifecycle.RuntimePreparationSourceNone
	}
	if err == nil {
		s.publishOperationStatus(operationID, artifact, lifecycle.RuntimePreparation{
			State: lifecycle.RuntimePreparationInstalled, Operation: lifecycle.RuntimePreparationOperationNone,
			TargetVersion: targetVersion, Source: source, CanCancel: false,
		})
		return
	}
	failure := runtimePreparationFailure(err)
	state := lifecycle.RuntimePreparationFailed
	if failure.Code == lifecycle.ErrorCancelled {
		state = lifecycle.RuntimePreparationCancelled
	}
	s.publishOperationStatus(operationID, artifact, lifecycle.RuntimePreparation{
		State: state, Operation: lifecycle.RuntimePreparationOperationNone,
		TargetVersion: targetVersion, Source: source, CanCancel: false, Error: &failure,
	})
}

// publishObservedPreparation keeps adapter progress visible while withholding
// adapter terminal states. The Manager can still fail during verification,
// persistence, or candidate cleanup, so only the service method's returned
// result is allowed to publish the operation's single terminal status.
func (s *ManagerService) publishObservedPreparation(operationID string, artifact acquisition.ArtifactIdentity, last *lifecycle.RuntimePreparation, preparation lifecycle.RuntimePreparation) {
	if last != nil {
		*last = preparation
	}
	if preparationIsTerminal(preparation.State) {
		return
	}
	s.publishOperationStatus(operationID, artifact, preparation)
}

func (s *ManagerService) publishOperationStatus(operationID string, base acquisition.ArtifactIdentity, preparation lifecycle.RuntimePreparation) {
	if s.publishAcquisition == nil {
		return
	}
	artifact := base
	if preparation.State == lifecycle.RuntimePreparationAcquiringNode || preparation.Operation == lifecycle.RuntimePreparationOperationDownloadNode {
		artifact = acquisition.ArtifactIdentity{Kind: acquisition.ArtifactNode, Name: "node"}
	}
	if preparation.TargetVersion != "" {
		artifact.Version = preparation.TargetVersion
	}
	route := acquisition.Route(preparation.Source)
	if route != acquisition.RouteOfficial && route != acquisition.RouteMirror && route != acquisition.RouteLocal {
		route = ""
	}
	state := acquisition.OperationActive
	var result *acquisition.OperationResult
	switch preparation.State {
	case lifecycle.RuntimePreparationInstalled:
		state = acquisition.OperationSucceeded
		result = &acquisition.OperationResult{Succeeded: true, Route: route}
	case lifecycle.RuntimePreparationCancelled:
		state = acquisition.OperationCanceled
		failure := acquisition.OperationFailure{Kind: acquisition.FailureCanceled, Summary: "Artifact acquisition was canceled.", Retryable: true}
		result = &acquisition.OperationResult{Route: route, Failure: &failure}
	case lifecycle.RuntimePreparationFailed:
		state = acquisition.OperationFailed
		failure := acquisition.OperationFailure{Kind: acquisition.FailureSemantic, Summary: "Artifact acquisition failed.", Retryable: true}
		if preparation.Error != nil {
			failure.Summary = preparation.Error.Summary
			failure.Retryable = preparation.Error.Retryable
		}
		result = &acquisition.OperationResult{Route: route, Failure: &failure}
	}
	attempt := 0
	if route == acquisition.RouteOfficial || route == acquisition.RouteLocal {
		attempt = 1
	} else if route == acquisition.RouteMirror {
		attempt = 2
	}
	s.publishAcquisition(acquisition.OperationStatus{
		OperationID: operationID, Artifact: artifact, State: state, Step: string(preparation.Operation),
		Route: route, Attempt: attempt, ReceivedBytes: preparation.ReceivedBytes, TotalBytes: preparation.TotalBytes,
		HasTotal: preparation.HasTotal, CanCancel: preparation.CanCancel, Result: result,
	})
}

func preparationIsTerminal(state lifecycle.RuntimePreparationState) bool {
	return state == lifecycle.RuntimePreparationInstalled || state == lifecycle.RuntimePreparationFailed || state == lifecycle.RuntimePreparationCancelled
}

func runtimePreparationFailure(err error) lifecycle.Failure {
	var failure lifecycle.Failure
	if errors.As(err, &failure) {
		if failure.CorrelationID == "" {
			failure.CorrelationID = lifecycle.NewCorrelationID()
		}
		return failure
	}
	return lifecycle.Failure{
		Code: lifecycle.ErrorRuntimeInstallFailed, Summary: "Runtime preparation failed.",
		Retryable: true, CorrelationID: lifecycle.NewCorrelationID(),
	}
}

func (s *ManagerService) RemoveNode(ctx context.Context, id string) (dshmanager.Snapshot, error) {
	if s == nil || s.manager == nil {
		return dshmanager.Snapshot{}, managerUnavailable()
	}
	if !isTrustedWindow(ctx, "settings") {
		return dshmanager.Snapshot{}, trustedSurfaceRequired("Node removal is available only in the Settings window.")
	}
	ctx, cancel := managerContext(ctx)
	defer cancel()
	return s.manager.RemoveNode(ctx, id)
}

// CancelRuntime cancels the active explicit runtime pull from the trusted
// Settings surface. Cancellation leaves no catalog entry; the installer owns
// cleanup of its staging directory.
func (s *ManagerService) CancelRuntime(ctx context.Context) error {
	if s == nil || s.manager == nil {
		return managerUnavailable()
	}
	if !isTrustedWindow(ctx, "settings") {
		return trustedSurfaceRequired("DSH runtime cancellation is available only in the Settings window.")
	}
	s.runtimeMu.Lock()
	cancel := s.runtimeCancel
	s.runtimeMu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

func (s *ManagerService) CancelAcquisition(ctx context.Context) error {
	return s.CancelRuntime(ctx)
}

func (s *ManagerService) RemoveRuntime(ctx context.Context, id string) (dshmanager.Snapshot, error) {
	if s == nil || s.manager == nil {
		return dshmanager.Snapshot{}, managerUnavailable()
	}
	if !isTrustedWindow(ctx, "settings") {
		return dshmanager.Snapshot{}, trustedSurfaceRequired("DSH management is available only in the Settings window.")
	}
	ctx, cancel := managerContext(ctx)
	defer cancel()
	return s.manager.RemoveRuntime(ctx, id)
}

func (s *ManagerService) RegisterDataDirectory(ctx context.Context, dataDirectory dshmanager.DataDirectoryInfo) (dshmanager.Snapshot, error) {
	if s == nil || s.manager == nil {
		return dshmanager.Snapshot{}, managerUnavailable()
	}
	if !isTrustedWindow(ctx, "settings") {
		return dshmanager.Snapshot{}, trustedSurfaceRequired("DSH management is available only in the Settings window.")
	}
	ctx, cancel := managerContext(ctx)
	defer cancel()
	return s.manager.RegisterDataDirectory(ctx, dataDirectory)
}

func (s *ManagerService) RemoveDataDirectory(ctx context.Context, id string) (dshmanager.Snapshot, error) {
	if s == nil || s.manager == nil {
		return dshmanager.Snapshot{}, managerUnavailable()
	}
	if !isTrustedWindow(ctx, "settings") {
		return dshmanager.Snapshot{}, trustedSurfaceRequired("DSH management is available only in the Settings window.")
	}
	ctx, cancel := managerContext(ctx)
	defer cancel()
	return s.manager.RemoveDataDirectory(ctx, id)
}

func (s *ManagerService) ResolveLaunch(ctx context.Context, request dshmanager.LaunchRequest) (dshmanager.ResolvedLaunch, error) {
	if s == nil || s.manager == nil {
		return dshmanager.ResolvedLaunch{}, managerUnavailable()
	}
	if !isTrustedWindow(ctx, "settings") {
		return dshmanager.ResolvedLaunch{}, trustedSurfaceRequired("DSH management is available only in the Settings window.")
	}
	ctx, cancel := managerContext(ctx)
	defer cancel()
	return s.manager.ResolveLaunch(ctx, request)
}

func (s *ManagerService) ListPlugins(ctx context.Context, request dshmanager.PluginListRequest) ([]dshmanager.PluginInfo, error) {
	if s == nil || s.manager == nil {
		return nil, managerUnavailable()
	}
	if !isTrustedWindow(ctx, "settings") {
		return nil, trustedSurfaceRequired("DSH management is available only in the Settings window.")
	}
	ctx, cancel := managerContext(ctx)
	defer cancel()
	return s.manager.ListPlugins(ctx, request)
}

func (s *ManagerService) InstallPlugin(ctx context.Context, request dshmanager.PluginInstallRequest) (dshmanager.PluginResult, error) {
	if s == nil || s.manager == nil {
		return dshmanager.PluginResult{}, managerUnavailable()
	}
	if !isTrustedWindow(ctx, "settings") {
		return dshmanager.PluginResult{}, trustedSurfaceRequired("DSH management is available only in the Settings window.")
	}
	ctx, cancel := managerContext(ctx)
	defer cancel()
	if s.host != nil {
		return s.host.InstallPlugin(ctx, request)
	}
	return s.manager.InstallPlugin(ctx, request)
}

func (s *ManagerService) RemovePlugin(ctx context.Context, request dshmanager.PluginRemoveRequest) (dshmanager.PluginResult, error) {
	if s == nil || s.manager == nil {
		return dshmanager.PluginResult{}, managerUnavailable()
	}
	if !isTrustedWindow(ctx, "settings") {
		return dshmanager.PluginResult{}, trustedSurfaceRequired("DSH management is available only in the Settings window.")
	}
	ctx, cancel := managerContext(ctx)
	defer cancel()
	if s.host != nil {
		return s.host.RemovePlugin(ctx, request)
	}
	return s.manager.RemovePlugin(ctx, request)
}

func (s *ManagerService) UpgradePlugin(ctx context.Context, request dshmanager.PluginUpgradeRequest) (dshmanager.PluginResult, error) {
	if s == nil || s.manager == nil {
		return dshmanager.PluginResult{}, managerUnavailable()
	}
	if !isTrustedWindow(ctx, "settings") {
		return dshmanager.PluginResult{}, trustedSurfaceRequired("DSH profile plugins are available only in the Settings window.")
	}
	ctx, cancel := managerContext(ctx)
	defer cancel()
	if s.host != nil {
		return s.host.UpgradePlugin(ctx, request)
	}
	return s.manager.UpgradePlugin(ctx, request)
}

func (s *ManagerService) RenameProfile(ctx context.Context, request dshmanager.ProfileRenameRequest) (dshmanager.Snapshot, error) {
	if s == nil || s.manager == nil {
		return dshmanager.Snapshot{}, managerUnavailable()
	}
	if !isTrustedWindow(ctx, "settings") {
		return dshmanager.Snapshot{}, trustedSurfaceRequired("DSH management is available only in the Settings window.")
	}
	ctx, cancel := managerContext(ctx)
	defer cancel()
	return s.manager.RenameProfile(ctx, request)
}

func (s *ManagerService) CloneProfile(ctx context.Context, request dshmanager.ProfileCloneRequest) (dshmanager.ProfileCloneResult, error) {
	if s == nil || s.manager == nil {
		return dshmanager.ProfileCloneResult{}, managerUnavailable()
	}
	if !isTrustedWindow(ctx, "settings") {
		return dshmanager.ProfileCloneResult{}, trustedSurfaceRequired("Profile cloning is available only in the Settings window.")
	}
	ctx, cancel := managerContext(ctx)
	defer cancel()
	return s.manager.CloneProfile(ctx, request)
}

func (s *ManagerService) DeleteProfile(ctx context.Context, request dshmanager.ProfileDeleteRequest) (dshmanager.Snapshot, error) {
	if s == nil || s.manager == nil {
		return dshmanager.Snapshot{}, managerUnavailable()
	}
	if !isTrustedWindow(ctx, "settings") {
		return dshmanager.Snapshot{}, trustedSurfaceRequired("Profile deletion is available only in the Settings window.")
	}
	ctx, cancel := managerContext(ctx)
	defer cancel()
	return s.manager.DeleteProfile(ctx, request)
}

func (s *ManagerService) BackupProfile(ctx context.Context, request dshmanager.ProfileBackupRequest) (dshmanager.ProfileBackupResult, error) {
	if s == nil || s.manager == nil {
		return dshmanager.ProfileBackupResult{}, managerUnavailable()
	}
	if !isTrustedWindow(ctx, "settings") {
		return dshmanager.ProfileBackupResult{}, trustedSurfaceRequired("Profile backups are available only in the Settings window.")
	}
	ctx, cancel := managerContext(ctx)
	defer cancel()
	return s.manager.BackupProfile(ctx, request)
}

func managerContext(parent context.Context) (context.Context, context.CancelFunc) {
	return contextWithTimeout(parent, managerOperationTimeout)
}

func contextWithTimeout(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(parent, timeout)
}

func isTrustedWindow(ctx context.Context, name string) bool {
	if ctx == nil {
		return false
	}
	window, ok := ctx.Value(application.WindowKey).(application.Window)
	return ok && window != nil && window.Name() == name
}

func trustedSurfaceRequired(detail string) error {
	return lifecycle.Failure{
		Code:          lifecycle.ErrorTrustedSurfaceRequired,
		Summary:       "This dsh-work control is available only on its trusted surface.",
		Detail:        detail,
		CorrelationID: lifecycle.NewCorrelationID(),
	}
}

func managerUnavailable() error {
	return lifecycle.Failure{
		Code:          lifecycle.ErrorManagerStateInvalid,
		Summary:       "The DSH manager in dsh-work Settings is unavailable.",
		Retryable:     true,
		CorrelationID: lifecycle.NewCorrelationID(),
		Detail:        "Restart dsh-work and try again.",
	}
}
