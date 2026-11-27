//go:build !linux

package dshadapter

func launchEnvironment() map[string]string { return map[string]string{} }
