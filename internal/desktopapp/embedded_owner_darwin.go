//go:build darwin && cgo

package desktopapp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/embeddedbrowser"
	"github.com/local/dsh-work/internal/embeddedbrowser/distribution"
	"github.com/local/dsh-work/internal/embeddedbrowser/macowner"
	"github.com/local/dsh-work/internal/supervisor"
)

var macPublication = struct {
	sync.Once
	value macowner.Publication
	err   error
}{}

func ownedMacPublication() (macowner.Publication, error) {
	macPublication.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		var previewManifest string
		if embeddedDevelopmentRoot() != "" {
			previewManifest = os.Getenv("DSH_WORK_CEF_MAC_PUBLICATION")
		}
		macPublication.value, macPublication.err = macowner.OpenHostPublication(ctx, previewManifest)
	})
	return macPublication.value, macPublication.err
}
func inspectDarwinPublication() distribution.Result {
	publication, err := ownedMacPublication()
	switch {
	case err == nil:
		return distribution.Result{State: distribution.Ready, Executable: publication.Executable}
	case errors.Is(err, os.ErrNotExist):
		return distribution.Result{State: distribution.Missing, Reason: err}
	case errors.Is(err, macowner.ErrUnsupportedPlatform):
		return distribution.Result{State: distribution.UnsupportedPlatform, Reason: err}
	default:
		return distribution.Result{State: distribution.Corrupt, Reason: err}
	}
}
func embeddedPlatformAvailable() bool { return embeddedRuntimeReady() }
func embeddedDevelopmentExecutable() string {
	publication, err := ownedMacPublication()
	if err != nil {
		return ""
	}
	return publication.Executable
}
func startEmbeddedBroker(ctx context.Context, c embeddedbrowser.Config) (*embeddedbrowser.Broker, error) {
	publication, err := ownedMacPublication()
	if err != nil {
		return nil, err
	}
	if c.Executable != "" && filepath.Clean(c.Executable) != filepath.Clean(publication.Executable) {
		return nil, errors.New("MAC_CEF_PUBLICATION_EXECUTABLE_MISMATCH")
	}
	owner, err := macowner.New(publication, c.Profile)
	if err != nil {
		return nil, err
	}
	c.Executable = publication.Executable
	c.Supervisor = owner
	if c.DownloadConsent == nil {
		c.DownloadConsent = macPreviewDownloadConsent()
	}
	broker, err := embeddedbrowser.Start(ctx, c)
	if err != nil {
		return nil, err
	}
	if err := macPreviewRecordBroker(owner); err != nil {
		return nil, errors.Join(err, broker.Close())
	}
	return broker, nil
}

type macBrowserComponentSupervisor struct{ cache string }

func computerEngineSupported(engine string) bool {
	return runtime.GOARCH == "arm64" && engine == "auv"
}

type macComputerSupervisor struct {
	cache       string
	publication func() (macowner.Publication, error)
}

func computerRuntimeSupervisor(_ supervisor.Adapter, cache string) supervisor.Adapter {
	return &macComputerSupervisor{cache: cache, publication: ownedMacPublication}
}

func (s *macComputerSupervisor) Start(ctx context.Context, plan supervisor.LaunchPlan, raw supervisor.RawOutputHandler) (supervisor.Worker, error) {
	publication, err := s.publication()
	if err != nil {
		return nil, err
	}
	dependencies, err := automation.PublishedPackage(s.cache, automation.AUV)
	if err != nil {
		return nil, err
	}
	owner, err := macowner.NewAUVAdapter(ctx, publication, plan.Executable, dependencies, computerRuntimeRoot(s.cache))
	if err != nil {
		return nil, err
	}
	return owner.Start(ctx, plan, raw)
}

func embeddedBrowserSupervisor(base supervisor.Adapter, cache string) supervisor.Adapter {
	if embeddedDevelopmentRoot() == "" && !embeddedRuntimeReady() {
		return base
	}
	return &macBrowserComponentSupervisor{cache}
}
func (s *macBrowserComponentSupervisor) Start(ctx context.Context, plan supervisor.LaunchPlan, raw supervisor.RawOutputHandler) (supervisor.Worker, error) {
	publication, err := ownedMacPublication()
	if err != nil {
		return nil, err
	}
	dependencies, err := automation.PublishedPackage(s.cache, automation.Browser)
	if err != nil {
		return nil, err
	}
	owner, err := macowner.NewMCPAdapter(publication, plan.Executable, dependencies, filepath.Join(s.cache, "automation", "browser-sessions"))
	if err != nil {
		return nil, err
	}
	return owner.Start(ctx, plan, raw)
}
