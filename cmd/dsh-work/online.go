package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/local/dsh-work/internal/app"
	"github.com/local/dsh-work/internal/daemon"
	"github.com/local/dsh-work/internal/dshmanager"
	"github.com/local/dsh-work/internal/lifecycle"
	"github.com/local/dsh-work/internal/storagepaths"
)

type managerAPI interface {
	Snapshot(context.Context) (dshmanager.Snapshot, error)
	InstallRuntime(context.Context, string) (dshmanager.Snapshot, error)
	RegisterRuntime(context.Context, dshmanager.RuntimeInfo) (dshmanager.Snapshot, error)
	RemoveRuntime(context.Context, string) (dshmanager.Snapshot, error)
	RegisterDataDirectory(context.Context, dshmanager.DataDirectoryInfo) (dshmanager.Snapshot, error)
	RemoveDataDirectory(context.Context, string) (dshmanager.Snapshot, error)
	SetConfigured(context.Context, dshmanager.RunContext) (dshmanager.Snapshot, error)
	ListPlugins(context.Context, dshmanager.PluginListRequest) ([]dshmanager.PluginInfo, error)
	InstallPlugin(context.Context, dshmanager.PluginInstallRequest) (dshmanager.PluginResult, error)
	RemovePlugin(context.Context, dshmanager.PluginRemoveRequest) (dshmanager.PluginResult, error)
}
type onlineManager struct{ client *daemon.Client }

func (m onlineManager) Snapshot(ctx context.Context) (v dshmanager.Snapshot, err error) {
	err = m.client.Call(ctx, "ManagerService", "GetSnapshot", "settings", nil, &v)
	return
}
func (m onlineManager) InstallRuntime(ctx context.Context, a string) (v dshmanager.Snapshot, err error) {
	err = m.client.Call(ctx, "ManagerService", "InstallRuntime", "settings", []any{a}, &v)
	return
}
func (m onlineManager) RegisterRuntime(ctx context.Context, a dshmanager.RuntimeInfo) (v dshmanager.Snapshot, err error) {
	err = m.client.Call(ctx, "ManagerService", "RegisterRuntime", "settings", []any{a}, &v)
	return
}
func (m onlineManager) RemoveRuntime(ctx context.Context, a string) (v dshmanager.Snapshot, err error) {
	err = m.client.Call(ctx, "ManagerService", "RemoveRuntime", "settings", []any{a}, &v)
	return
}
func (m onlineManager) RegisterDataDirectory(ctx context.Context, a dshmanager.DataDirectoryInfo) (v dshmanager.Snapshot, err error) {
	err = m.client.Call(ctx, "ManagerService", "RegisterDataDirectory", "settings", []any{a}, &v)
	return
}
func (m onlineManager) RemoveDataDirectory(ctx context.Context, a string) (v dshmanager.Snapshot, err error) {
	err = m.client.Call(ctx, "ManagerService", "RemoveDataDirectory", "settings", []any{a}, &v)
	return
}
func (m onlineManager) SetConfigured(ctx context.Context, a dshmanager.RunContext) (v dshmanager.Snapshot, err error) {
	err = m.client.Call(ctx, "ManagerService", "SetRunContext", "settings", []any{a}, &v)
	return
}
func (m onlineManager) ListPlugins(ctx context.Context, a dshmanager.PluginListRequest) (v []dshmanager.PluginInfo, err error) {
	err = m.client.Call(ctx, "ManagerService", "ListPlugins", "settings", []any{a}, &v)
	return
}
func (m onlineManager) InstallPlugin(ctx context.Context, a dshmanager.PluginInstallRequest) (v dshmanager.PluginResult, err error) {
	err = m.client.Call(ctx, "ManagerService", "InstallPlugin", "settings", []any{a}, &v)
	return
}
func (m onlineManager) RemovePlugin(ctx context.Context, a dshmanager.PluginRemoveRequest) (v dshmanager.PluginResult, err error) {
	err = m.client.Call(ctx, "ManagerService", "RemovePlugin", "settings", []any{a}, &v)
	return
}

func tryOnline(args []string, stdout io.Writer) (bool, error) {
	identity := filepath.Dir(app.DefaultConfig("").SettingsPath)
	if root := os.Getenv("DSH_WORK_DESKTOP_ROOT"); root != "" && os.Getenv("DSH_WORK_DESKTOP_REPORT") != "" {
		identity = root
	}
	c := daemon.NewClient(identity)
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var state daemon.Snapshot
	waitForStop := args[0] == "stop" && hasFlag(args[1:], "--wait")
	err := c.JSON(ctx, "/snapshot", nil, &state)
	if err != nil {
		if waitForStop {
			requestDesktopUIStop(identity)
			if waitErr := waitForDaemonStop(identity, state.Root, 30*time.Second); waitErr != nil {
				return true, waitErr
			}
			return true, printValue(stdout, true, lifecycle.Status{State: lifecycle.StateStopped, Phase: lifecycle.PhaseIdle}, func() {})
		}
		if args[0] == "status" || args[0] == "stop" || args[0] == "restart" || args[0] == "update" {
			return true, err
		}
		return false, nil
	}
	if state.Protocol != daemon.Protocol {
		return true, fmt.Errorf("background protocol mismatch")
	}
	m := onlineManager{client: c}
	switch args[0] {
	case "status":
		return true, printValue(stdout, true, state, func() {})
	case "update":
		return true, runOnlineUpdate(c, args[1:], state, stdout)
	case "stop", "restart":
		method := "Quit"
		if args[0] == "restart" {
			method = "Restart"
		}
		if waitForStop {
			requestDesktopUIStop(identity)
		}
		var status lifecycle.Status
		err := c.Call(context.Background(), "HostService", method, "workspace", nil, &status)
		if err != nil {
			return true, err
		}
		if waitForStop {
			c.Close()
			if err := waitForDaemonStop(identity, state.Root, 30*time.Second); err != nil {
				return true, err
			}
		}
		return true, printValue(stdout, true, status, func() {})
	case "runtime":
		return true, runRuntime(m, args[1:], stdout)
	case "data-directory":
		return true, runDataDirectory(m, args[1:], stdout)
	case "profile":
		return true, runProfile(m, args[1:], stdout)
	case "use":
		return true, runUse(m, args[1:], stdout)
	case "plugin":
		return true, runPlugin(m, args[1:], stdout)
	default:
		return true, fmt.Errorf("unknown command %q", args[0])
	}
}

func runOnlineUpdate(client *daemon.Client, args []string, snapshot daemon.Snapshot, stdout io.Writer) error {
	if len(args) == 0 || args[0] == "status" {
		if len(args) > 1 {
			return fmt.Errorf("unexpected arguments for update status: %v", args[1:])
		}
		return printValue(stdout, true, snapshot.Update, func() {})
	}
	if len(args) != 1 {
		return fmt.Errorf("usage: dsh-work update status|check|download|install")
	}
	var action daemon.UpdateAction
	switch args[0] {
	case string(daemon.UpdateActionCheck):
		action = daemon.UpdateActionCheck
	case string(daemon.UpdateActionDownload):
		action = daemon.UpdateActionDownload
	case string(daemon.UpdateActionInstall):
		action = daemon.UpdateActionInstall
	default:
		return fmt.Errorf("unknown update action %q; use status, check, download, or install", args[0])
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var update daemon.UpdateSnapshot
	err := client.JSON(ctx, "/update/action", struct {
		Action daemon.UpdateAction `json:"action"`
	}{Action: action}, &update)
	if err != nil {
		return err
	}
	return printValue(stdout, true, update, func() {})
}

func hasFlag(args []string, wanted string) bool {
	for _, arg := range args {
		if arg == wanted {
			return true
		}
	}
	return false
}

func requestDesktopUIStop(identity string) {
	client := daemon.NewClient(identity + "-ui")
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = client.JSON(ctx, "/stop", nil, nil)
}

// waitForDaemonStop waits for both the IPC endpoint and the manager lock to
// disappear. A successful Quit RPC only requests shutdown; the daemon still
// needs to close its Worker, Wails application and process locks.
func waitForDaemonStop(identity, root string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	if root == "" {
		var resolveErr error
		root, resolveErr = resolveStorageRoot()
		if resolveErr != nil {
			return lifecycle.Failure{
				Code:          lifecycle.ErrorProcessStopFailed,
				Summary:       "dsh-work could not prove that the background stopped.",
				Retryable:     true,
				CorrelationID: lifecycle.NewCorrelationID(),
				Detail:        "The current storage location could not be resolved safely; retry after checking the location file.",
			}
		}
	}
	if !filepath.IsAbs(root) {
		return lifecycle.Failure{
			Code:          lifecycle.ErrorProcessStopFailed,
			Summary:       "dsh-work could not prove that the background stopped.",
			Retryable:     true,
			CorrelationID: lifecycle.NewCorrelationID(),
			Detail:        "The current storage location is not an absolute user path; retry after repairing the location file.",
		}
	}
	lastObservation := "the daemon and UI endpoints were not reachable"
	for time.Now().Before(deadline) {
		client := daemon.NewClient(identity)
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		var state daemon.Snapshot
		err := client.JSON(ctx, "/snapshot", nil, &state)
		cancel()
		client.Close()
		if err == nil {
			lastObservation = "the daemon endpoint was still reachable"
			if state.Root != "" {
				root = state.Root
			}
			time.Sleep(150 * time.Millisecond)
			continue
		}
		uiClient := daemon.NewClient(identity + "-ui")
		uiCtx, uiCancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		var uiStatus struct{ PID int }
		uiErr := uiClient.JSON(uiCtx, "/status", nil, &uiStatus)
		uiCancel()
		uiClient.Close()
		if uiErr == nil {
			lastObservation = "the desktop UI endpoint was still reachable"
			time.Sleep(150 * time.Millisecond)
			continue
		}
		lockPath := filepath.Join(root, "manager.lock")
		if _, statErr := os.Stat(lockPath); statErr != nil {
			if errors.Is(statErr, os.ErrNotExist) {
				return nil
			}
			lastObservation = "the manager lock could not be checked"
		} else {
			lastObservation = "the manager lock was still held"
			lock, lockErr := app.AcquireManagerProcessLock(filepath.Join(root, "settings.json"))
			if lockErr == nil {
				return lock.Close()
			}
		}
		time.Sleep(150 * time.Millisecond)
	}
	return lifecycle.Failure{
		Code:          lifecycle.ErrorProcessStopFailed,
		Summary:       "dsh-work background did not stop.",
		Retryable:     true,
		CorrelationID: lifecycle.NewCorrelationID(),
		Detail:        fmt.Sprintf("%s after %s.", lastObservation, timeout.Round(time.Second)),
	}
}

func resolveStorageRoot() (string, error) {
	defaultRoot := filepath.Dir(app.DefaultConfig("").SettingsPath)
	locator := filepath.Join(filepath.Dir(defaultRoot), "dsh-work-location", "locations.json")
	locations, err := storagepaths.Open(locator, defaultRoot)
	if err != nil {
		return "", err
	}
	root := locations.Snapshot().Current.Root
	if root == "" {
		return "", errors.New("storage location has no current root")
	}
	return root, nil
}
