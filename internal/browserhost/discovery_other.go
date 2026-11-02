//go:build !windows

package browserhost

func discover() Discovery { return Discovery{Default: "unknown"} }
