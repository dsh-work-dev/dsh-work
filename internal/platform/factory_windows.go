//go:build windows

package platform

import winplatform "github.com/local/work/internal/platform/windows"

func newDependencies() Dependencies {
	return Dependencies{
		Supervisor:      winplatform.NewJobObjectAdapter(),
		CommandExecutor: winplatform.NewCommandExecutor(),
	}
}
