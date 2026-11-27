//go:build darwin && cgo

package platform

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/local/dsh-work/internal/embeddedbrowser/macowner"
)

func VerifyCEFPackage() error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := macowner.OpenInstalledPublication(ctx, executable); err != nil {
		return fmt.Errorf("CEF_PACKAGE_INVALID: %w", err)
	}
	return nil
}
