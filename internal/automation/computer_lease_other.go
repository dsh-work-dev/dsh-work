//go:build !windows && (!darwin || !cgo)

package automation

func AcquireComputerDesktopLease() (func() error, error) { return nil, ErrUnavailable }
