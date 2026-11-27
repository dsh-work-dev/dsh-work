//go:build !darwin || !cgo

package platform

import (
	"fmt"
	"os"
	"runtime"

	"github.com/local/dsh-work/internal/embeddedbrowser/distribution"
	"github.com/local/dsh-work/internal/version"
)

func VerifyCEFPackage() error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	result := distribution.Inspect(executable, version.String(), runtime.GOARCH, version.CEFPublicationSHA256)
	if result.State != distribution.Ready {
		return fmt.Errorf("CEF_PACKAGE_INVALID: %s: %w", result.State, result.Reason)
	}
	return nil
}
