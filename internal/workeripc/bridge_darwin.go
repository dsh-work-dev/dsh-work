//go:build darwin

package workeripc

import (
	"errors"
	"os"
	"path/filepath"
)

var ErrMacBridgeMissing = errors.New("WORKER_IPC_MAC_BRIDGE_MISSING")

func bundledMacBridge() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	// The app build places this ABI-stable addon beside its owned executable.
	// No request, profile, page, or DSH module-resolution path chooses it.
	path := filepath.Join(filepath.Dir(executable), "workeripc-mac.node")
	return macBridgeAt(path)
}
func macBridgeAt(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", ErrMacBridgeMissing
	}
	return path, nil
}
func prepareLocalBridge(config map[string]string) error {
	path, err := bundledMacBridge()
	if err != nil {
		return err
	}
	config["nativeBridge"] = path
	return nil
}
