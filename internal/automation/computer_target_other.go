//go:build !windows

package automation

func InspectComputerTarget(uint32, uint64) (ComputerTarget, error) {
	return ComputerTarget{}, ErrUnavailable
}
