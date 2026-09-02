//go:build windows

package windows

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/local/work/internal/dshadapter"
	"github.com/local/work/internal/dshmanager"
	"github.com/local/work/internal/lifecycle"
)

// RuntimeInstaller performs an explicit DSH package installation into Work's
// managed runtime store. It uses npm only for this user-requested operation;
// normal Work startup never reaches this adapter.
type RuntimeInstaller struct {
	executor  dshadapter.CommandExecutor
	storeRoot string
}

func NewRuntimeInstaller(executor dshadapter.CommandExecutor, storeRoot string) dshmanager.RuntimeInstaller {
	return RuntimeInstaller{executor: executor, storeRoot: storeRoot}
}

func (i RuntimeInstaller) Install(ctx context.Context, version string) (dshmanager.RuntimeInfo, error) {
	if i.executor == nil {
		return dshmanager.RuntimeInfo{}, lifecycle.Failure{
			Code:    lifecycle.ErrorRuntimeInstallUnavailable,
			Summary: "The Windows runtime command adapter is unavailable.",
		}
	}
	if !runtimeVersionPattern.MatchString(version) {
		return dshmanager.RuntimeInfo{}, lifecycle.Failure{
			Code:    lifecycle.ErrorRuntimeInstallFailed,
			Summary: "The requested DSH runtime version is invalid.",
		}
	}
	if strings.TrimSpace(i.storeRoot) == "" {
		return dshmanager.RuntimeInfo{}, lifecycle.Failure{
			Code:    lifecycle.ErrorRuntimeInstallFailed,
			Summary: "The Work runtime store is unavailable.",
		}
	}
	storeRoot, err := filepath.Abs(i.storeRoot)
	if err != nil || storeRoot == "" {
		return dshmanager.RuntimeInfo{}, lifecycle.Failure{
			Code:    lifecycle.ErrorRuntimeInstallFailed,
			Summary: "The Work runtime store is unavailable.",
		}
	}
	destination := filepath.Join(storeRoot, "dsh-"+version)
	if err := os.MkdirAll(destination, 0o700); err != nil {
		return dshmanager.RuntimeInfo{}, lifecycle.Failure{
			Code:    lifecycle.ErrorRuntimeInstallFailed,
			Summary: "The Work runtime store could not be prepared.",
		}
	}
	_, err = i.executor.Run(ctx, "npm.cmd", []string{
		"install", "--prefix", destination, "--ignore-scripts", "--no-save", "@deepseek-ai/dsh@" + version,
	}, nil, destination)
	if err != nil {
		return dshmanager.RuntimeInfo{}, lifecycle.Failure{
			Code:      lifecycle.ErrorRuntimeInstallFailed,
			Summary:   "npm could not install the requested DSH runtime.",
			Retryable: true,
		}
	}
	executable := filepath.Join(destination, "node_modules", ".bin", "dsh.cmd")
	info, err := os.Stat(executable)
	if err != nil || info.IsDir() {
		return dshmanager.RuntimeInfo{}, lifecycle.Failure{
			Code:    lifecycle.ErrorRuntimeInstallFailed,
			Summary: "The installed DSH runtime did not expose a Windows launcher.",
		}
	}
	return dshmanager.RuntimeInfo{
		ID: "dsh-" + version, Version: version, Path: executable,
		Source: dshmanager.RuntimeSourceManaged, Installed: true, Removable: true,
	}, nil
}

var runtimeVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?$`)
