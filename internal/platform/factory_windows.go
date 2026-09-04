//go:build windows

package platform

import winplatform "github.com/local/dsh-work/internal/platform/windows"
import "github.com/local/dsh-work/internal/dshmanager"

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
