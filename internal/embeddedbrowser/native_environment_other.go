//go:build !linux

package embeddedbrowser

func nativeEnvironment(map[string]string) error { return nil }
