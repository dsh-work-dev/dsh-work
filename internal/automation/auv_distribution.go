package automation

import (
	"errors"
	"path/filepath"
	"runtime"
)

const MacAUVCodeRequirement = `anchor apple generic and identifier "auv" and certificate leaf[subject.OU] = "433DLLA855"`

// AUVNativePackage is the binary package in the host's locked AUV dependency set.
func AUVNativePackage() (name, filename string, err error) {
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "windows/amd64":
		return "@auv-js/cli-win32-x64-msvc", "auv.exe", nil
	case "darwin/arm64":
		return "@auv-js/cli-darwin-arm64", "auv", nil
	default:
		return "", "", ErrUnavailable
	}
}

func AUVExecutable(root string) (string, error) {
	name, filename, err := AUVNativePackage()
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(root) {
		return "", errors.New("absolute AUV publication directory required")
	}
	return filepath.Join(root, "node_modules", filepath.FromSlash(name), "bin", filename), nil
}
