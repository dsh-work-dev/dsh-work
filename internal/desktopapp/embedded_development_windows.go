//go:build windows && e67presenter && !production

package desktopapp

import (
	"context"
	"fmt"
	"github.com/local/dsh-work/internal/daemon"
	"github.com/local/dsh-work/internal/dshadapter"
	"github.com/local/dsh-work/internal/dshmanager"
	"github.com/wailsapp/wails/v3/pkg/application"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"time"
)

func embeddedDevelopmentRoot() string {
	if os.Getenv("DSH_WORK_EMBEDDED_CHECKPOINT") != "1" {
		return ""
	}
	root := os.Getenv("DSH_WORK_EMBEDDED_ROOT")
	if !filepath.IsAbs(root) {
		return ""
	}
	return root
}
func embeddedComposition(options *application.WebviewWindowOptions) {
	if embeddedDevelopmentRoot() != "" {
		options.Windows.WebView2CompositionBehindChildWindows = true
	}
}
func embeddedConfigureManager(c *dshmanager.Config, adapter *dshadapter.Adapter) error {
	if embeddedDevelopmentRoot() == "" {
		return nil
	}
	if os.Getenv("DSH_WORK_DESKTOP_REPORT") != "" {
		return fmt.Errorf("embedded candidate cannot use desktop probe")
	}
	ctx, stop := context.WithTimeout(context.Background(), 20*time.Second)
	defer stop()
	runtime, err := adapter.DevelopmentFixture(ctx)
	if err != nil {
		return err
	}
	if runtime.Version != "0.2.2-rc.1" {
		return fmt.Errorf("CEF runtime seam requires DSH 0.2.2-rc.1; found %s", runtime.Version)
	}
	c.Runtimes = []dshmanager.RuntimeInfo{{ID: dshmanager.DevelopmentFixtureRuntimeID, Version: runtime.Version, Path: runtime.Path, Source: dshmanager.RuntimeSourceDevelopmentFixture, Installed: true}}
	c.DefaultRunContext = dshmanager.RunContext{RuntimeID: dshmanager.DevelopmentFixtureRuntimeID, Node: dshmanager.NodeSelection{Kind: dshmanager.NodeSelectionSystem}, Profile: dshmanager.ProfileRef{DataDirectoryID: "dsh-work", Name: dshmanager.AppProfileName}}
	return nil
}

func embeddedRunnerPeer(peer daemon.ProcessPeer) bool {
	if embeddedDevelopmentRoot() == "" {
		return false
	}
	h, e := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, peer.PID)
	if e != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var created, exit, kernel, user windows.Filetime
	if windows.GetProcessTimes(h, &created, &exit, &kernel, &user) != nil || uint64(created.HighDateTime)<<32|uint64(created.LowDateTime) != peer.Started {
		return false
	}
	state, e := windows.WaitForSingleObject(h, 0)
	if e != nil || state != uint32(windows.WAIT_TIMEOUT) {
		return false
	}
	buf := make([]uint16, 32768)
	size := uint32(len(buf))
	if windows.QueryFullProcessImageName(h, 0, &buf[0], &size) != nil {
		return false
	}
	exe, e := os.Executable()
	if e != nil {
		return false
	}
	actual, e := os.Stat(windows.UTF16ToString(buf[:size]))
	if e != nil {
		return false
	}
	expected, e := os.Stat(filepath.Join(filepath.Dir(exe), "e67-dev-runner.exe"))
	return e == nil && os.SameFile(actual, expected)
}
