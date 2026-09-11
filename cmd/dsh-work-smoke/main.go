//go:build windows

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	dshworkapp "github.com/local/dsh-work/internal/app"
	"github.com/local/dsh-work/internal/dshadapter"
	"github.com/local/dsh-work/internal/dshmanager"
	"github.com/local/dsh-work/internal/lifecycle"
	"github.com/local/dsh-work/internal/platform"
	"github.com/local/dsh-work/internal/workerchannel"
	"github.com/local/dsh-work/internal/workeripc"
)

// dsh-work-smoke exercises the same Host, DSH Adapter and native Windows
// Supervisor used by the desktop composition root. It never substitutes a
// fake worker, server or readiness signal.
func main() {
	root, err := os.Getwd()
	if err != nil {
		fatalf("resolve repository root: %v", err)
	}
	dependencies := platform.New()
	if dependencies.Err != nil {
		fatalf("native platform dependencies unavailable: %v", dependencies.Err)
	}
	config := dshworkapp.DefaultConfig(root)
	// Keep the smoke data directory under ignored generated state so a second run tests
	// the normal persistent data-directory path instead of copying the preview profile
	// tree from scratch every time. It remains dsh-work-owned and never touches
	// the user's default ~/.dsh.
	config.DSHDataDirectory = filepath.Join(root, ".task", "dsh-smoke-data")
	config.BootstrapDirectory = filepath.Join(root, ".task", "dsh-smoke-bootstrap")
	// A fresh dsh-work-owned DSH data directory may materialise the pinned profile tree on
	// first launch. Keep the smoke deadline bounded but long enough to cover
	// that real initialization rather than turning cold-start latency into a
	// false readiness failure.
	config.ReadinessTimeout = 180 * time.Second
	config.ProbeTimeout = 2 * time.Second
	config.GracefulStopTimeout = 8 * time.Second
	config.ForceStopTimeout = 8 * time.Second
	config.EmptyTimeout = 8 * time.Second
	config.ShutdownTimeout = 25 * time.Second

	dsh := dshadapter.New(dependencies.CommandExecutor, config.ExpectedDSHVersion)
	dsh.SetDiscoveryRoot(root)
	runtimeHint := dsh.RuntimeHint()
	manager, err := dshmanager.New(dshmanager.Config{
		StatePath:         filepath.Join(root, ".task", "dsh-smoke-manager.json"),
		PluginCommands:    dshadapter.NewPluginCommands(),
		NodeResolver:      platform.NewNodeResolver(filepath.Join(root, ".task", "runtimes")),
		RuntimeVerifier:   dsh,
		ProfileCatalog:    dsh,
		DataDirectories:   []dshmanager.DataDirectoryInfo{{ID: "dsh-work", Name: "dsh-work smoke data directory", Path: config.DSHDataDirectory, Ownership: dshmanager.DataDirectoryOwnershipDSHWork}},
		Runtimes:          []dshmanager.RuntimeInfo{{ID: "dsh-" + runtimeHint.Version, Version: runtimeHint.Version, Path: runtimeHint.Path, Source: dshmanager.RuntimeSourceDevelopmentFixture, Installed: true}},
		DefaultRunContext: dshmanager.RunContext{RuntimeID: "dsh-" + runtimeHint.Version, Node: dshmanager.NodeSelection{Kind: dshmanager.NodeSelectionSystem}, Profile: dshmanager.ProfileRef{DataDirectoryID: "dsh-work", Name: "web"}},
	})
	if err != nil {
		fatalf("create smoke launch manager: %v", err)
	}
	channel := workerchannel.New()
	host := dshworkapp.NewHost(dshworkapp.Dependencies{
		DSH:        dsh,
		Manager:    manager,
		Supervisor: dependencies.Supervisor,
		Channel:    channel,
	}, config)
	statuses := make(chan lifecycle.Status, 32)
	var workspaceURL string
	host.SetPublish(func(status lifecycle.Status) {
		encoded, _ := json.Marshal(status)
		fmt.Println(string(encoded))
		statuses <- status
	})
	host.SetReadyHandler(func(url string) { workspaceURL = url })
	host.SetDebug(func(message string) { fmt.Printf("DEBUG %s\n", message) })

	start := host.Start()
	if start.State != lifecycle.StateStarting {
		fatalf("host did not enter Starting: %+v", start)
	}
	ready := waitForState(statuses, lifecycle.StateReady, config.ReadinessTimeout+config.GracefulStopTimeout+config.EmptyTimeout+10*time.Second)
	if ready.Error != nil || ready.WorkspaceURL == "" || workspaceURL == "" {
		diagnostics := host.Diagnostics()
		_ = host.ShutdownForApp()
		fatalf("host did not become ready: %+v diagnostics=%+v", ready, diagnostics)
	}
	if err := probeWorkspace(channel.Current().Client(), workeripc.Origin+"/"); err != nil {
		_ = host.ShutdownForApp()
		fatalf("workspace probe failed after readiness: %v", err)
	}

	if status := host.Cancel(); status.State != lifecycle.StateStopping {
		fatalf("host did not enter Stopping: %+v", status)
	}
	stopped := waitForState(statuses, lifecycle.StateStopped, config.ShutdownTimeout)
	if stopped.Error != nil || stopped.State != lifecycle.StateStopped {
		fatalf("host did not stop cleanly: %+v", stopped)
	}
	if err := host.ShutdownForApp(); err != nil {
		fatalf("second shutdown was not idempotent: %v", err)
	}
	diagnostics := host.Diagnostics()
	if diagnostics.ActiveProcesses != 0 {
		fatalf("managed process boundary is not empty: %+v", diagnostics)
	}
	fmt.Printf("REAL_DSH_SMOKE_OK url=%s pid=%d activeProcesses=%d\n", ready.WorkspaceURL, diagnostics.PID, diagnostics.ActiveProcesses)
}

func waitForState(statuses <-chan lifecycle.Status, expected lifecycle.State, timeout time.Duration) lifecycle.Status {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case status := <-statuses:
			if status.State == expected || status.State == lifecycle.StateFailed {
				return status
			}
		case <-timer.C:
			return lifecycle.Status{
				State: lifecycle.StateFailed,
				Error: &lifecycle.Failure{Code: lifecycle.ErrorDSHReadinessTimeout, Summary: "smoke test timed out"},
			}
		}
	}
}

func probeWorkspace(client *http.Client, value string) error {
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, value, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("status %s", response.Status)
	}
	_, err = io.Copy(io.Discard, io.LimitReader(response.Body, 64*1024))
	return err
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
