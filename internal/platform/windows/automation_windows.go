//go:build windows

package windows

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/local/dsh-work/internal/acquisition"
	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/dshadapter"
	"github.com/local/dsh-work/internal/dshmanager"
	"github.com/local/dsh-work/internal/platform/automationpackages"
	"github.com/local/dsh-work/internal/selfupdate"
	"io"
	"os"
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
func (i *AutomationPreparer) packageInstaller() *automationpackages.Installer {
	return &automationpackages.Installer{Root: i.root, Executor: i.executor, Replacer: NewFileReplacer(), Resolve: i.resolve, NPMCommand: "npm.cmd"}
}
func (i *AutomationPreparer) Ready(c automation.Capability) bool {
	return i.packageInstaller().Ready(c) && (c != automation.Computer || i.driverReady())
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
	return i.packageInstaller().Prepare(ctx, c, report, force)
}
func preparationError(err error) error { return automationpackages.PreparationError(err) }
func validateAutomationPackages(root string, c automation.Capability) error {
	return automationpackages.Validate(root, c)
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
