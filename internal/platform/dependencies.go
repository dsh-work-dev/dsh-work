package platform

import (
	"github.com/local/work/internal/dshadapter"
	"github.com/local/work/internal/settings"
	"github.com/local/work/internal/supervisor"
)

// Dependencies are the native seams selected by the current build target.
// The rest of Work only sees the shared interfaces; platform handles and
// signals never cross this boundary.
type Dependencies struct {
	Supervisor      supervisor.Adapter
	CommandExecutor dshadapter.CommandExecutor
	FileReplacer    settings.FileReplacer
	Err             error
}

func New() Dependencies {
	return newDependencies()
}
