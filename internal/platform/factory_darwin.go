//go:build darwin && cgo

package platform

import (
	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/dshmanager"
	macplatform "github.com/local/dsh-work/internal/platform/mac"
	"sync"
)

var macRuntime struct {
	sync.Once
	runtime *macplatform.Runtime
	err     error
}

func selectedMacRuntime() (*macplatform.Runtime, error) {
	macRuntime.Do(func() { macRuntime.runtime, macRuntime.err = macplatform.NewRuntime() })
	return macRuntime.runtime, macRuntime.err
}
func newDependencies() Dependencies {
	runtime, err := selectedMacRuntime()
	if err != nil {
		return Dependencies{Err: err}
	}
	return Dependencies{Supervisor: runtime, CommandExecutor: runtime, FileReplacer: macplatform.FileReplacer{}}
}
func NewRuntimeInstaller(string) dshmanager.RuntimeInstaller     { return nil }
func NewNodeReleaseCatalog(string) dshmanager.NodeReleaseCatalog { return nil }
func NewDSHReleaseCatalog(string) dshmanager.DSHReleaseCatalog   { return nil }
func NewNodeInstaller(string) dshmanager.NodeInstaller           { return nil }
func NewNodeResolver(string) dshmanager.NodeResolver {
	r, err := selectedMacRuntime()
	if err != nil {
		return nil
	}
	return r
}
func NewAutomationPreparer(root string) automation.Preparer {
	return macplatform.FixedBrowserPreparer{Root: root}
}
