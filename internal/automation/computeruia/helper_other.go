//go:build !windows

package computeruia

import "errors"

func RunHelper() error { return errors.New("computer UIA requires Windows") }
