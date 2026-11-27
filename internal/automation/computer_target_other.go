//go:build !windows && (!darwin || !cgo)

package automation

func InspectComputerTarget(uint32, uint64) (ComputerTarget, error) {
	return ComputerTarget{}, ErrUnavailable
}
