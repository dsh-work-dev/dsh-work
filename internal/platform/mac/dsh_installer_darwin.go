//go:build darwin && cgo

package mac

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/local/dsh-work/internal/acquisition"
	"github.com/local/dsh-work/internal/dshadapter"
	"github.com/local/dsh-work/internal/dshmanager"
	"github.com/local/dsh-work/internal/lifecycle"
	"github.com/local/dsh-work/internal/platform/automationpackages"
	"golang.org/x/sys/unix"
)

const macDSHManifestFile = ".dsh-work-runtime.json"

var macDSHVersion = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?$`)
var macDSHRevision = regexp.MustCompile(`^generation-[A-Z2-7]{26}$`)

type macDSHManifest struct {
	Version     string `json:"version"`
	EntrySHA256 string `json:"entrySha256"`
}

type macDSHInstaller struct {
	runtime *Runtime
	root    string
}

func (r *Runtime) DSHInstaller(storeRoot string) dshmanager.RuntimeInstaller {
	r.mu.Lock()
	r.dshStore = storeRoot
	r.mu.Unlock()
	return macDSHInstaller{runtime: r, root: storeRoot}
}

func macDSHEntry(directory string) string {
	return filepath.Join(directory, "node_modules", "@deepseek-ai", "dsh", "lib", "bin.js")
}

func macDSHPackage(directory, version string) (string, error) {
	entry := macDSHEntry(directory)
	actual, err := filepath.EvalSymlinks(entry)
	if err != nil || actual != entry {
		return "", errors.New("Mac DSH package entry is unavailable")
	}
	if info, e := os.Lstat(entry); e != nil || !info.Mode().IsRegular() {
		return "", errors.New("Mac DSH package entry is invalid")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return "", err
	}
	defer root.Close()
	file, err := root.Open(filepath.Join("node_modules", "@deepseek-ai", "dsh", "package.json"))
	if err != nil {
		return "", err
	}
	defer file.Close()
	var pkg struct{ Name, Version string }
	if err = json.NewDecoder(io.LimitReader(file, 1<<20)).Decode(&pkg); err != nil || pkg.Name != "@deepseek-ai/dsh" || pkg.Version != version {
		return "", errors.New("Mac DSH package identity is invalid")
	}
	return entry, nil
}

func verifiedMacDSH(directory, version string) (string, error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return "", err
	}
	defer root.Close()
	file, err := root.Open(macDSHManifestFile)
	if err != nil {
		return "", err
	}
	defer file.Close()
	var manifest macDSHManifest
	if err = json.NewDecoder(io.LimitReader(file, 4096)).Decode(&manifest); err != nil || manifest.Version != version || !validSHA256(manifest.EntrySHA256) {
		return "", errors.New("managed Mac DSH manifest is invalid")
	}
	entry, err := macDSHPackage(directory, version)
	if err != nil {
		return "", err
	}
	digest, err := macExecutableSHA256(entry)
	if err != nil || digest != manifest.EntrySHA256 {
		return "", errors.New("managed Mac DSH entry failed integrity verification")
	}
	return entry, nil
}

func (r *Runtime) dshForExecutable(executable string) (string, error) {
	actual, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return "", err
	}
	if actual == r.cli {
		return actual, nil
	}
	r.mu.RLock()
	store := r.dshStore
	r.mu.RUnlock()
	if !filepath.IsAbs(store) {
		return "", errors.New("managed Mac DSH store is unavailable")
	}
	store, err = filepath.EvalSymlinks(store)
	if err != nil {
		return "", err
	}
	directory, version, err := managedMacDSHLocation(store, actual)
	if err != nil {
		return "", err
	}
	return verifiedMacDSH(directory, version)
}

func managedMacDSHLocation(store, actual string) (string, string, error) {
	relative, err := filepath.Rel(store, actual)
	if err != nil || !filepath.IsLocal(relative) {
		return "", "", errors.New("Mac DSH executable is outside the runtime store")
	}
	parts := strings.Split(relative, string(os.PathSeparator))
	name := parts[0]
	version := strings.TrimPrefix(name, "dsh-")
	directory := filepath.Join(store, name)
	if len(parts) > 3 && parts[1] == "revisions" && macDSHRevision.MatchString(parts[2]) {
		directory = filepath.Join(directory, parts[1], parts[2])
	}
	if !strings.HasPrefix(name, "dsh-") || !macDSHVersion.MatchString(version) || macDSHEntry(directory) != actual {
		return "", "", errors.New("managed Mac DSH executable is invalid")
	}
	return directory, version, nil
}

func (i macDSHInstaller) Install(ctx context.Context, version string) (dshmanager.RuntimeInfo, error) {
	return i.InstallWithNodes(ctx, version, nil, nil)
}

func (i macDSHInstaller) InstallWithProgress(ctx context.Context, version string, observer dshmanager.RuntimeInstallObserver) (dshmanager.RuntimeInfo, error) {
	return i.InstallWithNodes(ctx, version, nil, observer)
}

func (i macDSHInstaller) npmToolchain(ctx context.Context, nodes []dshmanager.NodeInstallationInfo) (string, string, dshmanager.RuntimeToolchain, error) {
	if i.runtime.node != "" && i.runtime.npm != "" {
		system, err := i.runtime.NodeResolver(i.root).Resolve(ctx, dshmanager.NodeSelection{Kind: dshmanager.NodeSelectionSystem}, nil)
		if err == nil && system.Version != "" {
			return system.NodePath, system.NPMPath, dshmanager.RuntimeToolchainSystemNPM, nil
		}
	}
	for _, node := range nodes {
		selection := dshmanager.NodeSelection{Kind: dshmanager.NodeSelectionManaged, InstallationID: node.ID}
		resolved, err := i.runtime.NodeResolver(i.root).Resolve(ctx, selection, nodes)
		if err == nil {
			return resolved.NodePath, resolved.NPMPath, dshmanager.RuntimeToolchainManagedNodeNPM, nil
		}
	}
	return "", "", dshmanager.RuntimeToolchainNone, exec.ErrNotFound
}

func (i macDSHInstaller) RequiresNodeAcquisition(ctx context.Context, nodes []dshmanager.NodeInstallationInfo) (bool, error) {
	_, _, _, err := i.npmToolchain(ctx, nodes)
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	if errors.Is(err, exec.ErrNotFound) {
		return true, nil
	}
	return false, err
}

func (i macDSHInstaller) InstallWithNodes(ctx context.Context, version string, nodes []dshmanager.NodeInstallationInfo, observer dshmanager.RuntimeInstallObserver) (dshmanager.RuntimeInfo, error) {
	node, npm, toolchain, err := i.npmToolchain(ctx, nodes)
	if err != nil {
		return dshmanager.RuntimeInfo{}, err
	}
	return i.install(ctx, version, node, npm, toolchain, false, observer)
}

func (i macDSHInstaller) ForceInstall(ctx context.Context, version string, selection dshmanager.ResolvedNode) (dshmanager.RuntimeInfo, error) {
	node, err := i.runtime.nodeForEnvironment(selection.ChildEnvironment)
	if err != nil || node != selection.NodePath || selection.NPMPath == "" {
		return dshmanager.RuntimeInfo{}, errors.New("the selected Mac Node toolchain is unavailable for DSH restore")
	}
	toolchain := dshmanager.RuntimeToolchainSystemNPM
	if selection.Selection.Kind == dshmanager.NodeSelectionManaged {
		toolchain = dshmanager.RuntimeToolchainManagedNodeNPM
	}
	return i.install(ctx, version, node, selection.NPMPath, toolchain, true, nil)
}

func (i macDSHInstaller) install(ctx context.Context, version, node, npm string, toolchain dshmanager.RuntimeToolchain, force bool, observer dshmanager.RuntimeInstallObserver) (dshmanager.RuntimeInfo, error) {
	if !macDSHVersion.MatchString(version) || !filepath.IsAbs(i.root) {
		return dshmanager.RuntimeInfo{}, errors.New("invalid Mac DSH install identity")
	}
	if err := os.MkdirAll(i.root, 0700); err != nil {
		return dshmanager.RuntimeInfo{}, err
	}
	root, err := filepath.EvalSymlinks(i.root)
	if err != nil {
		return dshmanager.RuntimeInfo{}, err
	}
	id := "dsh-" + version
	destination := filepath.Join(root, id)
	if force {
		// The runtime catalog stores the selected path. Write a distinct revision
		// so a canceled restore or a failed catalog save keeps the old tree intact.
		revisions, e := macStoreDirectory(root, filepath.Join(id, "revisions"), true)
		if e != nil {
			return dshmanager.RuntimeInfo{}, e
		}
		destination = filepath.Join(revisions, "generation-"+rand.Text())
	}
	info := dshmanager.RuntimeInfo{ID: id, Version: version, Path: macDSHEntry(destination), Source: dshmanager.RuntimeSourceManaged,
		Toolchain: toolchain, ToolchainPath: filepath.Dir(node), Installed: true, Removable: true}
	if _, err = verifiedMacDSH(destination, version); err == nil {
		info.InstallSource = dshmanager.RuntimeArtifactSourceLocal
		return info, nil
	}
	if _, err = os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
		return dshmanager.RuntimeInfo{}, errors.New("managed Mac DSH destination already exists or cannot be inspected")
	}
	staging, err := macStoreDirectory(root, ".staging", true)
	if err != nil {
		return dshmanager.RuntimeInfo{}, err
	}
	identity := acquisition.ArtifactIdentity{Kind: acquisition.ArtifactDSH, Name: "@deepseek-ai/dsh", Version: version}
	registries := []acquisition.SourceCandidate{{Route: acquisition.RouteOfficial, Location: "https://registry.npmjs.org/"}, {Route: acquisition.RouteMirror, Location: "https://registry.npmmirror.com/"}}
	result, err := acquisition.Acquire(ctx, acquisition.Request{OperationID: "install-" + id, Identity: identity, StagingRoot: staging, StagePrefix: "dsh-", Candidates: registries},
		macNPMInstall{npm: npm, node: node, version: version, toolchain: toolchain, observer: observer}, nil)
	if err != nil {
		return dshmanager.RuntimeInfo{}, err
	}
	defer os.RemoveAll(result.StagingPath)
	if observer != nil {
		observer(lifecycle.RuntimePreparation{State: lifecycle.RuntimePreparationVerifying, Operation: lifecycle.RuntimePreparationOperationVerify,
			TargetVersion: version, Source: lifecycle.RuntimePreparationSource(result.Route), Toolchain: string(toolchain), CanCancel: true})
	}
	if err = i.publish(ctx, node, version, result.PayloadPath, destination); err != nil {
		return dshmanager.RuntimeInfo{}, err
	}
	info.InstallSource = dshmanager.RuntimeArtifactSourceOfficial
	if result.Route == acquisition.RouteMirror {
		info.InstallSource = dshmanager.RuntimeArtifactSourceMirror
	}
	return info, nil
}

func (i macDSHInstaller) publish(ctx context.Context, node, version, stage, destination string) error {
	entry, err := macDSHPackage(stage, version)
	if err != nil {
		return err
	}
	probe, err := i.runtime.runRestricted(ctx, node, entry, entry, []string{"--version"}, macNodeEnvironment(filepath.Dir(node)), stage)
	if err != nil {
		return err
	}
	if dshadapter.ParseVersion(probe.Stdout) != version {
		return errors.New("Mac DSH package reported an unexpected version")
	}
	digest, err := macExecutableSHA256(entry)
	if err != nil {
		return err
	}
	manifest, err := json.Marshal(macDSHManifest{Version: version, EntrySHA256: digest})
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(stage, macDSHManifestFile), manifest, 0600); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return unix.RenameatxNp(unix.AT_FDCWD, stage, unix.AT_FDCWD, destination, unix.RENAME_EXCL)
}

func (i macDSHInstaller) Discard(ctx context.Context, runtime dshmanager.RuntimeInfo) error {
	if runtime.InstallSource == dshmanager.RuntimeArtifactSourceLocal {
		return nil
	}
	return i.remove(context.WithoutCancel(ctx), runtime, false)
}

func (i macDSHInstaller) Remove(ctx context.Context, runtime dshmanager.RuntimeInfo) error {
	return i.remove(ctx, runtime, true)
}

func (i macDSHInstaller) remove(ctx context.Context, runtime dshmanager.RuntimeInfo, allRevisions bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if runtime.Source != dshmanager.RuntimeSourceManaged || !macDSHVersion.MatchString(runtime.Version) || runtime.ID != "dsh-"+runtime.Version {
		return errors.New("managed Mac DSH removal identity is invalid")
	}
	root, err := filepath.EvalSymlinks(i.root)
	if err != nil {
		return err
	}
	directory, version, err := managedMacDSHLocation(root, runtime.Path)
	if err != nil || version != runtime.Version {
		return errors.New("managed Mac DSH path changed")
	}
	if _, err = os.Lstat(directory); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if _, err = verifiedMacDSH(directory, runtime.Version); err != nil {
		return err
	}
	store, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer store.Close()
	if allRevisions {
		return store.RemoveAll(runtime.ID)
	}
	relative, err := filepath.Rel(root, directory)
	if err != nil {
		return err
	}
	return store.RemoveAll(relative)
}

type macNPMInstall struct {
	npm, node, version string
	toolchain          dshmanager.RuntimeToolchain
	observer           dshmanager.RuntimeInstallObserver
}

func (a macNPMInstall) Attempt(ctx context.Context, request acquisition.AttemptRequest) (acquisition.AttemptResult, error) {
	if a.observer != nil {
		source := lifecycle.RuntimePreparationSourceOfficial
		if request.Candidate.Route == acquisition.RouteMirror {
			source = lifecycle.RuntimePreparationSourceMirror
		}
		a.observer(lifecycle.RuntimePreparation{State: lifecycle.RuntimePreparationAcquiringDSH, Operation: lifecycle.RuntimePreparationOperationInstallDSH,
			TargetVersion: a.version, Toolchain: string(a.toolchain), Source: source, CanCancel: true})
	}
	args := []string{"install", "--ignore-scripts", "--no-audit", "--no-fund", "--no-save", "--package-lock=false",
		"--prefer-offline", "--fetch-retries=1", "--fetch-timeout=60000", "--loglevel=http",
		"--registry", request.Candidate.Location, "@deepseek-ai/dsh@" + a.version}
	result, err := runMacNPM(ctx, a.npm, args, request.StagingPath, filepath.Dir(a.node))
	if err != nil {
		var failure acquisition.Failure
		if errors.As(err, &failure) {
			return acquisition.AttemptResult{}, err
		}
		return acquisition.AttemptResult{}, automationpackages.ClassifyPackageManagerFailure(result, err)
	}
	return acquisition.AttemptResult{ResolvedIdentity: request.Identity, PayloadPath: request.StagingPath}, nil
}

// runMacNPM owns installer children in a separate process group. This
// launcher is used only by explicit package installation and repair.
func runMacNPM(ctx context.Context, executable string, args []string, directory, nodeBin string) (dshadapter.CommandResult, error) {
	config, err := os.CreateTemp(directory, ".dsh-empty-npmrc-")
	if err != nil {
		return dshadapter.CommandResult{}, acquisition.Failure{Kind: acquisition.FailureLocalIO, Summary: "npm staging configuration could not be created", Cause: err}
	}
	defer os.Remove(config.Name())
	if err := config.Close(); err != nil {
		return dshadapter.CommandResult{}, acquisition.Failure{Kind: acquisition.FailureLocalIO, Summary: "npm staging configuration could not be closed", Cause: err}
	}
	args = append(append([]string{}, args...), "--userconfig="+config.Name(), "--globalconfig=/dev/null")
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Dir = directory
	cmd.Env = []string{"PATH=" + nodeBin + ":/usr/bin:/bin", "HOME=" + os.Getenv("HOME"), "TMPDIR=" + os.TempDir()}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return unix.Kill(-cmd.Process.Pid, unix.SIGKILL) }
	cmd.WaitDelay = 3 * time.Second
	var log limitedNPMOutput
	var lastFetch time.Time
	output := dshadapter.NewCommandOutputWriter(dshadapter.WithCommandOutput(ctx, func(line string) {
		if strings.HasPrefix(line, "npm http fetch ") {
			if time.Since(lastFetch) < time.Second {
				return
			}
			lastFetch = time.Now()
		} else {
			_, _ = log.Write([]byte(line + "\n"))
		}
		dshadapter.ReportCommandOutput(ctx, line)
	}))
	cmd.Stdout = output
	cmd.Stderr = output
	err = cmd.Run()
	_ = output.Close()
	if ctx.Err() != nil {
		err = errors.Join(ctx.Err(), err)
	}
	return dshadapter.CommandResult{Stderr: string(log)}, err
}

type limitedNPMOutput []byte

func (w *limitedNPMOutput) Write(chunk []byte) (int, error) {
	const limit = 64 * 1024
	if remaining := limit - len(*w); remaining > 0 {
		*w = append(*w, chunk[:min(remaining, len(chunk))]...)
	}
	return len(chunk), nil
}
