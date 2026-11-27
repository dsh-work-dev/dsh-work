//go:build !windows

package platform

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/settings"
)

func TestNonWindowsComputerBackendRemainsUnavailable(t *testing.T) {
	if target, err := automation.InspectComputerTarget(0, 0); err == nil || target != (automation.ComputerTarget{}) {
		t.Fatalf("invalid native target must be rejected: %+v, %v", target, err)
	}
	for _, engine := range []string{"cua-driver", "auv"} {
		t.Run(engine, func(t *testing.T) {
			preparer := NewAutomationPreparer(t.TempDir())
			if preparer != nil && (runtime.GOOS != "darwin" || runtime.GOARCH != "arm64") {
				if !errors.Is(preparer.Prepare(context.Background(), automation.Computer, func(automation.Progress) {}), automation.ErrUnavailable) {
					t.Fatal("unsupported platform installed a computer component")
				}
			}
			manager, err := settings.New(settings.Config{Path: filepath.Join(t.TempDir(), "settings.json")})
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			if _, err := manager.SetComputerEngine(ctx, engine); err != nil {
				t.Fatal(err)
			}
			s := automation.New(manager, preparer)
			t.Cleanup(s.Close)
			factoryCalled, leaseCalled, inspectCalled := false, false, false
			s.ConfigureComputerGateway(automation.ComputerGatewayConfig{
				Inspect: func(pid uint32, window uint64) (automation.ComputerTarget, error) {
					inspectCalled = true
					return automation.InspectComputerTarget(pid, window)
				},
				Acquire: func() (func() error, error) {
					leaseCalled = true
					return nil, errors.New("unexpected desktop lease acquisition")
				},
				Factory: func(context.Context, string) (automation.Resource, error) {
					factoryCalled = true
					return nil, errors.New("unexpected backend launch")
				},
			})
			s.Worker("generation", true, true)
			s.Gateway("generation", true)
			if _, err := s.SetEnabled(ctx, automation.Computer, true); err != nil {
				t.Fatal(err)
			}
			v := s.InspectInstalled()
			if v.Computer.Availability != "unavailable" || v.Computer.Dependency == "prepared" || len(v.ComputerTools) != 0 {
				t.Fatalf("unsupported backend was advertised: %+v", v.Computer)
			}
			r := automation.Request{Generation: "generation", Epoch: v.Computer.Epoch, Capability: automation.Computer, Session: "session", Lifetime: "life", Turn: "turn", Operation: "computer", Tool: "computer_observe", Arguments: map[string]any{"pid": float64(1), "window_id": float64(1)}}
			if decision, err := s.AuthorizeComputer(ctx, r); !errors.Is(err, automation.ErrRevoked) || decision.Permit != "" {
				t.Fatalf("unsupported backend authorized dispatch: %+v, %v", decision, err)
			}
			if factoryCalled || leaseCalled || inspectCalled || s.Snapshot().ComputerAccess.Use != nil {
				t.Fatal("unsupported backend started or retained desktop access")
			}
		})
	}
}
