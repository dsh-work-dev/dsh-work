//go:build !windows

package platform

import "errors"

func newDependencies() Dependencies {
	return Dependencies{
		Err: errors.New("native Work process supervision is not implemented for this target"),
	}
}
