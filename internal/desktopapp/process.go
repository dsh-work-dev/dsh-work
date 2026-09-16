package desktopapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/local/dsh-work/internal/app"
	"github.com/local/dsh-work/internal/daemon"
	"github.com/local/dsh-work/internal/lifecycle"
	"github.com/local/dsh-work/internal/maintenance"
)

type Resources struct {
	Assets                                            fs.FS
	AppIcon, TrayIcon, TrayDarkIcon, TrayTemplateIcon []byte
}

func Run(resources Resources) {
	if maintenance.InstallerInProgress() {
		if len(os.Args) < 2 || os.Args[1] != "--daemon" {
			maintenance.ShowInstallerBusy()
		}
		log.Printf("dsh-work installer is performing maintenance; reopen after it finishes")
		os.Exit(1)
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
		log.Printf("desktop: %v", err)
		os.Exit(1)
	}
}

func ensureDaemon(ctx context.Context, identity string) (*daemon.Client, daemon.Snapshot, error) {
	if maintenance.InstallerInProgress() {
		return nil, daemon.Snapshot{}, lifecycle.Failure{
			Code:      lifecycle.ErrorManagerOperationBusy,
			Summary:   "dsh-work is being updated.",
			Retryable: true,
			Detail:    "Wait for the installer to finish, then reopen dsh-work.",
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
	identity string
	mu       sync.Mutex
}

func newUILauncher(identity string) *uiLauncher { return &uiLauncher{identity: identity} }
func (l *uiLauncher) Open(section string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	client := daemon.NewClient(l.identity + "-ui")
	defer client.Close()
	open := func() error {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		return client.JSON(ctx, "/open", section, nil)
	}
	if open() == nil {
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "--ui", section)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	daemon.Detach(cmd)
	if err := cmd.Start(); err != nil {
		return err
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
			return errors.New("desktop did not open")
		case <-ticker.C:
			if open() == nil {
				return nil
			}
		}
	}
}
func (l *uiLauncher) Stop() error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c := daemon.NewClient(l.identity + "-ui")
	defer c.Close()
	return c.JSON(ctx, "/stop", nil, nil)
}

func uiHandler(open func(string), closeWindow func(string), stop func()) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != "" {
			http.Error(w, "forbidden", 403)
			return
		}
		switch r.URL.Path {
		case "/open", "/close":
			var section string
			if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&section) != nil {
				http.Error(w, "invalid section", 400)
				return
			}
			if r.URL.Path == "/open" {
				open(section)
			} else {
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
		_, _ = w.Write([]byte("true"))
	})
}
