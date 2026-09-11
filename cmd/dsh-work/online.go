package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/local/dsh-work/internal/app"
	"github.com/local/dsh-work/internal/daemon"
	"github.com/local/dsh-work/internal/dshmanager"
	"github.com/local/dsh-work/internal/lifecycle"
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
	err := c.JSON(ctx, "/snapshot", nil, &state)
	if err != nil {
		if args[0] == "status" || args[0] == "stop" || args[0] == "restart" {
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
	case "stop", "restart":
		method := "Quit"
		if args[0] == "restart" {
			method = "Restart"
		}
		var status lifecycle.Status
		err := c.Call(context.Background(), "HostService", method, "workspace", nil, &status)
		if err != nil {
			return true, err
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
