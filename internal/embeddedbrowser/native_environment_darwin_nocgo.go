//go:build darwin && !cgo

package embeddedbrowser

import "errors"

func nativeEnvironment(map[string]string) error {
	return errors.New("MAC_CEF_NATIVE_BRIDGE_UNAVAILABLE")
}
