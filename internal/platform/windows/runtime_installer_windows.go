//go:build windows

package windows

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/local/dsh-work/internal/acquisition"
	"github.com/local/dsh-work/internal/dshadapter"
	"github.com/local/dsh-work/internal/dshmanager"
	"github.com/local/dsh-work/internal/lifecycle"
)

const (
	defaultOfficialRegistry  = "https://registry.npmjs.org/"
	defaultMirrorRegistry    = "https://registry.npmmirror.com/"
	defaultNodeDownloadLimit = int64(512 * 1024 * 1024)
)

var (
	errSystemToolchainUnavailable = errors.New("no usable system Node.js toolchain is available")
	errArtifactIntegrity          = errors.New("downloaded artifact integrity check failed")
)

// RuntimeInstallerOption is a test and distribution seam for the native
// installer. Production construction uses the official defaults; tests can
// replace probing and downloading without changing process-wide PATH or cache
// settings.
type RuntimeInstallerOption func(*RuntimeInstaller)

// RuntimeInstaller performs a DSH package installation into dsh-work's
// managed runtime store. It never changes the user's global Node/npm/pnpm
// configuration. The selected toolchain is passed only to child processes.
type RuntimeInstaller struct {
	executor         dshadapter.CommandExecutor
	storeRoot        string
	resolver         runtimeToolchainResolver
	lookup           func(string) (string, error)
	officialRegistry string
	mirrorRegistry   string
	pendingMu        sync.Mutex
	pendingBackups   map[string]string
}

// NewRuntimeInstaller returns the Windows runtime acquisition adapter. DSH and
// Node are intentionally not bundled in the application; this adapter only
// materializes them after a requested or first-use runtime preparation path.
func NewRuntimeInstaller(executor dshadapter.CommandExecutor, storeRoot string, options ...RuntimeInstallerOption) *RuntimeInstaller {
	installer := &RuntimeInstaller{
		executor:         executor,
		storeRoot:        storeRoot,
		lookup:           exec.LookPath,
		officialRegistry: defaultOfficialRegistry,
		mirrorRegistry:   defaultMirrorRegistry,
		pendingBackups:   make(map[string]string),
	}
	for _, option := range options {
		if option != nil {
			option(installer)
		}
	}
	if installer.resolver == nil {
		installer.resolver = systemToolchainResolver{
			executor: installer.executor,
			lookup:   installer.lookup,
		}
	}
	return installer
}

// WithRuntimeToolchainResolver replaces system toolchain detection. It is an
// option rather than a public global setting so child-process behavior remains
// local to this runtime operation.
func WithRuntimeToolchainResolver(resolver runtimeToolchainResolver) RuntimeInstallerOption {
	return func(installer *RuntimeInstaller) { installer.resolver = resolver }
}

// WithRuntimeExecutableLookup replaces PATH lookup for toolchain tests.
func WithRuntimeExecutableLookup(lookup func(string) (string, error)) RuntimeInstallerOption {
	return func(installer *RuntimeInstaller) { installer.lookup = lookup }
}

// WithRuntimeRegistries configures the official-first registry pair. An empty
// mirror disables package-registry fallback; the official source remains the
// first attempt.
func WithRuntimeRegistries(official, mirror string) RuntimeInstallerOption {
	return func(installer *RuntimeInstaller) {
		if strings.TrimSpace(official) != "" {
			installer.officialRegistry = strings.TrimRight(strings.TrimSpace(official), "/") + "/"
		}
		installer.mirrorRegistry = strings.TrimRight(strings.TrimSpace(mirror), "/")
		if installer.mirrorRegistry != "" {
			installer.mirrorRegistry += "/"
		}
	}
}

func (i *RuntimeInstaller) Install(ctx context.Context, version string) (dshmanager.RuntimeInfo, error) {
	return i.InstallWithProgress(ctx, version, nil)
}

func (i *RuntimeInstaller) InstallWithProgress(ctx context.Context, version string, observer dshmanager.RuntimeInstallObserver) (dshmanager.RuntimeInfo, error) {
	return i.InstallWithNodes(ctx, version, nil, observer)
}

// RequiresNodeAcquisition probes only availability. It never downloads and is
// called only inside an explicit DSH install operation so Manager can own the
// independent Node catalog transaction.
func (i *RuntimeInstaller) RequiresNodeAcquisition(ctx context.Context, nodes []dshmanager.NodeInstallationInfo) (bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if i == nil || i.executor == nil {
		return false, runtimeFailure(lifecycle.ErrorRuntimeInstallUnavailable, "The Windows runtime command adapter is unavailable.", false)
	}
	i.initializeDefaults()
	if _, err := i.resolver.Resolve(ctx, nil); err == nil {
		return false, nil
	} else if !errors.Is(err, errSystemToolchainUnavailable) {
		return false, err
	}
	for _, node := range nodes {
		if node.Ownership != dshmanager.NodeOwnershipManaged || !node.Installed || !node.Verified || node.NodePath == "" || node.NPMPath == "" {
			continue
		}
		if validateManagedNodeInstallation(i.storeRoot, node) != nil {
			continue
		}
		if verifyNodeExecutables(ctx, i.executor, node.NodePath, node.NPMPath, node.Version) == nil {
			return false, nil
		}
	}
	if err := preparationContextError(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func (i *RuntimeInstaller) InstallWithNodes(ctx context.Context, version string, nodes []dshmanager.NodeInstallationInfo, observer dshmanager.RuntimeInstallObserver) (dshmanager.RuntimeInfo, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if i == nil || i.executor == nil {
		return dshmanager.RuntimeInfo{}, runtimeFailure(lifecycle.ErrorRuntimeInstallUnavailable, "The Windows runtime command adapter is unavailable.", false)
	}
	i.initializeDefaults()
	if !runtimeVersionPattern.MatchString(version) {
		return dshmanager.RuntimeInfo{}, runtimeFailure(lifecycle.ErrorRuntimeInstallFailed, "The requested DSH runtime version is invalid.", false)
	}
	storeRoot, err := managedStoreRoot(i.storeRoot)
	if err != nil {
		return dshmanager.RuntimeInfo{}, runtimeFailure(lifecycle.ErrorRuntimeInstallFailed, "The dsh-work runtime store is unavailable.", false)
	}

	emitPreparation(observer, lifecycle.RuntimePreparation{
		State:         lifecycle.RuntimePreparationResolvingToolchain,
		Operation:     lifecycle.RuntimePreparationOperationDetectNode,
		TargetVersion: version,
		Toolchain:     string(dshmanager.RuntimeToolchainNone),
		Source:        lifecycle.RuntimePreparationSourceNone,
		CanCancel:     true,
	})
	toolchain, err := i.resolver.Resolve(ctx, observer)
	if cancellation := preparationContextError(ctx); cancellation != nil {
		return dshmanager.RuntimeInfo{}, i.failPreparation(version, cancellation, observer)
	}
	if err != nil && !errors.Is(err, errSystemToolchainUnavailable) {
		return dshmanager.RuntimeInfo{}, i.failPreparation(version, err, observer)
	}
	if errors.Is(err, errSystemToolchainUnavailable) {
		toolchain, err = i.resolveManagedNode(ctx, nodes, version, observer)
		if err != nil {
			return dshmanager.RuntimeInfo{}, err
		}
	}

	return i.installDSH(ctx, storeRoot, version, toolchain, observer)
}

func (i *RuntimeInstaller) resolveManagedNode(ctx context.Context, nodes []dshmanager.NodeInstallationInfo, targetVersion string, observer dshmanager.RuntimeInstallObserver) (runtimeToolchain, error) {
	var selected *dshmanager.NodeInstallationInfo
	for index := range nodes {
		node := &nodes[index]
		if node.Ownership != dshmanager.NodeOwnershipManaged || !node.Installed || !node.Verified || node.NodePath == "" || node.NPMPath == "" {
			continue
		}
		if selected == nil || compareNodeVersions(node.Version, selected.Version) > 0 {
			selected = node
		}
	}
	if selected == nil {
		return runtimeToolchain{}, i.failPreparation(targetVersion, runtimeFailure(lifecycle.ErrorRuntimeInstallUnavailable, "No usable Node toolchain is available.", false), observer)
	}
	if err := validateManagedNodeInstallation(i.storeRoot, *selected); err != nil {
		return runtimeToolchain{}, i.failPreparation(targetVersion, runtimeFailure(lifecycle.ErrorRuntimeInstallUnavailable, "The selected managed Node installation could not be verified.", false), observer)
	}
	environment := childPathEnvironment(filepath.Dir(selected.NodePath))
	nodeResult, err := i.executor.Run(ctx, selected.NodePath, []string{"--version"}, environment, "")
	if err != nil || strings.TrimSpace(dshadapter.ParseVersion(nodeResult.Stdout+"\n"+nodeResult.Stderr)) != strings.TrimPrefix(selected.Version, "v") {
		return runtimeToolchain{}, i.failPreparation(targetVersion, runtimeFailure(lifecycle.ErrorRuntimeInstallUnavailable, "The selected managed Node installation could not be verified.", false), observer)
	}
	npmResult, err := i.executor.Run(ctx, selected.NPMPath, []string{"--version"}, environment, "")
	if err != nil || strings.TrimSpace(npmResult.Stdout+"\n"+npmResult.Stderr) == "" {
		return runtimeToolchain{}, i.failPreparation(targetVersion, runtimeFailure(lifecycle.ErrorRuntimeInstallUnavailable, "The selected managed npm installation could not be verified.", false), observer)
	}
	return runtimeToolchain{kind: dshmanager.RuntimeToolchainManagedNodeNPM, version: strings.TrimPrefix(selected.Version, "v"), nodePath: selected.NodePath, packageManagerPath: selected.NPMPath, env: environment}, nil
}

func compareNodeVersions(left, right string) int {
	left = strings.TrimPrefix(left, "v")
	right = strings.TrimPrefix(right, "v")
	leftParts := strings.Split(left, ".")
	rightParts := strings.Split(right, ".")
	for index := 0; index < 3 && index < len(leftParts) && index < len(rightParts); index++ {
		leftNumber, _ := strconv.ParseUint(leftParts[index], 10, 64)
		rightNumber, _ := strconv.ParseUint(rightParts[index], 10, 64)
		if leftNumber < rightNumber {
			return -1
		}
		if leftNumber > rightNumber {
			return 1
		}
	}
	return 0
}

func (i *RuntimeInstaller) initializeDefaults() {
	if i.lookup == nil {
		i.lookup = exec.LookPath
	}
	if i.officialRegistry == "" {
		i.officialRegistry = defaultOfficialRegistry
	}
	if i.resolver == nil {
		i.resolver = systemToolchainResolver{executor: i.executor, lookup: i.lookup}
	}
	if i.pendingBackups == nil {
		i.pendingBackups = make(map[string]string)
	}
}

// Discard removes only a managed candidate under this installer's exact store
// root. It is used after DSH adapter verification rejects an otherwise staged
// candidate. Current/known-good runtime ownership is enforced by the manager
// before removal.
func (i *RuntimeInstaller) Discard(_ context.Context, runtime dshmanager.RuntimeInfo) error {
	if i == nil {
		return nil
	}
	root, err := managedStoreRoot(i.storeRoot)
	if err != nil {
		return err
	}
	if runtime.Source != "" && runtime.Source != dshmanager.RuntimeSourceManaged {
		return nil
	}
	if runtime.InstallSource == dshmanager.RuntimeArtifactSourceLocal {
		return nil
	}
	if runtime.ID == "" || !strings.HasPrefix(runtime.ID, "dsh-") {
		return nil
	}
	target := filepath.Join(root, runtime.ID)
	if !isDirectManagedChild(root, target) {
		return errors.New("runtime candidate is outside the managed store")
	}
	if backup, ok := i.pendingBackup(target); ok {
		if err := os.RemoveAll(target); err != nil {
			return err
		}
		if err := os.Rename(backup, target); err != nil {
			return err
		}
		i.clearPendingBackup(target)
		return nil
	}
	return os.RemoveAll(target)
}

// Finalize releases the previous same-version runtime after the manager has
// persisted the new catalog entry. A cleanup error leaves the backup in place
// so a later explicit cleanup can still recover it.
func (i *RuntimeInstaller) Finalize(_ context.Context, runtime dshmanager.RuntimeInfo) error {
	if i == nil {
		return nil
	}
	root, err := managedStoreRoot(i.storeRoot)
	if err != nil {
		return err
	}
	if runtime.Source != "" && runtime.Source != dshmanager.RuntimeSourceManaged {
		return nil
	}
	if runtime.ID == "" || !strings.HasPrefix(runtime.ID, "dsh-") {
		return nil
	}
	target := filepath.Join(root, runtime.ID)
	if !isDirectManagedChild(root, target) {
		return errors.New("runtime candidate is outside the managed store")
	}
	backup, ok := i.pendingBackup(target)
	if !ok {
		return nil
	}
	if err := os.RemoveAll(backup); err != nil {
		return err
	}
	i.clearPendingBackup(target)
	return nil
}

// Remove deletes a managed runtime directory after the manager has checked that
// no Run context uses it. The directory is first renamed aside so a locked file
// leaves the runtime intact; a partial delete of the renamed copy is retried on
// the next removal.
func (i *RuntimeInstaller) Remove(_ context.Context, runtime dshmanager.RuntimeInfo) error {
	if i == nil {
		return nil
	}
	root, err := managedStoreRoot(i.storeRoot)
	if err != nil {
		return err
	}
	if runtime.Source != "" && runtime.Source != dshmanager.RuntimeSourceManaged {
		return errors.New("runtime is not managed by dsh-work")
	}
	target := filepath.Join(root, runtime.ID)
	if !strings.HasPrefix(runtime.ID, "dsh-") || !isDirectManagedChild(root, target) || !isWithinDirectory(target, runtime.Path) {
		return errors.New("runtime is outside the managed store")
	}
	removeAbandonedRuntimes(root)
	if _, err := os.Stat(target); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	aside, err := os.MkdirTemp(root, ".removing-"+runtime.ID+"-")
	if err != nil {
		return err
	}
	if err := os.Remove(aside); err != nil {
		return err
	}
	if err := os.Rename(target, aside); err != nil {
		return err
	}
	_ = os.RemoveAll(aside)
	return nil
}

func removeAbandonedRuntimes(root string) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), ".removing-") {
			_ = os.RemoveAll(filepath.Join(root, entry.Name()))
		}
	}
}

func (i *RuntimeInstaller) rememberPendingBackup(target, backup string) {
	i.pendingMu.Lock()
	i.pendingBackups[target] = backup
	i.pendingMu.Unlock()
}

func (i *RuntimeInstaller) pendingBackup(target string) (string, bool) {
	i.pendingMu.Lock()
	backup, ok := i.pendingBackups[target]
	i.pendingMu.Unlock()
	return backup, ok
}

func (i *RuntimeInstaller) clearPendingBackup(target string) {
	i.pendingMu.Lock()
	delete(i.pendingBackups, target)
	i.pendingMu.Unlock()
}

func (i *RuntimeInstaller) installDSH(ctx context.Context, storeRoot, version string, toolchain runtimeToolchain, observer dshmanager.RuntimeInstallObserver) (runtime dshmanager.RuntimeInfo, err error) {
	destination := filepath.Join(storeRoot, "dsh-"+version)
	if destinationInfo, statErr := os.Stat(destination); statErr == nil && destinationInfo.IsDir() {
		executable := runtimeExecutablePath(destination)
		if verifiedVersion, err := i.verifyDSH(ctx, executable, version, toolchain, observer); err == nil && verifiedVersion == version {
			emitPreparation(observer, lifecycle.RuntimePreparation{
				State:         lifecycle.RuntimePreparationInstalled,
				Operation:     lifecycle.RuntimePreparationOperationNone,
				TargetVersion: version,
				Toolchain:     string(toolchain.kind),
				Source:        lifecycle.RuntimePreparationSourceLocal,
				CanCancel:     false,
			})
			return dshmanager.RuntimeInfo{
				ID:            "dsh-" + version,
				Version:       version,
				Path:          executable,
				Source:        dshmanager.RuntimeSourceManaged,
				Toolchain:     toolchain.kind,
				InstallSource: dshmanager.RuntimeArtifactSourceLocal,
				ToolchainPath: filepath.Dir(toolchain.nodePath),
				Installed:     true,
				Removable:     true,
			}, nil
		}
	}

	stagingRoot := filepath.Join(storeRoot, ".staging")
	if err := os.MkdirAll(stagingRoot, 0o700); err != nil {
		return dshmanager.RuntimeInfo{}, i.failPreparation(version, runtimeFailure(lifecycle.ErrorRuntimeInstallFailed, "The dsh-work runtime staging directory could not be prepared.", true), observer)
	}
	emitPreparation(observer, lifecycle.RuntimePreparation{
		State:         lifecycle.RuntimePreparationAcquiringDSH,
		Operation:     lifecycle.RuntimePreparationOperationInstallDSH,
		TargetVersion: version,
		Toolchain:     string(toolchain.kind),
		Source:        lifecycle.RuntimePreparationSourceOfficial,
		CanCancel:     true,
	})

	stage, source, err := i.installPackage(ctx, stagingRoot, version, toolchain, observer)
	if err != nil {
		if cancellation := preparationContextError(ctx); cancellation != nil {
			return dshmanager.RuntimeInfo{}, i.failPreparation(version, cancellation, observer)
		}
		return dshmanager.RuntimeInfo{}, i.failPreparation(version, packageInstallFailure(err), observer)
	}
	defer func() {
		if cleanupErr := os.RemoveAll(stage); cleanupErr != nil {
			err = runtimeCleanupFailure(err)
			runtime = dshmanager.RuntimeInfo{}
		}
	}()
	executable := runtimeExecutablePath(stage)
	verifiedVersion, err := i.verifyDSH(ctx, executable, version, toolchain, observer)
	if err != nil {
		return dshmanager.RuntimeInfo{}, i.failPreparation(version, err, observer)
	}
	if verifiedVersion != version {
		return dshmanager.RuntimeInfo{}, i.failPreparation(version, runtimeFailure(lifecycle.ErrorDSHUnsupportedVersion, "The installed DSH runtime does not match the requested version.", false), observer)
	}
	if cancellation := preparationContextError(ctx); cancellation != nil {
		return dshmanager.RuntimeInfo{}, i.failPreparation(version, cancellation, observer)
	}

	backup, err := commitManagedRuntime(stage, destination, storeRoot)
	if err != nil {
		return dshmanager.RuntimeInfo{}, i.failPreparation(version, runtimeFailure(lifecycle.ErrorRuntimeInstallFailed, "The verified DSH runtime could not be committed.", true), observer)
	}
	if backup != "" {
		i.rememberPendingBackup(destination, backup)
	}
	installedRuntime := dshmanager.RuntimeInfo{
		ID:            "dsh-" + version,
		Version:       version,
		Path:          filepath.Join(destination, "node_modules", ".bin", filepath.Base(executable)),
		Source:        dshmanager.RuntimeSourceManaged,
		Toolchain:     toolchain.kind,
		InstallSource: artifactSource(source),
		ToolchainPath: filepath.Dir(toolchain.nodePath),
		Installed:     true,
		Removable:     true,
	}
	if cancellation := preparationContextError(ctx); cancellation != nil {
		if cleanupErr := i.Discard(ctx, installedRuntime); cleanupErr != nil {
			return dshmanager.RuntimeInfo{}, i.failPreparation(version, runtimeCleanupFailure(cancellation), observer)
		}
		return dshmanager.RuntimeInfo{}, i.failPreparation(version, cancellation, observer)
	}
	emitPreparation(observer, lifecycle.RuntimePreparation{
		State:         lifecycle.RuntimePreparationInstalled,
		Operation:     lifecycle.RuntimePreparationOperationNone,
		TargetVersion: version,
		Toolchain:     string(toolchain.kind),
		Source:        source,
		CanCancel:     false,
	})
	return installedRuntime, nil
}

func (i *RuntimeInstaller) installPackage(ctx context.Context, stagingRoot, version string, toolchain runtimeToolchain, observer dshmanager.RuntimeInstallObserver) (string, lifecycle.RuntimePreparationSource, error) {
	if strings.TrimSpace(i.officialRegistry) == "" {
		return "", lifecycle.RuntimePreparationSourceNone, runtimeFailure(lifecycle.ErrorRuntimeInstallFailed, "The official DSH package source is unavailable.", false)
	}
	candidates := []acquisition.SourceCandidate{{Route: acquisition.RouteOfficial, Location: i.officialRegistry}}
	if strings.TrimSpace(i.mirrorRegistry) != "" {
		candidates = append(candidates, acquisition.SourceCandidate{Route: acquisition.RouteMirror, Location: i.mirrorRegistry})
	}
	request := acquisition.Request{
		OperationID: "install-dsh-" + safeFilePart(version),
		Identity: acquisition.ArtifactIdentity{
			Kind:    acquisition.ArtifactDSH,
			Name:    "@deepseek-ai/dsh",
			Version: version,
		},
		Candidates:  candidates,
		StagingRoot: stagingRoot,
		StagePrefix: "dsh-" + safeFilePart(version) + "-",
	}
	result, err := acquisition.Acquire(ctx, request, packageManagerAcquisitionAdapter{
		executor: i.executor, toolchain: toolchain, version: version,
	}, func(progress acquisition.Progress) {
		emitPreparation(observer, lifecycle.RuntimePreparation{
			State:         lifecycle.RuntimePreparationAcquiringDSH,
			Operation:     lifecycle.RuntimePreparationOperationInstallDSH,
			TargetVersion: version,
			Toolchain:     string(toolchain.kind),
			Source:        preparationSource(progress.Route),
			CanCancel:     true,
		})
	})
	if err != nil {
		return "", lastAttemptSource(result), err
	}
	return result.StagingPath, preparationSource(result.Route), nil
}

func packageInstallArgs(kind dshmanager.RuntimeToolchain, stage, version, registry string) []string {
	packageSpec := "@deepseek-ai/dsh@" + version
	if kind == dshmanager.RuntimeToolchainSystemPNPM {
		args := []string{
			"add", "--dir", stage, "--config.lockfile=false", "--config.node-linker=hoisted",
		}
		for _, packageName := range pnpmAllowedBuildPackages {
			args = append(args, "--allow-build="+packageName)
		}
		return append(args, "--registry", registry, packageSpec)
	}
	return []string{
		"install", "--prefix", stage, "--no-save", "--no-audit", "--fund=false",
		"--registry", registry, packageSpec,
	}
}

// pnpm 10+ requires an explicit allow-list for dependency lifecycle scripts.
// These are the native/runtime dependencies published in the DSH package tree;
// allowing only these names keeps the install scoped without disabling pnpm's
// build-script guard globally.
var pnpmAllowedBuildPackages = []string{
	"@deepseek-ai/dsh-subprocess-local",
	"@google/genai",
	"koffi",
	"node-pty",
	"protobufjs",
}

func (i *RuntimeInstaller) verifyDSH(ctx context.Context, executable, expected string, toolchain runtimeToolchain, observer dshmanager.RuntimeInstallObserver) (string, error) {
	emitPreparation(observer, lifecycle.RuntimePreparation{
		State:         lifecycle.RuntimePreparationVerifying,
		Operation:     lifecycle.RuntimePreparationOperationVerify,
		TargetVersion: expected,
		Toolchain:     string(toolchain.kind),
		Source:        lifecycle.RuntimePreparationSourceNone,
		CanCancel:     true,
	})
	if !runtimeExecutablePresent(executable) {
		return "", runtimeFailure(lifecycle.ErrorRuntimeInstallFailed, "The installed DSH runtime did not expose a Windows launcher.", false)
	}
	result, err := i.executor.Run(ctx, executable, []string{"--version"}, toolchain.env, filepath.Dir(filepath.Dir(filepath.Dir(executable))))
	if err != nil {
		if cancellation := preparationContextError(ctx); cancellation != nil {
			return "", cancellation
		}
		return "", runtimeFailure(lifecycle.ErrorDSHVersionCheckFailed, "The installed DSH runtime could not report its version.", false)
	}
	version := dshadapter.ParseVersion(result.Stdout + "\n" + result.Stderr)
	if version == "" {
		return "", runtimeFailure(lifecycle.ErrorDSHVersionCheckFailed, "The installed DSH runtime returned an invalid version.", false)
	}
	return version, nil
}

func (i *RuntimeInstaller) failPreparation(version string, err error, observer dshmanager.RuntimeInstallObserver) error {
	if err == nil {
		err = runtimeFailure(lifecycle.ErrorRuntimeInstallFailed, "DSH runtime preparation failed.", true)
	}
	failure := failureProjection(err)
	emitPreparation(observer, lifecycle.RuntimePreparation{
		State:         preparationStateForFailure(failure.Code),
		Operation:     lifecycle.RuntimePreparationOperationNone,
		TargetVersion: version,
		Source:        lifecycle.RuntimePreparationSourceNone,
		CanCancel:     false,
		Error:         &failure,
	})
	return err
}

func emitPreparation(observer dshmanager.RuntimeInstallObserver, preparation lifecycle.RuntimePreparation) {
	if observer != nil {
		observer(preparation)
	}
}

func preparationContextError(ctx context.Context) error {
	if ctx == nil || ctx.Err() == nil {
		return nil
	}
	return lifecycle.Failure{
		Code:      lifecycle.ErrorCancelled,
		Summary:   "Runtime preparation was cancelled.",
		Retryable: true,
		Detail:    "The runtime was not added to the catalog.",
	}
}

func runtimeFailure(code lifecycle.ErrorCode, summary string, retryable bool) error {
	return lifecycle.Failure{
		Code:           code,
		Summary:        summary,
		Retryable:      retryable,
		EffectOccurred: code == lifecycle.ErrorRuntimeInstallFailed,
		CorrelationID:  lifecycle.NewCorrelationID(),
	}
}

func runtimeCleanupFailure(original error) error {
	const detail = "A temporary runtime artifact could not be cleaned up; retry the operation after closing dsh-work."
	var failureValue lifecycle.Failure
	if errors.As(original, &failureValue) {
		failureValue.Detail = detail
		failureValue.Retryable = true
		failureValue.EffectOccurred = true
		if failureValue.CorrelationID == "" {
			failureValue.CorrelationID = lifecycle.NewCorrelationID()
		}
		return failureValue
	}
	return lifecycle.Failure{
		Code:           lifecycle.ErrorRuntimeInstallFailed,
		Summary:        "DSH runtime cleanup did not complete.",
		Detail:         detail,
		Retryable:      true,
		EffectOccurred: true,
		CorrelationID:  lifecycle.NewCorrelationID(),
	}
}

func failureProjection(err error) lifecycle.Failure {
	var failure lifecycle.Failure
	if errors.As(err, &failure) {
		if failure.CorrelationID == "" {
			failure.CorrelationID = lifecycle.NewCorrelationID()
		}
		return failure
	}
	var pointer *lifecycle.Failure
	if errors.As(err, &pointer) && pointer != nil {
		failure = *pointer
		if failure.CorrelationID == "" {
			failure.CorrelationID = lifecycle.NewCorrelationID()
		}
		return failure
	}
	return lifecycle.Failure{
		Code:          lifecycle.ErrorRuntimeInstallFailed,
		Summary:       "DSH runtime preparation failed.",
		Retryable:     true,
		CorrelationID: lifecycle.NewCorrelationID(),
	}
}

func preparationStateForFailure(code lifecycle.ErrorCode) lifecycle.RuntimePreparationState {
	if code == lifecycle.ErrorCancelled {
		return lifecycle.RuntimePreparationCancelled
	}
	return lifecycle.RuntimePreparationFailed
}

func managedStoreRoot(root string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", errors.New("empty runtime store")
	}
	absolute, err := filepath.Abs(root)
	if err != nil || absolute == "" {
		return "", errors.New("invalid runtime store")
	}
	return filepath.Clean(absolute), nil
}

func isDirectManagedChild(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == "." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return false
	}
	return !strings.ContainsAny(rel, `/\\`)
}

func commitManagedRuntime(stage, destination, storeRoot string) (string, error) {
	if !isDirectManagedChild(storeRoot, destination) {
		return "", errors.New("runtime destination is outside the managed store")
	}
	if _, err := os.Stat(destination); err == nil {
		backup, err := os.MkdirTemp(storeRoot, ".dsh-backup-")
		if err != nil {
			return "", err
		}
		if removeErr := os.RemoveAll(backup); removeErr != nil {
			return "", removeErr
		}
		if err := os.Rename(destination, backup); err != nil {
			return "", err
		}
		if err := os.Rename(stage, destination); err != nil {
			restoreErr := os.Rename(backup, destination)
			if restoreErr != nil {
				return "", errors.Join(err, fmt.Errorf("restore previous runtime after commit failure: %w", restoreErr))
			}
			return "", err
		}
		return backup, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := os.Rename(stage, destination); err != nil {
		return "", err
	}
	return "", nil
}

func runtimeExecutablePath(root string) string {
	for _, name := range []string{"dsh.cmd", "dsh.exe", "dsh"} {
		candidate := filepath.Join(root, "node_modules", ".bin", name)
		if runtimeExecutablePresent(candidate) {
			return candidate
		}
	}
	return filepath.Join(root, "node_modules", ".bin", "dsh.cmd")
}

func runtimeExecutablePresent(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func childPathEnvironment(nodeDirectory string) map[string]string {
	pathValue := os.Getenv("PATH")
	if pathValue == "" {
		return map[string]string{"PATH": nodeDirectory}
	}
	return map[string]string{"PATH": nodeDirectory + string(os.PathListSeparator) + pathValue}
}

func safeFilePart(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "runtime"
	}
	var builder strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '-' || r == '_' {
			builder.WriteRune(r)
		} else {
			builder.WriteByte('_')
		}
	}
	return builder.String()
}

func extractNodeArchive(archivePath, destination string) (err error) {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := reader.Close(); closeErr != nil {
			err = runtimeCleanupFailure(err)
		}
	}()
	if len(reader.File) == 0 {
		return errors.New("empty Node.js archive")
	}
	rootName := ""
	for _, file := range reader.File {
		name, err := safeArchiveName(file.Name)
		if err != nil {
			return err
		}
		parts := strings.Split(filepath.ToSlash(name), "/")
		isDirectory := file.FileInfo().IsDir() || strings.HasSuffix(file.Name, "/") || strings.HasSuffix(file.Name, "\\")
		if len(parts) == 1 && isDirectory {
			if rootName == "" {
				rootName = parts[0]
			} else if rootName != parts[0] {
				return errors.New("Node.js archive contains multiple top-level directories")
			}
			continue
		}
		if len(parts) < 2 || parts[0] == "" {
			return errors.New("Node.js archive has no single top-level directory")
		}
		if rootName == "" {
			rootName = parts[0]
		} else if rootName != parts[0] {
			return errors.New("Node.js archive contains multiple top-level directories")
		}
	}

	parent := filepath.Dir(destination)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	extractRoot, err := os.MkdirTemp(parent, ".node-extract-")
	if err != nil {
		return err
	}
	defer func() {
		if cleanupErr := os.RemoveAll(extractRoot); cleanupErr != nil {
			err = runtimeCleanupFailure(err)
		}
	}()
	for _, file := range reader.File {
		name, _ := safeArchiveName(file.Name)
		target := filepath.Join(extractRoot, filepath.FromSlash(name))
		if !isWithinDirectory(extractRoot, target) {
			return errors.New("Node.js archive entry escapes extraction directory")
		}
		if file.FileInfo().Mode()&os.ModeSymlink != 0 {
			return errors.New("Node.js archive contains a symbolic link")
		}
		if file.FileInfo().IsDir() || strings.HasSuffix(file.Name, "/") {
			if err := os.MkdirAll(target, 0o700); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		input, err := file.Open()
		if err != nil {
			return err
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o700)
		if err != nil {
			input.Close()
			return err
		}
		_, copyErr := io.Copy(output, input)
		closeErr := errors.Join(output.Close(), input.Close())
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}

	extracted := filepath.Join(extractRoot, rootName)
	if !runtimeExecutablePresent(filepath.Join(extracted, "node.exe")) || !runtimeExecutablePresent(filepath.Join(extracted, "npm.cmd")) || !runtimeExecutablePresent(filepath.Join(extracted, "node_modules", "npm", "bin", "npm-cli.js")) {
		return errors.New("Node.js archive does not contain the expected Node/npm files")
	}
	if err := os.RemoveAll(destination); err != nil {
		return err
	}
	return os.Rename(extracted, destination)
}

func safeArchiveName(name string) (string, error) {
	if name == "" || strings.ContainsRune(name, '\x00') || strings.HasPrefix(name, "/") || strings.HasPrefix(name, "\\") || filepath.VolumeName(filepath.FromSlash(name)) != "" {
		return "", errors.New("Node.js archive contains an absolute path")
	}
	clean := filepath.Clean(filepath.FromSlash(name))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("Node.js archive contains a path traversal")
	}
	return clean, nil
}

func isWithinDirectory(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func packageInstallFailure(err error) error {
	failure := acquisitionFailure(err)
	retryable := failure.Kind == acquisition.FailureReachability
	summary := "The DSH package could not be installed."
	if retryable {
		summary = "The DSH package source could not be reached."
	}
	return lifecycle.Failure{
		Code:           lifecycle.ErrorRuntimeInstallFailed,
		Summary:        summary,
		Retryable:      retryable,
		EffectOccurred: true,
		CorrelationID:  lifecycle.NewCorrelationID(),
	}
}

func artifactAcquisitionFailure(err error) error {
	failure := acquisitionFailure(err)
	retryable := failure.Kind == acquisition.FailureReachability || failure.Kind == acquisition.FailureLocalIO
	summary := "The Node.js artifact could not be acquired."
	if failure.Kind == acquisition.FailureIntegrity {
		summary = "The Node.js artifact failed its integrity check."
	}
	return lifecycle.Failure{
		Code:          lifecycle.ErrorRuntimeInstallFailed,
		Summary:       summary,
		Retryable:     retryable,
		CorrelationID: lifecycle.NewCorrelationID(),
	}
}

func preparationSource(route acquisition.Route) lifecycle.RuntimePreparationSource {
	switch route {
	case acquisition.RouteOfficial:
		return lifecycle.RuntimePreparationSourceOfficial
	case acquisition.RouteMirror:
		return lifecycle.RuntimePreparationSourceMirror
	case acquisition.RouteLocal:
		return lifecycle.RuntimePreparationSourceLocal
	default:
		return lifecycle.RuntimePreparationSourceNone
	}
}

func lastAttemptSource(result acquisition.Result) lifecycle.RuntimePreparationSource {
	if len(result.Attempts) == 0 {
		return lifecycle.RuntimePreparationSourceNone
	}
	return preparationSource(result.Attempts[len(result.Attempts)-1].Route)
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

type artifactDownloader interface {
	Download(context.Context, string, string, string, func(int64, int64, bool)) error
}

type httpArtifactDownloader struct {
	client *http.Client
}

func (d httpArtifactDownloader) Download(ctx context.Context, source, destination, expectedSHA256 string, report func(int64, int64, bool)) error {
	client := d.client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Minute}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("artifact download returned status %d", response.StatusCode)
	}
	if response.ContentLength > defaultNodeDownloadLimit {
		return errors.New("artifact exceeds the download limit")
	}
	part := destination + ".part"
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(part, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	hash := sha256.New()
	reader := io.Reader(io.LimitReader(response.Body, defaultNodeDownloadLimit+1))
	writer := io.MultiWriter(file, hash)
	received, copyErr := io.Copy(writer, &progressReader{reader: reader, report: report, total: response.ContentLength, hasTotal: response.ContentLength >= 0})
	closeErr := file.Close()
	if copyErr != nil {
		_ = os.Remove(part)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(part)
		return closeErr
	}
	if received > defaultNodeDownloadLimit {
		_ = os.Remove(part)
		return errors.New("artifact exceeds the download limit")
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if !strings.EqualFold(strings.TrimSpace(expectedSHA256), actual) {
		_ = os.Remove(part)
		return fmt.Errorf("%w: expected %s", errArtifactIntegrity, strings.TrimSpace(expectedSHA256))
	}
	if err := os.Rename(part, destination); err != nil {
		_ = os.Remove(part)
		return err
	}
	return nil
}

type progressReader struct {
	reader   io.Reader
	report   func(int64, int64, bool)
	received int64
	total    int64
	hasTotal bool
}

func (r *progressReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.received += int64(n)
	if r.report != nil {
		r.report(r.received, r.total, r.hasTotal)
	}
	return n, err
}

var runtimeVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?$`)
