//go:build !windows

package platform

import (
	"errors"

	"github.com/local/dsh-work/internal/dshmanager"
)

func newDependencies() Dependencies {
	return Dependencies{
		Err: errors.New("native dsh-work process supervision is not implemented for this target"),
	}
}

// NewRuntimeInstaller deliberately returns nil until a native installer is
// implemented for this platform. It is not a production fake adapter.
func NewRuntimeInstaller(string) dshmanager.RuntimeInstaller {
	return nil
}

func NewNodeReleaseCatalog(string) dshmanager.NodeReleaseCatalog { return nil }

func NewDSHReleaseCatalog(string) dshmanager.DSHReleaseCatalog { return nil }

func NewNodeInstaller(string) dshmanager.NodeInstaller { return nil }

func NewNodeResolver(string) dshmanager.NodeResolver { return nil }
