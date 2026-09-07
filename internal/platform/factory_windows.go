//go:build windows

package platform

import (
	"github.com/local/dsh-work/internal/dshmanager"
	winplatform "github.com/local/dsh-work/internal/platform/windows"
)

func newDependencies() Dependencies {
	return Dependencies{
		Supervisor:      winplatform.NewJobObjectAdapter(),
		CommandExecutor: winplatform.NewCommandExecutor(),
		FileReplacer:    winplatform.NewFileReplacer(),
	}
}

func NewRuntimeInstaller(storeRoot string) dshmanager.RuntimeInstaller {
	return winplatform.NewRuntimeInstaller(winplatform.NewCommandExecutor(), storeRoot)
}

func NewNodeReleaseCatalog(storeRoot string) dshmanager.NodeReleaseCatalog {
	return winplatform.NewNodeReleaseCatalog(storeRoot)
}

func NewDSHReleaseCatalog(storeRoot string) dshmanager.DSHReleaseCatalog {
	return winplatform.NewDSHReleaseCatalog(storeRoot)
}

func NewNodeInstaller(storeRoot string) dshmanager.NodeInstaller {
	return winplatform.NewManagedNodeInstaller(winplatform.NewCommandExecutor(), storeRoot)
}

func NewNodeResolver(storeRoot string) dshmanager.NodeResolver {
	return winplatform.NewRunNodeResolver(winplatform.NewCommandExecutor(), storeRoot)
}
