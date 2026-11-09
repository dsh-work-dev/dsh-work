//go:build !darwin

package workeripc

func prepareLocalBridge(map[string]string) error { return nil }
