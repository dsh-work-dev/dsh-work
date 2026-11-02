// Windows scratch-only acceptance entry. Never mounted by the product daemon.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/automation/browsernative"
	"github.com/local/dsh-work/internal/browserhost"
	"github.com/local/dsh-work/internal/daemon"
	"github.com/local/dsh-work/internal/platform"
	"github.com/local/dsh-work/internal/redact"
	"github.com/local/dsh-work/internal/settings"
)

func main() {
	if e := run(); e != nil {
		message := redact.Secrets(e.Error())
		if len(os.Args) > 1 && (os.Args[1] == "owner" || os.Args[1] == "formal-owner" || os.Args[1] == "integration-owner") {
			if exe, err := os.Executable(); err == nil {
				root := filepath.Dir(exe)
				if marker, err := os.ReadFile(filepath.Join(root, "binding-gate-owner.txt")); err == nil && strings.TrimSpace(string(marker)) == "dsh-work-binding-acceptance-v1" {
					if os.MkdirAll(filepath.Join(root, "evidence"), 0700) == nil {
						_ = os.WriteFile(filepath.Join(root, "evidence", "owner-error.txt"), []byte(message+"\n"), 0600)
					}
				}
			}
		}
		fmt.Fprintln(os.Stderr, message)
		os.Exit(1)
	}
}
func run() error {
	exe, e := os.Executable()
	if e != nil {
		return e
	}
	root := filepath.Dir(exe)
	marker, e := os.ReadFile(filepath.Join(root, "binding-gate-owner.txt"))
	if e != nil || strings.TrimSpace(string(marker)) != "dsh-work-binding-acceptance-v1" {
		return errors.New("scratch ownership marker required")
	}
	identity := "browser-binding-acceptance:" + root
	integration := len(os.Args) > 1 && os.Args[1] == "integration-owner"
	formal := len(os.Args) > 1 && (os.Args[1] == "formal-owner" || integration)
	if formal {
		identity = root // Matches the product's existing isolated desktop fixture identity.
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if len(os.Args) > 1 && strings.HasPrefix(os.Args[1], "chrome-extension://") {
		client := daemon.NewClient(identity)
		defer client.Close()
		e := browsernative.Run(ctx, client, os.Args[1], os.Stdin, os.Stdout)
		if errors.Is(e, io.EOF) {
			return nil
		}
		return e
	}
	if len(os.Args) != 3 || (os.Args[1] != "owner" && !formal) || (os.Args[2] != "chrome" && os.Args[2] != "edge") {
		return errors.New("usage: browser-binding-acceptance owner|formal-owner chrome|edge")
	}
	evidence := filepath.Join(root, "evidence")
	if e := os.MkdirAll(evidence, 0700); e != nil {
		return e
	}
	log, e := os.OpenFile(filepath.Join(evidence, "events.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	defer log.Close()
	var mu sync.Mutex
	nativeInstances := map[string]string{}
	record := func(kind string, fields map[string]any) {
		mu.Lock()
		defer mu.Unlock()
		if fields == nil {
			fields = map[string]any{}
		}
		if kind == "native-connected" {
			nativeInstances[fields["connection"].(string)] = fields["instance"].(string)
		}
		if kind == "native-disconnected" {
			delete(nativeInstances, fields["connection"].(string))
		}
		fields["event"] = kind
		fields["at"] = time.Now().UTC()
		json.NewEncoder(log).Encode(fields)
	}
	m, e := settings.New(settings.Config{Path: filepath.Join(root, "settings.json")})
	if e != nil {
		return e
	}
	s := automation.New(m, nil)
	if integration {
		s.Close()
		s, m, e = automation.NewDevelopmentAcceptance(root, readyDependencies{})
		if e != nil {
			return e
		}
	}
	defer s.Close()
	if formal {
		config := browserhost.Config{Directory: filepath.Join(root, "native"), Executable: filepath.Join(root, "dsh-work.exe"), Identity: identity, Replacer: platform.New().FileReplacer}
		registrar := browserhost.New(config)
		defer func() {
			record("registration-cleanup", map[string]any{"error": errorText(registrar.Remove(context.Background()))})
		}()
		registrations := registrar.Ensure(ctx)
		record("registration", map[string]any{"result": registrations})
		for _, r := range registrations {
			if !r.Registered || r.Failure != "" {
				return fmt.Errorf("formal registration failed for %s: %s", r.Browser, r.Failure)
			}
		}
		if e := browserhost.InstallDevelopmentBundle(ctx, config); e != nil {
			return e
		}
		// Repeating both operations exercises the ordinary update path without
		// replacing a user's installation or changing executable ownership.
		if e := registrar.Refresh(ctx); e != nil {
			return e
		}
		record("registration-refresh", nil)
		if integration {
			var config integrationConfig
			data, err := os.ReadFile(filepath.Join(root, "integration-config.json"))
			if err != nil {
				return err
			}
			if err = json.Unmarshal(data, &config); err != nil {
				return err
			}
			if config.Reliability {
				if err = verifyNativePathChange(ctx, root, evidence); err != nil {
					return err
				}
				record("registration-path-change", map[string]any{"passed": true})
			}
		}
	}
	s.OnChange(func(v automation.Snapshot) { record("state", map[string]any{"snapshot": v}) })
	var integrationWorker *browserWorker
	if integration {
		integrationWorker, e = startBrowserWorker(ctx, root, s, record)
		if e != nil {
			return e
		}
		defer func() {
			if integrationWorker != nil {
				integrationWorker.Close()
			}
		}()
	}
	handler := &browsernative.Handler{Service: s, Browser: os.Args[2], Context: ctx, Observe: record}
	if formal {
		handler.Browser = "" // Only the signed product parent adapter supplies family.
	}
	listener, e := daemon.Listen(identity)
	if e != nil {
		return e
	}
	defer listener.Close()
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if e := server.Serve(listener); e != nil && !errors.Is(e, http.ErrServerClosed) {
			record("serve-error", map[string]any{"error": e.Error()})
			cancel()
		}
	}()
	defer server.Close()
	defer cancel()
	checks := map[string]int{}
	if prefs, err := m.Snapshot(ctx); err == nil && prefs.BrowserProfiles != nil {
		for _, row := range prefs.BrowserProfiles.Bindings {
			record("binding-restored", map[string]any{"name": row.Name, "binding": row.ID})
		}
	}
	integrationPassed := false
	lifecyclePassed := false
	reliabilityPassed := false
	leases := map[string]automation.BrowserLease{}
	find := func(name string) (automation.BrowserBindingView, error) {
		var rows []automation.BrowserBindingView
		for _, b := range s.Snapshot().Profiles.Bindings {
			if b.Name == name {
				rows = append(rows, b)
			}
		}
		if len(rows) != 1 {
			return automation.BrowserBindingView{}, errors.New("expected exactly one confirmed binding named " + name)
		}
		return rows[0], nil
	}
	record("owner-ready", map[string]any{"productExecution": false, "browser": os.Args[2], "formalNative": formal})
	fmt.Println("Ready. In each scratch profile open the extension, name it A or B, then request binding.")
	fmt.Println("Commands: state | confirm A|B | select A|B | check A|B | stable A|B | stale A|B | offline B | reconnected A|B | conflict B | exercise | lifecycle | prepare-reliability | idle | begin-sleep | resume-sleep | faults | finish-reliability | finish | finish-integration | quit")
	scanner := bufio.NewScanner(os.Stdin)
	lines := make(chan string)
	inputDone := make(chan error, 1)
	go func() {
		for scanner.Scan() {
			select {
			case lines <- scanner.Text():
			case <-ctx.Done():
				return
			}
		}
		inputDone <- scanner.Err()
	}()
	for {
		var line string
		select {
		case <-ctx.Done():
			record("owner-cancelled", nil)
			return nil
		case err := <-inputDone:
			return err
		case line = <-lines:
		}
		parts := strings.Fields(line)
		if len(parts) == 0 {
			continue
		}
		cmd := parts[0]
		name := ""
		if len(parts) == 2 {
			name = parts[1]
		}
		check := func() error {
			switch cmd {
			case "exercise":
				if integrationWorker == nil {
					return errors.New("exercise requires isolated integration-owner")
				}
				err := integrationWorker.exercise(ctx, evidence)
				integrationPassed = err == nil
				return err
			case "lifecycle":
				if integrationWorker == nil {
					return errors.New("lifecycle requires isolated integration-owner")
				}
				if err := integrationWorker.lifecycle(ctx, evidence, s); err != nil {
					return err
				}
				integrationWorker.Close()
				integrationWorker, e = startBrowserWorker(ctx, root, s, record)
				if e != nil {
					return e
				}
				if e = integrationWorker.recoverAfterWorkerExit(ctx); e != nil {
					return e
				}
				lifecyclePassed = true
				return os.WriteFile(filepath.Join(evidence, "lifecycle-summary.json"), []byte(`{"status":"passed","scope":"single-call drain, child dispose, capability off/on, exact Worker root hard exit and new generation recovery","modelCalls":0,"productExecution":false}`), 0600)
			case "prepare-reliability", "idle", "begin-sleep", "resume-sleep", "faults":
				if integrationWorker == nil {
					return errors.New("reliability requires isolated integration-owner")
				}
				switch cmd {
				case "prepare-reliability":
					return integrationWorker.prepareReliability(ctx, root, evidence, s, m)
				case "idle":
					fmt.Println("Waiting for 30 minutes with no MCP/DevTools polling. Keep Windows awake; Ctrl+C cancels safely.")
					return integrationWorker.idleReliability(ctx)
				case "begin-sleep":
					fmt.Println("Put Windows to sleep manually for at least two minutes, wake it, then type resume-sleep.")
					return integrationWorker.beginSleep()
				case "resume-sleep":
					return integrationWorker.resumeSleep(ctx)
				case "faults":
					return integrationWorker.reliabilityFaults(ctx)
				}
				return errors.New("unknown reliability stage")
			case "state":
				b, _ := json.MarshalIndent(s.Snapshot(), "", "  ")
				fmt.Println(string(b))
				return nil
			case "confirm":
				var candidates []automation.BrowserBindingRequest
				for _, p := range s.Snapshot().Profiles.Pending {
					if p.Name == name {
						candidates = append(candidates, p)
					}
				}
				if len(candidates) != 1 {
					return errors.New("expected exactly one live candidate named " + name)
				}
				id, e := s.ConfirmBrowserBinding(ctx, candidates[0].ID)
				if e != nil {
					return e
				}
				record("user-confirmed", map[string]any{"name": name, "candidate": candidates[0].ID, "binding": id})
				checks["bound-"+name]++
				return nil
			case "select":
				b, e := find(name)
				if e != nil {
					return e
				}
				return s.SelectBrowserBinding(ctx, b.ID)
			case "check", "reconnected":
				b, e := find(name)
				if e != nil {
					return e
				}
				l, e := s.SelectedBrowserConnection()
				if e != nil {
					return e
				}
				if l.Binding != b.ID || b.Status != "connected" {
					return errors.New("selected the wrong binding")
				}
				if cmd == "reconnected" {
					previous, ok := leases[name]
					if !ok || previous.Connection == l.Connection || previous.Binding != l.Binding {
						return errors.New("expected new connection, same binding")
					}
					if s.ValidateBrowserLease(previous) == nil {
						return errors.New("old lease revived")
					}
				}
				leases[name] = l
				checks[cmd+"-"+name]++
				record("lease-checked", map[string]any{"name": name, "lease": l})
				return nil
			case "stable", "stale":
				l, ok := leases[name]
				if !ok {
					return errors.New("check the binding first")
				}
				e := s.ValidateBrowserLease(l)
				if cmd == "stable" && e != nil {
					return e
				}
				if cmd == "stale" && e == nil {
					return errors.New("old lease unexpectedly valid")
				}
				checks[cmd+"-"+name]++
				if cmd == "stable" {
					for _, other := range s.Snapshot().Profiles.Bindings {
						if other.Name != name && other.Status == "conflict" {
							checks["stable-during-other-conflict-"+name]++
						}
					}
				}
				return nil
			case "offline", "conflict":
				b, e := find(name)
				if e != nil {
					return e
				}
				if s.Snapshot().Profiles.Selected != b.ID {
					return errors.New("select the tested binding first")
				}
				_, e = s.SelectedBrowserConnection()
				expected := automation.ErrBrowserOffline
				status := "offline"
				if cmd == "conflict" {
					expected = automation.ErrBrowserConflict
					status = "conflict"
				}
				if !errors.Is(e, expected) || b.Status != status {
					return fmt.Errorf("expected %s; got %s / %v", status, b.Status, e)
				}
				if cmd == "offline" {
					otherLive := false
					for _, other := range s.Snapshot().Profiles.Bindings {
						otherLive = otherLive || other.ID != b.ID && other.Status == "connected"
					}
					if !otherLive {
						return errors.New("expected the other scratch profile to remain connected")
					}
				} else {
					prefs, _ := m.Snapshot(ctx)
					instance := ""
					for _, row := range prefs.BrowserProfiles.Bindings {
						if row.ID == b.ID {
							instance = row.Instance
						}
					}
					mu.Lock()
					count := 0
					for _, value := range nativeInstances {
						if value == instance {
							count++
						}
					}
					mu.Unlock()
					if count >= 2 {
						checks["duplicate-active-"+name]++
					} else if count == 1 {
						if checks["duplicate-active-"+name] == 0 {
							return errors.New("observe both duplicate connections before checking quarantine")
						}
						checks["duplicate-quarantined-"+name]++
					} else {
						return errors.New("no conflicting native instance remains")
					}
				}
				checks[cmd+"-"+name]++
				return nil
			case "finish", "finish-native", "finish-integration", "finish-reliability":
				if cmd == "finish-integration" && (!integration || !integrationPassed) {
					return errors.New("finish-integration requires a successful exercise in this run")
				}
				if cmd == "finish-native" && !formal {
					return errors.New("finish-native requires the formal native-host fixture")
				}
				required := []string{"bound-A", "bound-B", "check-A", "check-B", "stale-A"}
				if cmd == "finish-integration" || cmd == "finish-reliability" {
					required = []string{"check-A", "check-B", "stale-A"}
					for _, name := range []string{"A", "B"} {
						binding, err := find(name)
						if err != nil || binding.Status != "connected" {
							return errors.New("finish-integration requires both explicitly bound profiles connected")
						}
					}
				}
				if cmd == "finish" {
					required = append(required, "stable-A", "stable-during-other-conflict-A", "offline-B", "reconnected-B", "duplicate-active-B", "duplicate-quarantined-B")
				}
				for _, key := range required {
					if checks[key] < 1 {
						return errors.New("missing check: " + key)
					}
				}
				if cmd == "finish" && checks["conflict-B"] < 2 {
					return errors.New("check conflict once with duplicate, again after closing only duplicate")
				}
				loaded, e := settings.New(settings.Config{Path: filepath.Join(root, "settings.json")})
				if e != nil {
					return e
				}
				v, e := loaded.Snapshot(ctx)
				if e != nil {
					return e
				}
				current, _ := m.Snapshot(ctx)
				if v.BrowserProfiles == nil || v.BrowserProfiles.Selected != current.BrowserProfiles.Selected || len(v.BrowserProfiles.Bindings) != 2 {
					return errors.New("persistent selection mismatch")
				}
				restored := automation.New(loaded, nil)
				if _, e := restored.SelectedBrowserConnection(); !errors.Is(e, automation.ErrBrowserOffline) {
					restored.Close()
					return errors.New("live route persisted")
				}
				restored.Close()
				if _, e := s.Execute(ctx, automation.Request{Capability: automation.Browser, Operation: "browser", Tool: "browser_navigate", Session: "fixture", Lifetime: "fixture"}); !errors.Is(e, automation.ErrRevoked) {
					return errors.New("normal execution gate opened")
				}
				if cmd == "finish-reliability" {
					if integrationWorker == nil {
						return errors.New("isolated integration-owner required")
					}
					if err := integrationWorker.finishReliability(ctx); err != nil {
						return err
					}
					reliabilityPassed = true
				}
				summary := map[string]any{"at": time.Now().UTC(), "checks": checks, "productExecution": false, "persistence": true, "status": "passed", "snapshot": s.Snapshot(), "formalNative": formal, "scope": cmd, "lifecyclePassed": lifecyclePassed, "reliabilityPassed": reliabilityPassed}
				data, e := json.MarshalIndent(summary, "", "  ")
				if e != nil {
					return e
				}
				if e := os.WriteFile(filepath.Join(evidence, "summary.json"), data, 0600); e != nil {
					return e
				}
				return nil
			case "quit":
				return nil
			default:
				return errors.New("unknown command")
			}
		}
		err := check()
		record("command", map[string]any{"command": line, "passed": err == nil, "error": fmt.Sprint(err)})
		if err != nil {
			fmt.Println("FAIL:", err)
			continue
		}
		fmt.Println("OK:", line)
		if cmd == "finish" || cmd == "finish-native" || cmd == "finish-integration" || cmd == "finish-reliability" || cmd == "quit" {
			return nil
		}
	}
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
