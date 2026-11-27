//go:build !linux && !darwin

package embeddedbrowser

func nativeEnvironment(map[string]string) error { return nil }
