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
	"github.com/local/dsh-work/internal/selfupdate"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type automationFailure struct {
	code  string
	cause error
}

func (e automationFailure) Error() string             { return "automation preparation failed: " + e.code }
func (e automationFailure) Unwrap() error             { return e.cause }
func (e automationFailure) AutomationFailure() string { return e.code }

type AutomationPreparer struct {
	root       string
	executor   dshadapter.CommandExecutor
	downloader artifactDownloader
	signature  func(string, string) error
}

func NewAutomationPreparer(root string) *AutomationPreparer {
	return &AutomationPreparer{root: root, executor: NewCommandExecutor(), downloader: httpArtifactDownloader{}, signature: selfupdate.VerifyAuthenticodePublisher}
}
func (i *AutomationPreparer) Prepare(ctx context.Context, c automation.Capability, report func(automation.Progress)) error {
	if !c.Valid() {
		return errors.New("unknown capability")
	}
	if c == automation.Computer && runtime.GOARCH != "amd64" {
		return automationFailure{"other", errors.New("computer preparation is currently Windows amd64 only")}
	}
	report(automation.Progress{Step: "download"})
	if err := i.packages(ctx, c, report); err != nil {
		return err
	}
	if c == automation.Computer {
		return i.driver(ctx, report)
	}
	return nil
}
func (i *AutomationPreparer) packages(ctx context.Context, c automation.Capability, report func(automation.Progress)) error {
	files, err := automation.DependencyFiles(c)
	if err != nil {
		return err
	}
	destination := filepath.Join(i.root, "automation", string(c), automation.DSHVersion)
	valid := func() bool {
		for name, want := range files {
			got, e := os.ReadFile(filepath.Join(destination, name))
			if e != nil || string(got) != string(want) {
				return false
			}
		}
		return validateAutomationPackages(destination, c) == nil
	}
	if valid() {
		report(automation.Progress{Step: "verify"})
		return nil
	}
	stageRoot := filepath.Join(i.root, ".staging")
	if err := os.MkdirAll(stageRoot, 0700); err != nil {
		return err
	}
	result, err := acquisition.Acquire(ctx, acquisition.Request{OperationID: "automation-" + string(c), Identity: acquisition.ArtifactIdentity{Kind: acquisition.ArtifactPlugin, Name: "dsh-work-" + string(c), Version: automation.DSHVersion}, Candidates: []acquisition.SourceCandidate{{Route: acquisition.RouteOfficial, Location: "https://registry.npmjs.org/"}}, StagingRoot: stageRoot, StagePrefix: "automation-"}, automationPackageAdapter{i.executor, files}, nil)
	if err != nil {
		return preparationError(err)
	}
	defer os.RemoveAll(result.StagingPath)
	report(automation.Progress{Step: "verify"})
	if err := validateAutomationPackages(result.PayloadPath, c); err != nil {
		return automationFailure{"verify", err}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		return err
	}
	if !valid() {
		if err := os.RemoveAll(destination); err != nil {
			return err
		}
	}
	return os.Rename(result.PayloadPath, destination)
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
	executor dshadapter.CommandExecutor
	files    map[string][]byte
}

func (a automationPackageAdapter) Attempt(ctx context.Context, r acquisition.AttemptRequest) (acquisition.AttemptResult, error) {
	for name, data := range a.files {
		if err := os.WriteFile(filepath.Join(r.StagingPath, name), data, 0600); err != nil {
			return acquisition.AttemptResult{}, err
		}
	}
	npm, err := exec.LookPath("npm.cmd")
	if err != nil {
		return acquisition.AttemptResult{}, err
	}
	result, err := a.executor.Run(ctx, npm, []string{"ci", "--ignore-scripts", "--no-audit", "--no-fund"}, nil, r.StagingPath)
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
	expected := map[string]string{"@modelcontextprotocol/sdk": automation.SDKVersion, "@playwright/mcp": automation.MCPVersion}
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
		if err := verify(root); err != nil {
			_ = os.Remove(filepath.Join(root, "release.json"))
			return automationFailure{"verify", err}
		}
		return nil
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
	if err := os.RemoveAll(root); err != nil {
		return err
	}
	return os.Rename(stage, root)
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
