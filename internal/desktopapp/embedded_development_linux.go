//go:build linux && cgo && e67presenter && !production && !server

package desktopapp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/local/dsh-work/internal/daemon"
	"github.com/local/dsh-work/internal/dshadapter"
	"github.com/local/dsh-work/internal/dshmanager"
	"github.com/wailsapp/wails/v3/pkg/application"
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
func embeddedComposition(*application.WebviewWindowOptions) {}
func embeddedRunnerPeer(daemon.ProcessPeer) bool            { return false }
func embeddedConfigureManager(c *dshmanager.Config, adapter *dshadapter.Adapter) error {
	if embeddedDevelopmentRoot() == "" {
		return nil
	}
	if os.Getenv("DSH_WORK_DESKTOP_REPORT") != "" {
		return fmt.Errorf("embedded candidate cannot use desktop probe")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
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
