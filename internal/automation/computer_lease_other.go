//go:build !windows

package automation

func AcquireComputerDesktopLease() (func() error, error) { return nil, ErrUnavailable }
