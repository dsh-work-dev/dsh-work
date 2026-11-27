//go:build darwin && arm64 && cgo && e67nativefixture

package desktopapp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/automation/computerruntime"
	"github.com/local/dsh-work/internal/embeddedbrowser/macowner"
	"github.com/local/dsh-work/internal/platform"
	"github.com/local/dsh-work/internal/settings"
	"github.com/local/dsh-work/internal/supervisor"
)

type fixtureAUVSupervisor struct {
	supervisor.Adapter
	worker supervisor.Worker
}

func (s *fixtureAUVSupervisor) Start(ctx context.Context, plan supervisor.LaunchPlan, output supervisor.RawOutputHandler) (supervisor.Worker, error) {
	var err error
	s.worker, err = s.Adapter.Start(ctx, plan, output)
	return s.worker, err
}

func TestLiveMacComputerAUV(t *testing.T) {
	cache, manifest := os.Getenv("DSH_WORK_AUV_TEST_CACHE"), os.Getenv("DSH_WORK_CEF_MAC_PUBLICATION")
	bundledHost := os.Getenv("DSH_WORK_TEST_DMG_EXECUTABLE")
	if !filepath.IsAbs(cache) || (!filepath.IsAbs(manifest) && !filepath.IsAbs(bundledHost)) {
		t.Skip("requires an isolated published AUV component and a packaged app or standalone Mac owner")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 65*time.Second)
	defer cancel()
	var publication macowner.Publication
	var err error
	if bundledHost != "" {
		publication, err = macowner.OpenInstalledPublication(ctx, bundledHost)
	} else {
		publication, err = macowner.OpenPublication(ctx, manifest)
	}
	if err != nil {
		t.Fatal(err)
	}
	deps, err := automation.PublishedPackage(cache, automation.AUV)
	if err != nil {
		t.Fatal(err)
	}
	driver, err := automation.AUVExecutable(deps)
	if err != nil {
		t.Fatal(err)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	path := t.TempDir()
	pid, window, clicks := openAUVButtonFixture(t, ctx, path)
	manager, err := settings.New(settings.Config{Path: filepath.Join(path, "settings.json")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.SetComputerEngine(ctx, "auv"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.SetAutomationEnabled(ctx, "computer", true); err != nil {
		t.Fatal(err)
	}
	service := automation.New(manager, platform.NewAutomationPreparer(cache))
	defer service.Close()
	owner := computerRuntimeSupervisor(nil, cache).(*macComputerSupervisor)
	owner.publication = func() (macowner.Publication, error) { return publication, nil }
	probe := &fixtureAUVSupervisor{Adapter: owner}
	service.ConfigureComputerGateway(automation.ComputerGatewayConfig{
		Inspect: automation.InspectComputerTarget, Acquire: automation.AcquireComputerDesktopLease,
		SupportsEngine: computerEngineSupported,
		Factory: computerruntime.NewFactory(computerruntime.Config{
			Supervisor: probe, Root: computerRuntimeRoot(cache),
			Resolve: func(context.Context, string) (computerruntime.Runtime, error) {
				return computerruntime.Runtime{Node: node, Dependencies: deps, Driver: driver}, nil
			},
		}),
	})
	service.Worker("fixture-worker", true, true)
	service.Gateway("fixture-worker", true)
	state := service.Snapshot()
	if state.Computer.Availability != "available" || len(state.ComputerTools) != 5 {
		t.Fatalf("AUV tools not advertised: %+v tools=%d", state.Computer, len(state.ComputerTools))
	}
	target, err := automation.InspectComputerTarget(pid, window)
	if err != nil || target.Title != "DSH Work AUV button fixture" || target.Level != "full" {
		t.Fatalf("fixture is not an eligible independent macOS window: %+v %v", target, err)
	}

	observe := automation.Request{Generation: state.Generation, Epoch: state.Computer.Epoch, Capability: automation.Computer,
		Session: "button-fixture", Lifetime: "fixture-life", Turn: "deny-turn", UserTurn: true, Operation: "computer",
		Tool: "computer_observe", Arguments: map[string]any{"pid": float64(pid), "window_id": float64(window)}}
	if _, err := service.ComputerTurn(observe); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Execute(ctx, observe); err == nil || !strings.Contains(err.Error(), "COMPUTER_AUTHORIZATION_REQUIRED") {
		t.Fatalf("unapproved observation reached execution: %v", err)
	}
	unapproved := observe
	unapproved.Tool = "computer_click"
	unapproved.Arguments = map[string]any{"pid": float64(pid), "window_id": float64(window), "x": float64(160), "y": float64(100), "snapshot_id": "unapproved"}
	if _, err := service.Execute(ctx, unapproved); err == nil || !strings.Contains(err.Error(), "COMPUTER_AUTHORIZATION_REQUIRED") {
		t.Fatalf("unapproved input reached execution: %v", err)
	}
	if _, err := answerAUVFixtureConsent(t, ctx, service, observe, "deny"); err == nil || !strings.Contains(err.Error(), "COMPUTER_APP_DENIED") || clicks(t) != 0 {
		t.Fatalf("denied app did not stay untouched: %v", err)
	}
	t.Log("Unapproved click and denied application access rejected, button count 0")

	observe.Turn = "allow-turn"
	if _, err := service.ComputerTurn(observe); err != nil {
		t.Fatal(err)
	}
	decision, err := answerAUVFixtureConsent(t, ctx, service, observe, "allow")
	if err != nil || decision.Kind != "allow" {
		current, inspectErr := automation.InspectComputerTarget(pid, window)
		t.Fatalf("approved app: %+v %v, initial target %+v, current %+v %v", decision, err, target, current, inspectErr)
	}
	if release, err := automation.AcquireComputerDesktopLease(); release != nil || err == nil || !strings.Contains(err.Error(), "COMPUTER_BUSY") {
		if release != nil {
			_ = release()
		}
		t.Fatalf("gateway did not own the desktop lease: %v", err)
	}
	approved := observe
	approved.Permit = decision.Permit
	decision, err = service.AuthorizeComputer(ctx, observe)
	if err != nil || decision.Kind != "allow" {
		t.Fatalf("spare permit before Stop: %+v %v", decision, err)
	}
	sparePermit := decision.Permit

	t.Run("approved background click", func(t *testing.T) {
		value, err := service.Execute(ctx, approved)
		if err != nil {
			if probe.worker != nil {
				t.Logf("AUV worker: %+v", probe.worker.Diagnostics())
			}
			t.Fatal("approved window capture:", err)
		}
		content := value.(map[string]any)["structuredContent"].(map[string]any)
		click := observe
		click.Permit = ""
		click.Tool = "computer_click"
		click.Arguments = map[string]any{"pid": float64(pid), "window_id": float64(window),
			"snapshot_id": content["snapshot_id"], "x": content["screenshot_width"].(float64) / 2,
			"y": content["screenshot_height"].(float64) / 2}
		decision, err := service.AuthorizeComputer(ctx, click)
		if err != nil || decision.Kind != "ask" || clicks(t) != 0 {
			t.Fatalf("click should request explicit approval: %+v %v", decision, err)
		}
		// The isolated harness accepts the action confirmation from the tool gateway.
		click.Permit = decision.Permit
		receipt, err := service.Execute(ctx, click)
		if err != nil {
			t.Fatal("approved background click:", err)
		}
		deadline := time.Now().Add(3 * time.Second)
		for clicks(t) == 0 && time.Now().Before(deadline) {
			time.Sleep(20 * time.Millisecond)
		}
		if clicks(t) != 1 {
			t.Fatalf("independent AppKit button callback recorded %d clicks, delivery receipt: %+v", clicks(t), receipt)
		}
		t.Log("Approved screenshot and PID-targeted click verified by AppKit callback, button count 1")
	})
	if err := service.StopComputerUse(observe.Session, observe.Lifetime); err != nil {
		t.Fatal(err)
	}
	observe.Permit = sparePermit
	if _, err := service.Execute(ctx, observe); !errors.Is(err, automation.ErrRevoked) {
		t.Fatalf("stopped authorization remained executable: %v", err)
	}
	release, err := automation.AcquireComputerDesktopLease()
	if err != nil {
		t.Fatal("Stop left the desktop lease held:", err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	t.Log("Stop released the desktop lease and revoked an unused prior permit")
}

func answerAUVFixtureConsent(t *testing.T, ctx context.Context, s *automation.Service, request automation.Request, choice string) (automation.BrowserDecision, error) {
	t.Helper()
	type result struct {
		decision automation.BrowserDecision
		err      error
	}
	done := make(chan result, 1)
	go func() {
		decision, err := s.AuthorizeComputer(ctx, request)
		done <- result{decision, err}
	}()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case outcome := <-done:
			return outcome.decision, outcome.err
		case <-ctx.Done():
			t.Fatal("no application consent request", ctx.Err())
		case <-ticker.C:
			if pending := s.Snapshot().ComputerAccess.Requests; len(pending) == 1 {
				if _, err := s.AnswerComputerConsent(ctx, pending[0].ID, choice); err != nil {
					t.Fatal(err)
				}
				outcome := <-done
				return outcome.decision, outcome.err
			}
		}
	}
}

func openAUVButtonFixture(t *testing.T, ctx context.Context, dir string) (uint32, uint64, func(*testing.T) int) {
	t.Helper()
	app := filepath.Join(dir, "AUV Button Fixture.app")
	executable := filepath.Join(app, "Contents", "MacOS", "AUVButtonFixture")
	if err := os.MkdirAll(filepath.Dir(executable), 0700); err != nil {
		t.Fatal(err)
	}
	plist := `<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>dev.dsh-work.fixture.auv-button</string>
<key>CFBundleExecutable</key><string>AUVButtonFixture</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>LSUIElement</key><true/></dict></plist>`
	if err := os.WriteFile(filepath.Join(app, "Contents", "Info.plist"), []byte(plist), 0600); err != nil {
		t.Fatal(err)
	}
	source := "../automation/testdata/computer-button-fixture.m"
	if output, err := exec.CommandContext(ctx, "clang", "-fobjc-arc", "-framework", "AppKit", source, "-o", executable).CombinedOutput(); err != nil {
		t.Fatalf("compile the AppKit fixture: %v %s", err, output)
	}
	witness := filepath.Join(dir, "button.txt")
	t.Cleanup(func() {
		_ = os.WriteFile(witness+".stop", nil, 0600)
		time.Sleep(300 * time.Millisecond)
	})
	if output, err := exec.CommandContext(ctx, "/usr/bin/open", "-g", "-n", "-a", app, "--args", witness).CombinedOutput(); err != nil {
		t.Fatalf("start the independent AppKit fixture: %v %s", err, output)
	}
	var pid uint32
	var window uint64
	var count int
	read := func() error {
		data, err := os.ReadFile(witness)
		if err != nil {
			return err
		}
		_, err = fmt.Sscan(string(data), &pid, &window, &count)
		return err
	}
	for read() != nil || pid == 0 || window == 0 {
		if ctx.Err() != nil {
			t.Fatal("AppKit fixture did not publish a window", ctx.Err())
		}
		time.Sleep(25 * time.Millisecond)
	}
	return pid, window, func(t *testing.T) int {
		t.Helper()
		if err := read(); err != nil {
			t.Fatal(err)
		}
		return count
	}
}
