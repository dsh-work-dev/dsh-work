//go:build windows

package windows

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/local/dsh-work/internal/acquisition"
	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/dshadapter"
	"github.com/local/dsh-work/internal/dshmanager"
	"github.com/local/dsh-work/internal/selfupdate"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

type automationFailure struct {
	code  string
	cause error
}

func (e automationFailure) Error() string             { return "automation preparation failed: " + e.code }
func (e automationFailure) Unwrap() error             { return e.cause }
func (e automationFailure) AutomationFailure() string { return e.code }

type AutomationPreparer struct {
	inventoryMu        sync.Mutex
	driverProbeTime    time.Time
	driverProbeVersion string
	driverProbePath    string
	root               string
	executor           dshadapter.CommandExecutor
	downloader         artifactDownloader
	signature          func(string, string) error
	resolve            func(context.Context) (dshmanager.ResolvedNode, error)
}

func NewAutomationPreparer(root string) *AutomationPreparer {
	return &AutomationPreparer{root: root, executor: NewCommandExecutor(), downloader: httpArtifactDownloader{}, signature: selfupdate.VerifyAuthenticodePublisher}
}
func (i *AutomationPreparer) Prepare(ctx context.Context, c automation.Capability, report func(automation.Progress)) error {
	if !c.Valid() {
		return errors.New("unknown capability")
	}
	if c != automation.Browser && runtime.GOARCH != "amd64" {
		return automationFailure{"other", errors.New("computer preparation is currently Windows amd64 only")}
	}
	report(automation.Progress{Step: "verify"})
	if c == automation.Computer {
		if err := i.driver(ctx, report); err != nil {
			return err
		}
	}
	return i.packages(ctx, c, report)
}

// ConfigureToolchain binds preparation to the already validated Run context.
func (i *AutomationPreparer) ConfigureToolchain(resolve func(context.Context) (dshmanager.ResolvedNode, error)) {
	i.resolve = resolve
}
func (i *AutomationPreparer) Ready(c automation.Capability) bool {
	dir, e := automation.PublishedPackage(i.root, c)
	if e != nil {
		return false
	}
	files, e := automation.DependencyFiles(c)
	if e != nil {
		return false
	}
	for name, want := range files {
		got, e := os.ReadFile(filepath.Join(dir, name))
		if e != nil || string(got) != string(want) {
			return false
		}
	}
	if validateAutomationPackages(dir, c) != nil {
		return false
	}
	if c == automation.Computer {
		return i.driverReady()
	}
	return true
}
func (i *AutomationPreparer) Repair(ctx context.Context, c automation.Capability, report func(automation.Progress)) error {
	if c == automation.Computer {
		if err := i.driver(ctx, report); err != nil {
			return err
		}
	}
	return i.preparePackages(ctx, c, report, true)
}
func (i *AutomationPreparer) packages(ctx context.Context, c automation.Capability, report func(automation.Progress)) error {
	return i.preparePackages(ctx, c, report, false)
}
func (i *AutomationPreparer) preparePackages(ctx context.Context, c automation.Capability, report func(automation.Progress), force bool) error {
	files, err := automation.DependencyFiles(c)
	if err != nil {
		return err
	}
	valid := func(dir string) bool {
		for name, want := range files {
			got, e := os.ReadFile(filepath.Join(dir, name))
			if e != nil || string(got) != string(want) {
				return false
			}
		}
		return validateAutomationPackages(dir, c) == nil
	}
	publish := func(dir string) error {
		root := automation.PackageRoot(i.root, c)
		if err := os.MkdirAll(root, 0700); err != nil {
			return err
		}
		rel, e := filepath.Rel(root, dir)
		if e != nil {
			return e
		}
		b, e := json.Marshal(automation.PackagePublication{Revision: automation.PackageRevision(c), Directory: rel})
		if e != nil {
			return e
		}
		f, e := os.CreateTemp(root, "current-*.tmp")
		if e != nil {
			return e
		}
		defer os.Remove(f.Name())
		defer f.Close()
		if _, e = f.Write(b); e != nil {
			return e
		}
		if e = f.Sync(); e != nil {
			return e
		}
		if e = f.Close(); e != nil {
			return e
		}
		return NewFileReplacer().Replace(f.Name(), automation.PackagePointer(i.root, c))
	}
	if !force {
		if dir, e := automation.PublishedPackage(i.root, c); e == nil && valid(dir) {
			report(automation.Progress{Step: "verify"})
			return nil
		}
		// Adopt the old valid directory without mutating it; running jobs may own it.
		legacy := filepath.Join(automation.PackageRoot(i.root, c), automation.DSHVersion)
		if valid(legacy) {
			report(automation.Progress{Step: "verify"})
			return publish(legacy)
		}
	}
	var command string
	var args []string
	var env map[string]string
	if i.resolve != nil {
		node, e := i.resolve(ctx)
		if e != nil {
			return automationFailure{"toolchain", e}
		}
		cli := filepath.Join(filepath.Dir(node.NodePath), "node_modules", "npm", "bin", "npm-cli.js")
		if info, e := os.Stat(cli); e == nil && !info.IsDir() {
			command = node.NodePath
			args = []string{cli}
		} else if node.NPMPath != "" {
			command = node.NPMPath
		} else {
			return automationFailure{"toolchain", errors.New("selected Node installation has no npm; install or repair Node in Runtimes")}
		}
		env = node.ChildEnvironment
	} else {
		command, err = exec.LookPath("npm.cmd")
		if err != nil {
			return automationFailure{"toolchain", err}
		}
	}
	stageRoot := filepath.Join(i.root, ".staging")
	if err = os.MkdirAll(stageRoot, 0700); err != nil {
		return err
	}
	adapter := automationPackageAdapter{executor: i.executor, files: files, command: command, args: args, environment: env}
	report(automation.Progress{Step: "download"})
	result, err := acquisition.Acquire(ctx, acquisition.Request{OperationID: "automation-" + string(c), Identity: acquisition.ArtifactIdentity{Kind: acquisition.ArtifactPlugin, Name: "dsh-work-" + string(c), Version: automation.PackageRevision(c)}, Candidates: []acquisition.SourceCandidate{{Route: acquisition.RouteOfficial, Location: "https://registry.npmjs.org/"}}, StagingRoot: stageRoot, StagePrefix: "automation-"}, adapter, nil)
	if err != nil {
		return preparationError(err)
	}
	defer os.RemoveAll(result.StagingPath)
	report(automation.Progress{Step: "verify"})
	if err = validateAutomationPackages(result.PayloadPath, c); err != nil {
		return automationFailure{"verify", err}
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	releases := filepath.Join(automation.PackageRoot(i.root, c), "releases", automation.PackageRevision(c))
	if err = os.MkdirAll(releases, 0700); err != nil {
		return err
	}
	// Every publication is immutable, including a repair of the same lock.
	destination, err := os.MkdirTemp(releases, "set-")
	if err != nil {
		return err
	}
	if err = os.Remove(destination); err != nil {
		return err
	}
	if err = os.Rename(result.PayloadPath, destination); err != nil {
		return err
	}
	// A failed pointer replacement leaves the previous complete set selected.
	return publish(destination)
}

func preparationError(err error) error {
	var f acquisition.Failure
	if errors.As(err, &f) {
		if f.Kind == acquisition.FailureIntegrity {
			return automationFailure{"verify", err}
		}
		if f.Kind == acquisition.FailureReachability || f.Kind == acquisition.FailureNotFound {
			return automationFailure{"network", err}
		}
	}
	return automationFailure{"other", err}
}

type automationPackageAdapter struct {
	executor    dshadapter.CommandExecutor
	files       map[string][]byte
	command     string
	args        []string
	environment map[string]string
}

func (a automationPackageAdapter) Attempt(ctx context.Context, r acquisition.AttemptRequest) (acquisition.AttemptResult, error) {
	for name, data := range a.files {
		if err := os.WriteFile(filepath.Join(r.StagingPath, name), data, 0600); err != nil {
			return acquisition.AttemptResult{}, err
		}
	}
	command := a.command
	var err error
	if command == "" {
		command, err = exec.LookPath("npm.cmd")
		if err != nil {
			return acquisition.AttemptResult{}, err
		}
	}
	args := append(append([]string{}, a.args...), "ci", "--ignore-scripts", "--no-audit", "--no-fund")
	result, err := a.executor.Run(ctx, command, args, a.environment, r.StagingPath)
	if err != nil {
		return acquisition.AttemptResult{}, classifyPackageManagerFailure(result, err)
	}
	payload := filepath.Join(r.StagingPath, "payload")
	if err := os.Mkdir(payload, 0700); err != nil {
		return acquisition.AttemptResult{}, err
	}
	for _, name := range []string{"package.json", "package-lock.json", "node_modules"} {
		if err := os.Rename(filepath.Join(r.StagingPath, name), filepath.Join(payload, name)); err != nil {
			return acquisition.AttemptResult{}, err
		}
	}
	return acquisition.AttemptResult{ResolvedIdentity: r.Identity, PayloadPath: payload}, nil
}
func validateAutomationPackages(root string, c automation.Capability) error {
	if c == automation.AUV {
		info, err := os.Stat(filepath.Join(root, "node_modules", "@auv-js", "cli-win32-x64-msvc", "bin", "auv.exe"))
		if err != nil {
			return err
		}
		if info.IsDir() || info.Size() == 0 {
			return errors.New("AUV executable missing")
		}
	}
	expected := map[string]string{"@modelcontextprotocol/sdk": automation.SDKVersion, "@playwright/mcp": automation.MCPVersion, "ws": automation.BrowserWebSocketVersion}
	if c == automation.AUV {
		expected = map[string]string{"@auv-js/sdk": automation.AUVVersion, "@auv-js/cli-win32-x64-msvc": automation.AUVVersion}
	}
	if c == automation.Computer {
		expected = map[string]string{"@deepseek-ai/dsh-computer-use": automation.DSHVersion, "@deepseek-ai/dsh-experimental-computer-use-cua-driver-mcp": automation.DSHVersion}
	}
	for name, version := range expected {
		data, err := os.ReadFile(filepath.Join(root, "node_modules", filepath.FromSlash(name), "package.json"))
		if err != nil {
			return err
		}
		var m struct {
			Version string `json:"version"`
		}
		if json.Unmarshal(data, &m) != nil || m.Version != version {
			return errors.New("automation package version mismatch")
		}
	}
	return nil
}
func (i *AutomationPreparer) driver(ctx context.Context, report func(automation.Progress)) error {
	root := filepath.Join(i.root, "automation", "cua-driver", automation.DriverVersion, "windows-amd64")
	verify := func(path string) error {
		for _, name := range []string{"cua-driver.exe", "cua-driver-uia.exe", "cua-cursor-theme.exe"} {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := i.signature(filepath.Join(path, name), "Cua AI, Inc."); err != nil {
				return err
			}
		}
		return nil
	}
	if data, e := os.ReadFile(filepath.Join(root, "release.json")); e == nil && string(data) == automation.DriverSHA256 {
		report(automation.Progress{Step: "verify"})
		if err := verify(root); err == nil {
			return nil
		}
	}
	archive := filepath.Join(i.root, "automation", "downloads", automation.DriverArchive)
	if _, err := os.Stat(archive); errors.Is(err, os.ErrNotExist) {
		stageRoot := filepath.Join(i.root, ".staging")
		if err := os.MkdirAll(stageRoot, 0700); err != nil {
			return err
		}
		result, err := acquisition.Acquire(ctx, acquisition.Request{OperationID: "automation-driver", Identity: acquisition.ArtifactIdentity{Kind: acquisition.ArtifactPlugin, Name: "cua-driver", Version: automation.DriverVersion, Platform: "windows", Architecture: "amd64", Filename: automation.DriverArchive}, Candidates: []acquisition.SourceCandidate{{Route: acquisition.RouteOfficial, Location: automation.DriverURL}}, StagingRoot: stageRoot, StagePrefix: "cua-driver-"}, nodeArchiveAcquisitionAdapter{downloader: i.downloader, sha256: automation.DriverSHA256, report: func(_ acquisition.Route, n, total int64, _ bool) {
			report(automation.Progress{Step: "download", ReceivedBytes: n, TotalBytes: total})
		}}, nil)
		if err != nil {
			return preparationError(err)
		}
		defer os.RemoveAll(result.StagingPath)
		if err := os.MkdirAll(filepath.Dir(archive), 0700); err != nil {
			return err
		}
		if err := os.Rename(result.PayloadPath, archive); err != nil {
			return err
		}
	}
	report(automation.Progress{Step: "verify"})
	if err := verifyArchiveDigest(archive, automation.DriverSHA256); err != nil {
		_ = os.Remove(archive)
		return automationFailure{"verify", err}
	}
	if err := os.MkdirAll(filepath.Dir(root), 0700); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(filepath.Dir(root), ".driver-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err := extractAutomationDriver(ctx, archive, stage); err != nil {
		return automationFailure{"verify", err}
	}
	if err := verify(stage); err != nil {
		return automationFailure{"verify", err}
	}
	if err := os.WriteFile(filepath.Join(stage, "release.json"), []byte(automation.DriverSHA256), 0600); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Retain the previous complete release until the staged replacement is published.
	backup, err := os.MkdirTemp(filepath.Dir(root), ".driver-previous-")
	if err != nil {
		return err
	}
	if err = os.Remove(backup); err != nil {
		return err
	}
	_, statErr := os.Stat(root)
	if statErr == nil {
		if err = os.Rename(root, backup); err != nil {
			return err
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	if err = os.Rename(stage, root); err != nil {
		if statErr == nil {
			return errors.Join(err, os.Rename(backup, root))
		}
		return err
	}
	return nil
}
func extractAutomationDriver(ctx context.Context, archive, destination string) error {
	reader, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer reader.Close()
	var total uint64
	if len(reader.File) > 4096 {
		return errors.New("driver archive has too many entries")
	}
	for _, file := range reader.File {
		if err := ctx.Err(); err != nil {
			return err
		}
		name, err := safeArchiveName(file.Name)
		if err != nil {
			return err
		}
		parts := strings.Split(filepath.ToSlash(name), "/")
		if parts[0] != "cua-driver-rs-0.28.1-windows-x86_64" {
			return errors.New("unexpected driver archive root")
		}
		if len(parts) == 1 {
			continue
		}
		total += file.UncompressedSize64
		if total > 1<<30 || file.Mode()&os.ModeSymlink != 0 {
			return errors.New("unsafe driver archive")
		}
		target := filepath.Join(destination, filepath.FromSlash(strings.Join(parts[1:], "/")))
		if !isWithinDirectory(destination, target) {
			return errors.New("driver entry escaped destination")
		}
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0700); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		input, err := file.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			input.Close()
			return err
		}
		_, copyErr := io.Copy(out, io.LimitReader(input, 1<<30))
		err = errors.Join(copyErr, out.Close(), input.Close())
		if err != nil {
			return err
		}
	}
	return nil
}
func verifyArchiveDigest(path, expected string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != expected {
		return errors.New("driver checksum mismatch")
	}
	return nil
}
