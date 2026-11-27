package desktopapp

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/local/dsh-work/internal/embeddedbrowser/distribution"
	"github.com/local/dsh-work/internal/version"
)

var publicationOnce sync.Once
var publicationResult distribution.Result

// The checkpoint override is confined to its explicit development gate.
// Formal builds resolve only the application-owned publication.
func embeddedPublication() distribution.Result {
	publicationOnce.Do(func() { publicationResult = inspectEmbeddedPublication() })
	return publicationResult
}

func inspectEmbeddedPublication() distribution.Result {
	if runtime.GOOS != "windows" || runtime.GOARCH != "amd64" {
		return distribution.Result{State: distribution.UnsupportedPlatform}
	}
	exe, err := os.Executable()
	if err != nil {
		return distribution.Result{State: distribution.Corrupt, Reason: err}
	}
	return distribution.Inspect(exe, version.String(), runtime.GOARCH, version.CEFPublicationSHA256)
}

func embeddedExecutable() string {
	if embeddedDevelopmentRoot() != "" {
		return os.Getenv("DSH_WORK_CEF_EXECUTABLE")
	}
	result := embeddedPublication()
	if result.State != distribution.Ready {
		return ""
	}
	return result.Executable
}

func embeddedRuntimeReady() bool { return embeddedExecutable() != "" }

func embeddedExpectedUserData() string {
	if root := embeddedDevelopmentRoot(); root != "" {
		return filepath.Join(root, "data")
	}
	return ""
}
