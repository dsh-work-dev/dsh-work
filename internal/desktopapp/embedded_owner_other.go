//go:build !darwin || !cgo

package desktopapp

import (
	"context"
	"os"
	"runtime"

	"github.com/local/dsh-work/internal/embeddedbrowser"
	"github.com/local/dsh-work/internal/embeddedbrowser/distribution"
	"github.com/local/dsh-work/internal/supervisor"
)

func embeddedDevelopmentExecutable() string { return os.Getenv("DSH_WORK_CEF_EXECUTABLE") }

func inspectDarwinPublication() distribution.Result {
	return distribution.Result{State: distribution.UnsupportedPlatform}
}

func embeddedBrowserSupervisor(base supervisor.Adapter, _ string) supervisor.Adapter { return base }
func computerEngineSupported(engine string) bool {
	return runtime.GOOS == "windows" && (engine == "cua-driver" || engine == "auv")
}
func computerRuntimeSupervisor(base supervisor.Adapter, _ string) supervisor.Adapter { return base }
func startEmbeddedBroker(ctx context.Context, c embeddedbrowser.Config) (*embeddedbrowser.Broker, error) {
	return embeddedbrowser.Start(ctx, c)
}
