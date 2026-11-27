//go:build !linux && !darwin

package dshadapter

func launchEnvironment() map[string]string { return map[string]string{} }
