package main

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/settings"
)

// These observers are embedded only in the separate acceptance executable.
// They are never registered as DSH tools or included in the product extension.
//
//go:embed inspect-scratch.mjs
var inspectorSource []byte

//go:embed scratch-process.ps1
var scratchProcessSource []byte

type reliabilityRun struct {
	root, evidence, base, node, browser, instance string
	service                                       *automation.Service
	server                                        *http.Server
	a, b                                          string
	parked, sleepAt                               time.Time
	lease                                         automation.BrowserLease
	requests                                      []automation.Request
	idleOK, sleepOK, faultsOK                     bool
}
type tabObservation struct {
	ID                int `json:"id"`
	Group             int `json:"groupId"`
	Attached, Missing bool
}
type groupObservation struct {
	ID           int `json:"id"`
	Title, Color string
}
type scratchObservation struct {
	Instance string
	Tabs     []tabObservation
	Groups   []groupObservation
}

func saveJSON(file string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(file, append(b, '\n'), 0600)
}
func (w *browserWorker) prepareReliability(ctx context.Context, root, evidence string, s *automation.Service, m *settings.Manager) error {
	if w.reliability != nil {
		return errors.New("reliability already prepared")
	}
	w.mu.Lock()
	empty := len(w.resources) == 0
	w.mu.Unlock()
	if !empty {
		return errors.New("use a fresh reliability owner; do not run exercise first")
	}
	var config integrationConfig
	data, err := os.ReadFile(filepath.Join(root, "integration-config.json"))
	if err != nil {
		return err
	}
	if err = json.Unmarshal(data, &config); err != nil {
		return err
	}
	if !filepath.IsAbs(config.BrowserExecutable) {
		return errors.New("restart browser executable missing; use -Reliability entry")
	}
	lease, err := s.SelectedBrowserConnection()
	if err != nil {
		return err
	}
	prefs, err := m.Snapshot(ctx)
	if err != nil {
		return err
	}
	instance := ""
	if prefs.BrowserProfiles != nil {
		for _, b := range prefs.BrowserProfiles.Bindings {
			if b.ID == lease.Binding && b.Name == "B" {
				instance = b.Instance
			}
		}
	}
	if instance == "" {
		return errors.New("explicitly select confirmed scratch B before reliability")
	}
	for name, body := range map[string][]byte{"inspect-scratch.mjs": inspectorSource, "scratch-process.ps1": scratchProcessSource} {
		if err = os.WriteFile(filepath.Join(root, name), body, 0600); err != nil {
			return err
		}
	}
	l, err := listenFixture()
	if err != nil {
		return err
	}
	r := &reliabilityRun{root: root, evidence: evidence, base: "http://" + l.Addr().String(), node: config.Node, browser: config.BrowserExecutable, instance: instance, service: s}
	r.server = &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/large" {
			fmt.Fprint(rw, `<!doctype html><title>Large local image</title><h1>Large local image</h1><canvas id="noise" width="2048" height="2048"></canvas><script>const c=document.querySelector('#noise').getContext('2d'),image=c.createImageData(2048,2048);let seed=123456789;const random=()=>{seed=(Math.imul(seed,1664525)+1013904223)>>>0;return seed>>>24};for(let i=0;i<image.data.length;i+=4){image.data[i]=random();image.data[i+1]=random();image.data[i+2]=random();image.data[i+3]=255}c.putImageData(image,0,0);</script>`)
			return
		}
		fmt.Fprintf(rw, "<!doctype html><title>Reliability</title><h1>%s</h1>", req.URL.Path)
	})}
	go r.server.Serve(l)
	w.reliability = r
	if err = w.freshPair(ctx); err != nil {
		return err
	}
	if _, err = r.inspect(ctx, "reliability-prepared", "", false); err != nil {
		return err
	}
	r.parked = time.Now() // No observer/DevTools or MCP polling after this timestamp.
	return saveJSON(filepath.Join(evidence, "reliability-parked.json"), map[string]any{"at": r.parked.UTC(), "minimumIdleSeconds": 1800, "instance": instance, "lease": r.lease, "devtoolsDisconnected": true, "productExecution": false})
}
func (w *browserWorker) browserCall(ctx context.Context, id, tool string, args map[string]any) (json.RawMessage, error) {
	return w.call(ctx, map[string]any{"action": "call", "id": rand.Text(), "session": id, "tool": "my_browser_" + tool, "arguments": args})
}
func (w *browserWorker) freshOne(ctx context.Context, label string) (string, error) {
	r := w.reliability
	id := "rel-" + label + "-" + rand.Text()
	if _, err := w.call(ctx, map[string]any{"action": "create", "session": id}); err != nil {
		return "", err
	}
	if err := w.awaitSchemas(ctx, id, 23); err != nil {
		return "", err
	}
	if _, err := w.browserCall(ctx, id, "navigate", map[string]any{"url": r.base + "/" + id}); err != nil {
		return "", err
	}
	return id, nil
}
func (w *browserWorker) freshPair(ctx context.Context) error {
	r := w.reliability
	var err error
	r.a, err = w.freshOne(ctx, "A")
	if err != nil {
		return err
	}
	r.b, err = w.freshOne(ctx, "B")
	if err != nil {
		return err
	}
	r.lease, err = r.service.SelectedBrowserConnection()
	if err != nil {
		return err
	}
	r.requests = nil
	w.mu.Lock()
	for _, id := range []string{r.a, r.b} {
		r.requests = append(r.requests, w.resources[id].request)
	}
	w.mu.Unlock()
	return nil
}
func (r *reliabilityRun) inspect(ctx context.Context, name, expected string, released bool) (scratchObservation, error) {
	var observed scratchObservation
	output := filepath.Join(r.evidence, name+".json")
	mode := "capture"
	if released {
		mode = "released"
	}
	if name == "reliability-after-stop" || name == "reliability-after-large" {
		mode = "stopped-a"
	}
	args := []string{filepath.Join(r.root, "inspect-scratch.mjs"), r.root, r.instance, output, expected, mode}
	bounded, stop := context.WithTimeout(ctx, 15*time.Second)
	defer stop()
	out, err := exec.CommandContext(bounded, r.node, args...).CombinedOutput()
	if err != nil {
		return observed, fmt.Errorf("scratch observation %s: %w: %s", name, err, out)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		return observed, err
	}
	err = json.Unmarshal(data, &observed)
	return observed, err
}
func (r *reliabilityRun) process(ctx context.Context, mode string) error {
	bounded, stop := context.WithTimeout(ctx, 20*time.Second)
	defer stop()
	out, err := exec.CommandContext(bounded, "pwsh", "-NoProfile", "-File", filepath.Join(r.root, "scratch-process.ps1"), "-Mode", mode, "-Root", r.root, "-Browser", r.browser).CombinedOutput()
	_ = os.WriteFile(filepath.Join(r.evidence, "process-"+mode+".txt"), out, 0600)
	if err != nil {
		return fmt.Errorf("owned %s: %w: %s", mode, err, out)
	}
	return nil
}
func (w *browserWorker) verifyPair(ctx context.Context) error {
	r := w.reliability
	for _, id := range []string{r.a, r.b} {
		raw, err := w.browserCall(ctx, id, "snapshot", map[string]any{})
		_ = os.WriteFile(filepath.Join(r.evidence, "reliability-snapshot-"+rand.Text()+".json"), raw, 0600)
		if err != nil {
			return err
		}
		if !strings.Contains(string(raw), r.base+"/"+id) {
			return errors.New("reliability page/session changed")
		}
	}
	return nil
}
func (w *browserWorker) idleReliability(ctx context.Context) error {
	r := w.reliability
	if r == nil || !r.faultsOK || r.idleOK {
		return errors.New("prepare reliability and finish faults once before idle")
	}
	remaining := 30*time.Minute - time.Since(r.parked)
	if remaining > 0 {
		timer := time.NewTimer(remaining)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	elapsed := time.Since(r.parked)
	err := r.service.ValidateBrowserLease(r.lease)
	if err == nil {
		err = w.verifyPair(ctx)
	}
	_ = saveJSON(filepath.Join(r.evidence, "reliability-idle.json"), map[string]any{"elapsedMilliseconds": elapsed.Milliseconds(), "passed": err == nil, "error": errorText(err), "noMCPOrDevtoolsPolling": true})
	r.idleOK = err == nil
	return err
}
func (w *browserWorker) beginSleep() error {
	r := w.reliability
	if r == nil || !r.idleOK || r.sleepOK {
		return errors.New("complete idle before the user-assisted sleep check")
	}
	r.sleepAt = time.Now()
	return saveJSON(filepath.Join(r.evidence, "reliability-sleep-start.json"), map[string]any{"at": r.sleepAt.UTC(), "userAssisted": true, "minimumSeconds": 120})
}
func (w *browserWorker) resumeSleep(ctx context.Context) error {
	r := w.reliability
	if r == nil || r.sleepAt.IsZero() {
		return errors.New("begin-sleep first, then manually put Windows to sleep for at least two minutes")
	}
	elapsed := time.Now().UTC().Sub(r.sleepAt.UTC())
	if elapsed < 2*time.Minute {
		return errors.New("sleep interval shorter than two minutes")
	}
	mode := "continued"
	var err error
	if !r.service.Snapshot().GatewayConnected {
		err = errors.New("automation host channel did not automatically recover after sleep")
	} else if r.service.ValidateBrowserLease(r.lease) != nil {
		mode = "rebuilt"
		if !w.allResourcesClosed() {
			err = errors.New("sleep invalidated lease but retained old resources")
		} else {
			for _, request := range r.requests {
				if _, e := r.service.Execute(ctx, request); !errors.Is(e, automation.ErrRevoked) {
					err = errors.New("old sleep epoch request revived")
				}
			}
			if err == nil {
				bounded, stop := context.WithTimeout(ctx, 45*time.Second)
				err = waitFor(bounded, func() bool { _, e := r.service.SelectedBrowserConnection(); return e == nil })
				stop()
				if err == nil {
					err = w.freshPair(ctx)
				}
			}
		}
	}
	if err == nil {
		err = w.verifyPair(ctx)
	}
	_ = saveJSON(filepath.Join(r.evidence, "reliability-sleep.json"), map[string]any{"elapsedMilliseconds": elapsed.Milliseconds(), "userAssisted": true, "mode": mode, "passed": err == nil, "error": errorText(err), "gatewayConnected": r.service.Snapshot().GatewayConnected, "resourceTreesClosed": w.allResourcesClosed(), "currentEpoch": r.service.Snapshot().Browser.Epoch, "savedEpoch": r.lease.Epoch, "productExecution": false})
	r.sleepOK = err == nil
	return err
}
func activePair(observed scratchObservation) error {
	if len(observed.Groups) != 2 || len(observed.Tabs) != 2 {
		return errors.New("expected exactly two controlled one-tab groups")
	}
	colors := map[string]bool{}
	for _, g := range observed.Groups {
		if !strings.HasPrefix(g.Title, "DSH Work · ") || len([]rune(g.Title)) > 42 || colors[g.Color] {
			return errors.New("group title truncation/color contract failed")
		}
		colors[g.Color] = true
	}
	for _, t := range observed.Tabs {
		if t.Missing || t.Group < 0 || !t.Attached {
			return errors.New("controlled tab not grouped/debugger-attached")
		}
	}
	return nil
}
func (w *browserWorker) reliabilityFaults(ctx context.Context) error {
	r := w.reliability
	if r == nil || r.faultsOK {
		return errors.New("prepare reliability and run faults once before idle/sleep")
	}
	ctx, stop := context.WithTimeout(ctx, 150*time.Second)
	defer stop()
	before, err := r.inspect(ctx, "reliability-before-stop", "", false)
	if err != nil {
		return err
	}
	if err = activePair(before); err != nil {
		return err
	}
	// The real rc.5 Agent owns this maintenance activity and its signal; it
	// invokes the actual user cancel API without contacting a model provider.
	stoppedCall := make(chan error, 1)
	go func() {
		_, err := w.call(ctx, map[string]any{"action": "maintenance-call", "session": r.a, "id": rand.Text(), "tool": "my_browser_wait_for", "arguments": map[string]any{"time": 10}})
		stoppedCall <- err
	}()
	time.Sleep(250 * time.Millisecond)
	stopStart := time.Now()
	rawStop, err := w.call(ctx, map[string]any{"action": "stop-agent", "session": r.a})
	if err != nil {
		return err
	}
	var stopResult struct {
		Active bool `json:"active"`
	}
	if json.Unmarshal(rawStop, &stopResult) != nil || !stopResult.Active {
		return errors.New("user Stop did not reach an active owned Agent activity")
	}
	bounded, cancelStop := context.WithTimeout(ctx, 5*time.Second)
	err = waitFor(bounded, func() bool {
		w.mu.Lock()
		resource := w.resources[r.a]
		w.mu.Unlock()
		resource.mu.Lock()
		defer resource.mu.Unlock()
		return resource.closed && resource.closeErr == nil
	})
	cancelStop()
	if err != nil {
		return errors.New("user Stop waited for the browser action instead of closing its owned resource")
	}
	select {
	case err = <-stoppedCall:
		if err == nil {
			return errors.New("user-stopped call succeeded")
		}
	case <-ctx.Done():
		return ctx.Err()
	}
	if err = saveJSON(filepath.Join(r.evidence, "reliability-user-stop.json"), map[string]any{"ownerAPI": "Agent.cancel({kind:user})", "modelCalls": 0, "millisecondsToResourceClose": time.Since(stopStart).Milliseconds(), "passed": true}); err != nil {
		return err
	}
	if _, err = w.browserCall(ctx, r.a, "snapshot", map[string]any{}); err == nil {
		return errors.New("stopped reliability A executed")
	}
	if _, err = w.browserCall(ctx, r.b, "snapshot", map[string]any{}); err != nil {
		return err
	}
	after, err := r.inspect(ctx, "reliability-after-stop", filepath.Join(r.evidence, "reliability-before-stop.json"), false)
	if err != nil {
		return err
	}
	aGroup, bGroup := -1, -1
	for _, g := range before.Groups {
		if strings.Contains(g.Title, "rel-A-") {
			aGroup = g.ID
		}
		if strings.Contains(g.Title, "rel-B-") {
			bGroup = g.ID
		}
	}
	if aGroup < 0 || bGroup < 0 {
		return errors.New("could not associate observed groups with real top session titles")
	}
	for _, old := range before.Tabs {
		found := false
		for _, now := range after.Tabs {
			if now.ID != old.ID {
				continue
			}
			found = true
			if now.Missing {
				return errors.New("stop deleted a controlled tab")
			}
			if old.Group == aGroup && (now.Group != -1 || now.Attached) {
				return errors.New("stop did not detach/ungroup A")
			}
			if old.Group == bGroup && (now.Group != bGroup || !now.Attached) {
				return errors.New("stop affected B")
			}
		}
		if !found {
			return errors.New("stopped tab not preserved")
		}
	}
	r.a, err = w.freshOne(ctx, "A")
	if err != nil {
		return err
	}
	if _, err = w.browserCall(ctx, r.a, "navigate", map[string]any{"url": r.base + "/large"}); err != nil {
		return err
	}
	if _, err = r.inspect(ctx, "reliability-before-large", "", false); err != nil {
		return err
	}
	largeStart := time.Now()
	large, largeErr := w.browserCall(ctx, r.a, "take_screenshot", map[string]any{"type": "png", "scale": "css", "fullPage": true})
	_ = os.WriteFile(filepath.Join(r.evidence, "reliability-large-response.json"), large, 0600)
	if largeErr == nil || !strings.Contains(string(large), "BROWSER_NATIVE_MESSAGE_TOO_LARGE") {
		return fmt.Errorf("large screenshot did not return an explicit native size error: %v", largeErr)
	}
	if time.Since(largeStart) > 8*time.Second {
		return errors.New("large response was surfaced as a timeout instead of immediate native size refusal")
	}
	if _, err = w.browserCall(ctx, r.b, "snapshot", map[string]any{}); err != nil {
		return fmt.Errorf("large A response affected B: %w", err)
	}
	if _, err = r.inspect(ctx, "reliability-after-large", filepath.Join(r.evidence, "reliability-before-large.json"), false); err != nil {
		return err
	}
	// Explicitly release the now-terminal failed session entry; never reopen its
	// old reference. The backend's own fatal path already owns process teardown.
	w.mu.Lock()
	failedRequest := w.resources[r.a].request
	w.mu.Unlock()
	r.service.StopSession(failedRequest.Generation, failedRequest.Session, failedRequest.Lifetime)
	r.a, err = w.freshOne(ctx, "A")
	if err != nil {
		return err
	}
	before, err = r.inspect(ctx, "reliability-before-native", "", false)
	if err != nil {
		return err
	}
	if err = activePair(before); err != nil {
		return err
	}
	if err = r.process(ctx, "native-stop"); err != nil {
		return err
	}
	if err = waitFor(ctx, func() bool {
		_, e := r.service.SelectedBrowserConnection()
		return errors.Is(e, automation.ErrBrowserOffline) && w.allResourcesClosed()
	}); err != nil {
		return err
	}
	if _, err = r.inspect(ctx, "reliability-after-native", filepath.Join(r.evidence, "reliability-before-native.json"), true); err != nil {
		return err
	}
	if err = waitFor(ctx, func() bool {
		v := r.service.Snapshot().Profiles
		return len(v.Bindings) == 2 && v.Bindings[0].Status == "connected" && v.Bindings[1].Status == "connected"
	}); err != nil {
		return err
	}
	if err = w.freshPair(ctx); err != nil {
		return err
	}
	before, err = r.inspect(ctx, "reliability-before-browser", "", false)
	if err != nil {
		return err
	}
	if err = activePair(before); err != nil {
		return err
	}
	oldLease := r.lease
	oldRequests := r.requests
	if err = r.process(ctx, "browser-stop"); err != nil {
		return err
	}
	if err = waitFor(ctx, func() bool {
		_, e := r.service.SelectedBrowserConnection()
		return errors.Is(e, automation.ErrBrowserOffline) && w.allResourcesClosed()
	}); err != nil {
		return err
	}
	// The separate A browser was not stopped and cannot be used as fallback.
	otherConnected := false
	for _, b := range r.service.Snapshot().Profiles.Bindings {
		if b.Name == "A" && b.Status == "connected" {
			otherConnected = true
		}
	}
	if !otherConnected {
		return errors.New("selected browser restart affected unselected scratch A")
	}
	if err = r.process(ctx, "browser-start"); err != nil {
		return err
	}
	if err = waitFor(ctx, func() bool {
		lease, e := r.service.SelectedBrowserConnection()
		return e == nil && lease.Binding == oldLease.Binding && lease.Connection != oldLease.Connection
	}); err != nil {
		return err
	}
	for _, request := range oldRequests {
		if _, err = r.service.Execute(ctx, request); !errors.Is(err, automation.ErrRevoked) {
			return errors.New("old browser epoch request revived")
		}
	}
	if err = w.freshPair(ctx); err != nil {
		return err
	}
	if err = w.verifyPair(ctx); err != nil {
		return err
	}
	if _, err = r.inspect(ctx, "reliability-after-browser", "", false); err != nil {
		return err
	}
	r.faultsOK = true
	r.parked = time.Now()
	if err = saveJSON(filepath.Join(r.evidence, "reliability-idle-start.json"), map[string]any{"at": r.parked.UTC(), "minimumIdleSeconds": 1800, "devtoolsDisconnected": true}); err != nil {
		return err
	}
	return saveJSON(filepath.Join(r.evidence, "reliability-faults.json"), map[string]any{"status": "passed", "stopPreservedTabs": true, "nativeCrashPhysicalRelease": true, "largeResponseRefusedWithoutTimeout": true, "largeResponseIsolatedToA": true, "nativeAutomaticReconnect": true, "selectedBrowserRestart": true, "unselectedAContinued": true, "oldEpochRefused": true, "productExecution": false})
}
func (w *browserWorker) finishReliability(ctx context.Context) error {
	r := w.reliability
	if r == nil || !r.idleOK || !r.sleepOK || !r.faultsOK {
		return errors.New("reliability stages incomplete")
	}
	if _, err := r.inspect(ctx, "reliability-before-final-close", "", false); err != nil {
		return err
	}
	if _, err := r.service.SetEnabled(ctx, automation.Browser, false); err != nil {
		return err
	}
	if !w.allResourcesClosed() {
		return errors.New("final disable retained owned resources")
	}
	if _, err := r.inspect(ctx, "reliability-final-release", filepath.Join(r.evidence, "reliability-before-final-close.json"), true); err != nil {
		return err
	}
	return saveJSON(filepath.Join(r.evidence, "reliability-summary.json"), map[string]any{"status": "passed", "idle": true, "userAssistedSleep": true, "faults": true, "physicalFinalRelease": true, "productExecution": false})
}
