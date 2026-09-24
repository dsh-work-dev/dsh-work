package app

import (
	"context"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/lifecycle"
)

func TestWebBootMustCompleteBeforeSuccessPoint(t *testing.T) {
	for _, outcome := range []string{"success", "plugin failure", "timeout", "cancel"} {
		t.Run(outcome, func(t *testing.T) {
			f := newRunContextSwitchFixture(t)
			defer f.dsh.server.Close()
			f.host.config.RequireWebBoot = true
			f.host.config.ReadinessTimeout = time.Second
			started := f.host.Start()
			defer f.host.Cancel()
			// HTTP readiness must leave the generation in Starting and no success point.
			time.Sleep(100 * time.Millisecond)
			if status := f.host.Status(); status.State != lifecycle.StateStarting {
				t.Fatalf("HTTP shell incorrectly completed startup: %+v", status)
			}
			snapshot, err := f.manager.Snapshot(context.Background())
			if err != nil || snapshot.RestorePoints.LastRunning != "" {
				t.Fatalf("HTTP shell recorded success: %+v, %v", snapshot.RestorePoints, err)
			}
			if err := f.host.ReportWebBoot("old-generation", ""); err == nil {
				t.Fatal("accepted old WebView result")
			}
			switch outcome {
			case "success", "plugin failure":
				detail := ""
				if outcome == "plugin failure" {
					detail = "web boot: 1 entry did not activate dsh-just-chat: pending (waiting for service: settingsScope)"
				}
				if err := f.host.ReportWebBoot(started.GenerationID, detail); err != nil {
					t.Fatal(err)
				}
				if outcome == "success" {
					f.waitForHostState(t, lifecycle.StateReady)
					snapshot, err = f.manager.Snapshot(context.Background())
					if err != nil || snapshot.RestorePoints.LastRunning == "" {
						t.Fatalf("mount did not record success: %v", err)
					}
					return
				}
				// A later success cannot override the first terminal failure.
				_ = f.host.ReportWebBoot(started.GenerationID, "")
			case "cancel":
				f.host.Cancel()
				f.waitForHostState(t, lifecycle.StateStopped)
			}
			if outcome != "cancel" {
				failed := f.waitForHostState(t, lifecycle.StateFailed)
				if failed.Error == nil {
					t.Fatal("missing web boot failure")
				}
			}
			snapshot, err = f.manager.Snapshot(context.Background())
			if err != nil || snapshot.RestorePoints.LastRunning != "" {
				t.Fatalf("failed boot recorded success: %+v %v", snapshot.RestorePoints, err)
			}
		})
	}
}
