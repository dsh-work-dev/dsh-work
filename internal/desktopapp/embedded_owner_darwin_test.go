//go:build darwin && cgo && e67presenter && !production && !server

package desktopapp

import (
	"context"
	"errors"
	"github.com/local/dsh-work/internal/supervisor"
	"testing"
)

type macLegacyBrowserAdapter struct{ called bool }

var macLegacyBrowserError = errors.New("legacy external browser supervisor")

func (a *macLegacyBrowserAdapter) Start(context.Context, supervisor.LaunchPlan, supervisor.RawOutputHandler) (supervisor.Worker, error) {
	a.called = true
	return nil, macLegacyBrowserError
}
func TestMacBrowserSupervisorPreservesExternalOutsidePreview(t *testing.T) {
	t.Setenv("DSH_WORK_EMBEDDED_CHECKPOINT", "")
	t.Setenv("DSH_WORK_EMBEDDED_ROOT", "")
	t.Setenv("DSH_WORK_CEF_MAC_PUBLICATION", "/missing/not-required-outside-preview.json")
	base := &macLegacyBrowserAdapter{}
	actual := embeddedBrowserSupervisor(base, t.TempDir())
	if actual != base {
		t.Fatal("existing browser caller acquired a CEF precondition")
	}
	if _, err := actual.Start(context.Background(), supervisor.LaunchPlan{Executable: "/legacy-node"}, nil); !errors.Is(err, macLegacyBrowserError) || !base.called {
		t.Fatal("legacy caller changed", err)
	}
	t.Setenv("DSH_WORK_EMBEDDED_CHECKPOINT", "1")
	t.Setenv("DSH_WORK_EMBEDDED_ROOT", t.TempDir())
	if _, ok := embeddedBrowserSupervisor(base, t.TempDir()).(*macBrowserComponentSupervisor); !ok {
		t.Fatal("preview did not select its fixed browser component owner")
	}
}
