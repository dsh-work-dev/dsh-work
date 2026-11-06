//go:build !windows

package platform

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/settings"
)

func TestNonWindowsComputerEntryPointsRemainUnavailable(t *testing.T) {
	if target, err := automation.InspectComputerTarget(1, 1); !errors.Is(err, automation.ErrUnavailable) || target != (automation.ComputerTarget{}) {
		t.Fatalf("native target inspection must be unavailable: %+v, %v", target, err)
	}
	if release, err := automation.AcquireComputerDesktopLease(); !errors.Is(err, automation.ErrUnavailable) || release != nil {
		t.Fatalf("native desktop lease must be unavailable: %v", err)
	}
	for _, engine := range []string{"cua-driver", "auv"} {
		t.Run(engine, func(t *testing.T) {
			preparer := NewAutomationPreparer(t.TempDir())
			if preparer != nil {
				t.Fatal("non-Windows must not install a computer backend")
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
			factoryCalled := false
			s.ConfigureComputerGateway(automation.ComputerGatewayConfig{
				Inspect: automation.InspectComputerTarget,
				Acquire: automation.AcquireComputerDesktopLease,
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
			if factoryCalled || s.Snapshot().ComputerAccess.Use != nil {
				t.Fatal("unsupported backend started or retained desktop access")
			}
		})
	}
}
