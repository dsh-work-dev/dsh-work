//go:build !windows

package pet

func isReparsePoint(string) bool { return false }
