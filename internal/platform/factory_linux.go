//go:build linux

package platform

import (
	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/dshmanager"
	linuxplatform "github.com/local/dsh-work/internal/platform/linux"
)

func newDependencies() Dependencies {
	owner := linuxplatform.NewSystemdAdapter()
	return Dependencies{Supervisor: owner, CommandExecutor: linuxplatform.NewCommandExecutor(owner), FileReplacer: linuxplatform.FileReplacer{}}
}

// Managed acquisition remains unavailable until its platform implementation is
// qualified. The installed system toolchain and explicit development runtime
// do not depend on these installers or on CEF.
func NewRuntimeInstaller(string) dshmanager.RuntimeInstaller     { return nil }
func NewNodeReleaseCatalog(string) dshmanager.NodeReleaseCatalog { return nil }
func NewDSHReleaseCatalog(string) dshmanager.DSHReleaseCatalog   { return nil }
func NewNodeInstaller(string) dshmanager.NodeInstaller           { return nil }
func NewNodeResolver(string) dshmanager.NodeResolver {
	return linuxplatform.NewSystemNodeResolver(linuxplatform.NewCommandExecutor(linuxplatform.NewSystemdAdapter()))
}
func NewAutomationPreparer(string) automation.Preparer { return nil }
