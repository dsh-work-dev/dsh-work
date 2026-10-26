package desktopapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/local/dsh-work/internal/app"
	"github.com/local/dsh-work/internal/daemon"
	"github.com/local/dsh-work/internal/lifecycle"
	"github.com/local/dsh-work/internal/logging"
	"github.com/local/dsh-work/internal/maintenance"
	"github.com/local/dsh-work/internal/platform"
	"github.com/local/dsh-work/internal/storagepaths"
)

type Resources struct {
	Assets                                            fs.FS
	AppIcon, TrayIcon, TrayDarkIcon, TrayTemplateIcon []byte
}

func Run(resources Resources) {
	proc := logging.ProcessUI
	if len(os.Args) > 1 && os.Args[1] == "--daemon" {
		proc = logging.ProcessDaemon
	}
	logging.Init(proc, logRoot())
	defer logging.Close()
	if maintenance.InstallerInProgress() {
		if len(os.Args) < 2 || os.Args[1] != "--daemon" {
			maintenance.ShowInstallerBusy()
		}
		logging.Fatal(logging.Module(logging.ModApp), "installer maintenance in progress")
	}
	identity := filepath.Dir(app.DefaultConfig(currentDiscoveryRoot()).SettingsPath)
	if root := os.Getenv("DSH_WORK_DESKTOP_ROOT"); root != "" && os.Getenv("DSH_WORK_DESKTOP_REPORT") != "" {
		identity = root
	}
	if len(os.Args) > 1 && os.Args[1] == "--daemon" {
		runDaemon(identity, resources)
		return
	}
	if err := runDesktopClient(identity, resources); err != nil {
		var failure lifecycle.Failure
		if errors.As(err, &failure) && failure.Code == lifecycle.ErrorManagerOperationBusy {
			maintenance.ShowInstallerBusy()
		}
		logging.Fatal(logging.Module(logging.ModApp), "desktop client failed", logging.ErrAttrs(err, "")...)
	}
}

func logRoot() string {
	if root := os.Getenv("DSH_WORK_DESKTOP_ROOT"); root != "" && os.Getenv("DSH_WORK_DESKTOP_REPORT") != "" {
		return filepath.Join(root, "logs")
	}
	paths, err := storagepaths.DefaultDirectories()
	if err != nil {
		return ""
	}
	return paths.LogRoot
}

func ensureDaemon(ctx context.Context, identity string) (*daemon.Client, daemon.Snapshot, error) {
	if maintenance.InstallerInProgress() {
		return nil, daemon.Snapshot{}, lifecycle.Failure{
			Code:      lifecycle.ErrorManagerOperationBusy,
			Summary:   "DSH Work is being updated.",
			Retryable: true,
			Detail:    "Wait for the installer to finish, then reopen DSH Work.",
		}
	}
	client := daemon.NewClient(identity)
	var state daemon.Snapshot
	probe := func() error {
		short, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		return client.JSON(short, "/snapshot", nil, &state)
	}
	if probe() == nil {
		if state.Protocol != daemon.Protocol {
			return nil, state, errors.New("background protocol mismatch; stop background and reopen")
		}
		return client, state, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, state, err
	}
	cmd := exec.Command(exe, "--daemon")
	daemon.Detach(cmd)
	if err := cmd.Start(); err != nil {
		return nil, state, err
	}
	go func() { _ = cmd.Wait() }()
	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			client.Close()
			return nil, state, fmt.Errorf("background did not become available: %w", ctx.Err())
		case <-ticker.C:
			if probe() == nil {
				if state.Protocol != daemon.Protocol {
					return nil, state, errors.New("background protocol mismatch")
				}
				return client, state, nil
			}
		}
	}
}

type uiLauncher struct {
	identity                  string
	allowForegroundActivation func()
	mu                        sync.Mutex
}

type uiOpenRequest struct {
	Section string `json:"section"`
	Window  string `json:"window"`
}

type uiOpenResult struct {
	Window string `json:"window"`
}

func newUILauncher(identity string) *uiLauncher {
	return &uiLauncher{identity: identity, allowForegroundActivation: platform.AllowForegroundActivation}
}
func (l *uiLauncher) Open(section, window string) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if window == "" {
		window = "workspace"
	}
	if l.allowForegroundActivation != nil {
		l.allowForegroundActivation()
	}
	client := daemon.NewClient(l.identity + "-ui")
	defer client.Close()
	open := func() (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		var result uiOpenResult
		if err := client.JSON(ctx, "/open", uiOpenRequest{Section: section, Window: window}, &result); err != nil {
			return "", err
		}
		if result.Window == "" {
			return "", errors.New("desktop window is unavailable")
		}
		return result.Window, nil
	}
	if actual, err := open(); err == nil {
		return actual, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	cmd := exec.Command(exe, "--ui", section, "--window", window)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	daemon.Detach(cmd)
	if err := cmd.Start(); err != nil {
		return "", err
	}
	go func() { _ = cmd.Wait() }()
	// The UI singleton listener, rather than process ancestry, arbitrates
	// concurrent tray clicks and normal application launches.
	deadline := time.NewTimer(20 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-deadline.C:
			return "", errors.New("desktop did not open")
		case <-ticker.C:
			if actual, err := open(); err == nil {
				return actual, nil
			}
		}
	}
}
func (l *uiLauncher) Stop() error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c := daemon.NewClient(l.identity + "-ui")
	defer c.Close()
	var status struct{ PID int }
	if err := c.JSON(ctx, "/status", nil, &status); err != nil {
		// No UI listener means there is no UI process holding the installed
		// executable. Treat that as an already-stopped client.
		return nil
	}
	if err := c.JSON(ctx, "/stop", nil, nil); err != nil {
		return err
	}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := c.JSON(ctx, "/status", nil, &status); err != nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func uiHandler(open func(section, window string) string, closeWindow func(string), stop func()) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != "" {
			http.Error(w, "forbidden", 403)
			return
		}
		switch r.URL.Path {
		case "/open", "/close":
			if r.URL.Path == "/open" {
				var request uiOpenRequest
				if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&request) != nil {
					http.Error(w, "invalid open request", 400)
					return
				}
				_ = json.NewEncoder(w).Encode(uiOpenResult{Window: open(request.Section, request.Window)})
			} else {
				var section string
				if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&section) != nil {
					http.Error(w, "invalid section", 400)
					return
				}
				go closeWindow(section)
			}
		case "/status":
			_ = json.NewEncoder(w).Encode(struct{ PID int }{os.Getpid()})
			return
		case "/stop":
			go stop()
		default:
			http.NotFound(w, r)
			return
		}
		if r.URL.Path != "/open" {
			_, _ = w.Write([]byte("true"))
		}
	})
}
