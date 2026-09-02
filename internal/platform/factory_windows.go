//go:build windows

package platform

import winplatform "github.com/local/work/internal/platform/windows"
import "github.com/local/work/internal/dshmanager"

func newDependencies() Dependencies {
	return Dependencies{
		Supervisor:      winplatform.NewJobObjectAdapter(),
		CommandExecutor: winplatform.NewCommandExecutor(),
	}
}

func NewRuntimeInstaller(storeRoot string) dshmanager.RuntimeInstaller {
	return winplatform.NewRuntimeInstaller(winplatform.NewCommandExecutor(), storeRoot)
}
