//go:build !linux

package platform

func RunBootstrap() bool { return false }
