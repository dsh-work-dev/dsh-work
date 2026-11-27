//go:build darwin && cgo

package desktopapp

import (
	"context"
	"errors"
	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/embeddedbrowser"
	"github.com/local/dsh-work/internal/embeddedbrowser/macowner"
	"github.com/local/dsh-work/internal/supervisor"
	"os"
	"path/filepath"
	"sync"
	"time"
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
		macPublication.value, macPublication.err = macowner.OpenPublication(ctx, os.Getenv("DSH_WORK_CEF_MAC_PUBLICATION"))
	})
	return macPublication.value, macPublication.err
}
func embeddedPlatformAvailable() bool {
	if embeddedDevelopmentRoot() == "" {
		return false
	}
	_, err := ownedMacPublication()
	return err == nil
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
	return embeddedbrowser.Start(ctx, c)
}

type macBrowserComponentSupervisor struct{ cache string }

func embeddedBrowserSupervisor(base supervisor.Adapter, cache string) supervisor.Adapter {
	if embeddedDevelopmentRoot() == "" {
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
