//go:build darwin && cgo

package mac

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/local/dsh-work/internal/acquisition"
	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/platform/automationpackages"
)

const (
	macCuaArchiveRoot = "cua-driver-rs-" + automation.DriverVersion + "-darwin-arm64"
	macCuaAppName     = "CuaDriver.app"
)

func stageMacAutomationPackage(ctx context.Context, root string, capability automation.Capability, report func(automation.Progress)) error {
	if capability != automation.Computer {
		return nil
	}
	report(automation.Progress{Step: "download"})
	result, err := acquisition.Acquire(ctx, acquisition.Request{
		OperationID: "automation-cua-driver", StagingRoot: filepath.Dir(root), StagePrefix: "cua-",
		Identity: acquisition.ArtifactIdentity{Kind: acquisition.ArtifactPlugin, Name: "cua-driver", Version: automation.DriverVersion,
			Platform: "darwin", Architecture: "arm64", Filename: automation.MacDriverArchive},
		Candidates: []acquisition.SourceCandidate{{Route: acquisition.RouteOfficial, Location: automation.MacDriverURL}},
	}, acquisition.VerifiedArchive{SHA256: automation.MacDriverSHA256, Report: func(_ acquisition.Route, received, total int64, _ bool) {
		report(automation.Progress{Step: "download", ReceivedBytes: received, TotalBytes: total})
	}}, nil)
	if err != nil {
		return automationpackages.PreparationError(err)
	}
	defer os.RemoveAll(result.StagingPath)
	report(automation.Progress{Step: "verify"})
	// The pinned full app carries its native signing and permissions identity.
	// Darwin's bsdtar rejects traversal and writes through symlinks by default.
	selected := macCuaArchiveRoot + "/" + macCuaAppName
	if err := exec.CommandContext(ctx, "/usr/bin/tar", "-xzf", result.PayloadPath, "--strip-components", "1",
		"--no-same-owner", "--no-same-permissions", "-C", root, selected).Run(); err != nil {
		return automationpackages.Failure{Code: "verify", Cause: errors.Join(errors.New("extract Mac Cua Driver"), err)}
	}
	return nil
}

func macCuaPackageInstalled(root string) bool {
	app := filepath.Join(root, macCuaAppName)
	info, err := os.Lstat(app)
	if err != nil || !info.IsDir() {
		return false
	}
	for _, name := range []string{"cua-driver", "cua-cursor-theme"} {
		binary, err := os.Lstat(filepath.Join(app, "Contents", "MacOS", name))
		if err != nil || !binary.Mode().IsRegular() || binary.Mode().Perm()&0111 == 0 {
			return false
		}
	}
	return true
}

func verifyMacCuaPackage(ctx context.Context, root string) error {
	if !macCuaPackageInstalled(root) {
		return errors.New("Mac Cua Driver application is incomplete")
	}
	bounded, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	const requirement = `anchor apple generic and identifier "com.trycua.driver" and certificate leaf[subject.OU] = "YCK386LBJ7"`
	app := filepath.Join(root, macCuaAppName)
	if err := exec.CommandContext(bounded, "/usr/bin/codesign", "--verify", "--deep", "--strict", "-R="+requirement, app).Run(); err != nil {
		return errors.Join(errors.New("Mac Cua Driver publisher signature rejected"), err)
	}
	driver := filepath.Join(app, "Contents", "MacOS", "cua-driver")
	version, err := exec.CommandContext(bounded, driver, "--version").Output()
	if err != nil || string(version) != "cua-driver "+automation.DriverVersion+"\n" {
		return errors.Join(errors.New("Mac Cua Driver version rejected"), err)
	}
	return nil
}
