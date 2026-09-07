//go:build windows

package windows

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/local/dsh-work/internal/acquisition"
	"github.com/local/dsh-work/internal/dshadapter"
	"github.com/local/dsh-work/internal/dshmanager"
	"github.com/local/dsh-work/internal/lifecycle"
)

const (
	defaultOfficialNodeBase = "https://nodejs.org/dist/"
	defaultMirrorNodeBase   = "https://npmmirror.com/mirrors/node/"
	nodeManifestName        = ".dsh-work-node.json"
)

type ManagedNodeInstallerOption func(*ManagedNodeInstaller)

func WithManagedNodeDownloader(downloader artifactDownloader) ManagedNodeInstallerOption {
	return func(installer *ManagedNodeInstaller) { installer.downloader = downloader }
}

func WithManagedNodeSources(official, mirror string) ManagedNodeInstallerOption {
	return func(installer *ManagedNodeInstaller) {
		if strings.TrimSpace(official) != "" {
			installer.officialBase = normalizedBaseURL(official)
		}
		installer.mirrorBase = normalizedBaseURL(mirror)
	}
}

// ManagedNodeInstaller is the Windows adapter for one exact, immutable Node
// installation. Download never changes the selected Run context.
type ManagedNodeInstaller struct {
	executor     dshadapter.CommandExecutor
	storeRoot    string
	downloader   artifactDownloader
	officialBase string
	mirrorBase   string
}

func NewManagedNodeInstaller(executor dshadapter.CommandExecutor, storeRoot string, options ...ManagedNodeInstallerOption) *ManagedNodeInstaller {
	installer := &ManagedNodeInstaller{
		executor: executor, storeRoot: storeRoot,
		downloader:   httpArtifactDownloader{client: &http.Client{Timeout: 15 * time.Minute}},
		officialBase: defaultOfficialNodeBase, mirrorBase: defaultMirrorNodeBase,
	}
	for _, option := range options {
		if option != nil {
			option(installer)
		}
	}
	return installer
}

func (i *ManagedNodeInstaller) Install(ctx context.Context, release dshmanager.NodeReleaseInfo, observer dshmanager.RuntimeInstallObserver) (dshmanager.NodeInstallationInfo, error) {
	if i == nil || i.executor == nil || i.downloader == nil {
		return dshmanager.NodeInstallationInfo{}, runtimeFailure(lifecycle.ErrorRuntimeInstallUnavailable, "The Windows Node installer is unavailable.", false)
	}
	if release.Platform != "windows" || normalizedNodeArchitecture(release.Architecture) != normalizedNodeArchitecture(runtime.GOARCH) {
		return dshmanager.NodeInstallationInfo{}, runtimeFailure(lifecycle.ErrorRuntimeInstallUnavailable, "The Node release does not match this platform.", false)
	}
	if !exactNodeVersion(release.Version) || release.Filename == "" || release.SHA256 == "" {
		return dshmanager.NodeInstallationInfo{}, runtimeFailure(lifecycle.ErrorRuntimeInstallFailed, "The Node release metadata is invalid.", false)
	}
	official := i.officialBase + release.Version + "/" + release.Filename
	candidates := []acquisition.SourceCandidate{{Route: acquisition.RouteOfficial, Location: official}}
	if i.mirrorBase != "" {
		candidates = append(candidates, acquisition.SourceCandidate{Route: acquisition.RouteMirror, Location: i.mirrorBase + release.Version + "/" + release.Filename})
	}
	return i.install(ctx, release, candidates, observer)
}

func (i *ManagedNodeInstaller) install(ctx context.Context, release dshmanager.NodeReleaseInfo, candidates []acquisition.SourceCandidate, observer dshmanager.RuntimeInstallObserver) (installation dshmanager.NodeInstallationInfo, err error) {
	storeRoot, err := managedStoreRoot(i.storeRoot)
	if err != nil {
		return dshmanager.NodeInstallationInfo{}, runtimeFailure(lifecycle.ErrorRuntimeInstallFailed, "The managed Node store is unavailable.", false)
	}
	arch := normalizedNodeArchitecture(release.Architecture)
	destination := filepath.Join(storeRoot, "toolchains", "node", release.Version, "windows-"+arch)
	if existing, ok := i.existingInstallation(ctx, destination, release); ok {
		return existing, nil
	}
	stagingRoot := filepath.Join(storeRoot, ".staging")
	if err := os.MkdirAll(stagingRoot, 0o700); err != nil {
		return dshmanager.NodeInstallationInfo{}, runtimeFailure(lifecycle.ErrorRuntimeInstallFailed, "The managed Node staging directory could not be prepared.", true)
	}
	request := acquisition.Request{
		OperationID: "install-node-" + safeFilePart(release.Version),
		Identity: acquisition.ArtifactIdentity{
			Kind: acquisition.ArtifactNode, Name: "node", Version: release.Version,
			Platform: release.Platform, Architecture: arch, Filename: release.Filename,
		},
		Candidates: candidates, StagingRoot: stagingRoot,
		StagePrefix: "node-" + safeFilePart(release.Version) + "-",
	}
	result, err := acquisition.Acquire(ctx, request, nodeArchiveAcquisitionAdapter{
		downloader: i.downloader, sha256: release.SHA256,
		report: func(route acquisition.Route, received, total int64, hasTotal bool) {
			emitPreparation(observer, lifecycle.RuntimePreparation{
				State: lifecycle.RuntimePreparationAcquiringNode, Operation: lifecycle.RuntimePreparationOperationDownloadNode,
				TargetVersion: release.Version, Toolchain: string(dshmanager.RuntimeToolchainManagedNodeNPM),
				Source: preparationSource(route), ReceivedBytes: received, TotalBytes: total, HasTotal: hasTotal, CanCancel: true,
			})
		},
	}, nil)
	if err != nil {
		return dshmanager.NodeInstallationInfo{}, artifactAcquisitionFailure(err)
	}
	defer func() {
		if cleanupErr := os.RemoveAll(result.StagingPath); cleanupErr != nil && err == nil {
			installation = dshmanager.NodeInstallationInfo{}
			err = runtimeCleanupFailure(cleanupErr)
		}
	}()

	extracted := filepath.Join(result.StagingPath, "extracted")
	if err := extractNodeArchive(result.PayloadPath, extracted); err != nil {
		return dshmanager.NodeInstallationInfo{}, runtimeFailure(lifecycle.ErrorRuntimeInstallFailed, "The managed Node archive could not be prepared.", false)
	}
	nodePath := filepath.Join(extracted, "node.exe")
	npmPath := filepath.Join(extracted, "npm.cmd")
	if err := verifyNodeExecutables(ctx, i.executor, nodePath, npmPath, release.Version); err != nil {
		return dshmanager.NodeInstallationInfo{}, err
	}
	manifest := nodeInstallationManifest{
		Version: release.Version, Platform: release.Platform, Architecture: arch,
		SHA256: strings.ToLower(release.SHA256), Route: result.Route,
	}
	manifestData, err := json.Marshal(manifest)
	if err != nil {
		return dshmanager.NodeInstallationInfo{}, err
	}
	if err := os.WriteFile(filepath.Join(extracted, nodeManifestName), manifestData, 0o600); err != nil {
		return dshmanager.NodeInstallationInfo{}, runtimeFailure(lifecycle.ErrorRuntimeInstallFailed, "The managed Node installation could not be recorded.", true)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return dshmanager.NodeInstallationInfo{}, runtimeFailure(lifecycle.ErrorRuntimeInstallFailed, "The managed Node directory could not be prepared.", true)
	}
	if _, statErr := os.Stat(destination); statErr == nil {
		if existing, ok := i.existingInstallation(ctx, destination, release); ok {
			return existing, nil
		}
		return dshmanager.NodeInstallationInfo{}, runtimeFailure(lifecycle.ErrorRuntimeInstallFailed, "A different managed Node installation already uses this identity.", false)
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return dshmanager.NodeInstallationInfo{}, runtimeFailure(lifecycle.ErrorRuntimeInstallFailed, "The managed Node destination is unavailable.", true)
	}
	if err := os.Rename(extracted, destination); err != nil {
		return dshmanager.NodeInstallationInfo{}, runtimeFailure(lifecycle.ErrorRuntimeInstallFailed, "The managed Node installation could not be committed.", true)
	}
	return nodeInstallationFromManifest(destination, manifest), nil
}

func (i *ManagedNodeInstaller) existingInstallation(ctx context.Context, destination string, release dshmanager.NodeReleaseInfo) (dshmanager.NodeInstallationInfo, bool) {
	data, err := os.ReadFile(filepath.Join(destination, nodeManifestName))
	if err != nil {
		return dshmanager.NodeInstallationInfo{}, false
	}
	var manifest nodeInstallationManifest
	if json.Unmarshal(data, &manifest) != nil || manifest.Version != release.Version || manifest.Platform != release.Platform || manifest.Architecture != normalizedNodeArchitecture(release.Architecture) || !strings.EqualFold(manifest.SHA256, release.SHA256) {
		return dshmanager.NodeInstallationInfo{}, false
	}
	installation := nodeInstallationFromManifest(destination, manifest)
	if verifyNodeExecutables(ctx, i.executor, installation.NodePath, installation.NPMPath, release.Version) != nil {
		return dshmanager.NodeInstallationInfo{}, false
	}
	return installation, true
}

func (i *ManagedNodeInstaller) Remove(_ context.Context, installation dshmanager.NodeInstallationInfo) error {
	if i == nil || installation.Ownership != dshmanager.NodeOwnershipManaged {
		return runtimeFailure(lifecycle.ErrorRuntimeInstallFailed, "The Node installation cannot be removed.", false)
	}
	if err := validateManagedNodeInstallation(i.storeRoot, installation); err != nil {
		return runtimeFailure(lifecycle.ErrorRuntimeInstallFailed, "The Node installation could not be verified for removal.", false)
	}
	storeRoot, err := managedStoreRoot(i.storeRoot)
	if err != nil {
		return err
	}
	installationRoot := filepath.Dir(installation.NodePath)
	managedRoot := filepath.Join(storeRoot, "toolchains", "node")
	if !isWithinDirectory(managedRoot, installationRoot) || installationRoot == managedRoot {
		return runtimeFailure(lifecycle.ErrorRuntimeInstallFailed, "The Node installation is outside the managed store.", false)
	}
	return os.RemoveAll(installationRoot)
}

type nodeInstallationManifest struct {
	Version      string            `json:"version"`
	Platform     string            `json:"platform"`
	Architecture string            `json:"architecture"`
	SHA256       string            `json:"sha256"`
	Route        acquisition.Route `json:"route"`
}

func nodeInstallationFromManifest(root string, manifest nodeInstallationManifest) dshmanager.NodeInstallationInfo {
	return dshmanager.NodeInstallationInfo{
		ID:      "node-" + manifest.Version + "-" + manifest.Platform + "-" + manifest.Architecture,
		Version: manifest.Version, Platform: manifest.Platform, Architecture: manifest.Architecture,
		NodePath: filepath.Join(root, "node.exe"), NPMPath: filepath.Join(root, "npm.cmd"),
		Ownership: dshmanager.NodeOwnershipManaged, InstallSource: nodeInstallSource(manifest.Route),
		SHA256: manifest.SHA256, Installed: true, Removable: true, Verified: true,
	}
}

func verifyNodeExecutables(ctx context.Context, executor dshadapter.CommandExecutor, nodePath, npmPath, expectedVersion string) error {
	nodeResult, err := executor.Run(ctx, nodePath, []string{"--version"}, childPathEnvironment(filepath.Dir(nodePath)), "")
	if err != nil || dshadapter.ParseVersion(nodeResult.Stdout+"\n"+nodeResult.Stderr) != strings.TrimPrefix(expectedVersion, "v") {
		return runtimeFailure(lifecycle.ErrorRuntimeInstallFailed, "The managed Node executable failed its exact version check.", false)
	}
	npmResult, err := executor.Run(ctx, npmPath, []string{"--version"}, childPathEnvironment(filepath.Dir(nodePath)), "")
	if err != nil || strings.TrimSpace(npmResult.Stdout+"\n"+npmResult.Stderr) == "" {
		return runtimeFailure(lifecycle.ErrorRuntimeInstallFailed, "The managed npm executable failed its version check.", false)
	}
	return nil
}

func normalizedBaseURL(value string) string {
	value = strings.TrimRight(strings.TrimSpace(value), "/")
	if value == "" {
		return ""
	}
	return value + "/"
}

func normalizedNodeArchitecture(value string) string {
	if value == "x64" || value == "amd64" {
		return "x64"
	}
	return value
}

func exactNodeVersion(value string) bool {
	value = strings.TrimPrefix(value, "v")
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if part == "" || strings.Trim(part, "0123456789") != "" {
			return false
		}
	}
	return true
}

func nodeInstallSource(route acquisition.Route) dshmanager.RuntimeArtifactSource {
	if route == acquisition.RouteMirror {
		return dshmanager.RuntimeArtifactSourceMirror
	}
	return dshmanager.RuntimeArtifactSourceOfficial
}
