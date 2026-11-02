package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/local/dsh-work/internal/automation"
)

// Acceptance-only observation of the real resource, not another execution
// backend. Close returns only after the production Job/route teardown finishes.
type observedResource struct {
	automation.Resource
	request  automation.Request
	mu       sync.Mutex
	closed   bool
	closeErr error
}

func (r *observedResource) Close() error {
	err := r.Resource.Close()
	r.mu.Lock()
	r.closed = true
	r.closeErr = err
	r.mu.Unlock()
	return err
}
func (w *browserWorker) allResourcesClosed() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, r := range w.resources {
		r.mu.Lock()
		ok := r.closed && r.closeErr == nil
		r.mu.Unlock()
		if !ok {
			return false
		}
	}
	return true
}
func waitFor(ctx context.Context, predicate func() bool) error {
	timer := time.NewTicker(20 * time.Millisecond)
	defer timer.Stop()
	for {
		if predicate() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
}
func (w *browserWorker) awaitSchemas(ctx context.Context, session string, count int) error {
	return waitFor(ctx, func() bool {
		raw, err := w.call(ctx, map[string]any{"action": "schemas", "session": session})
		if err != nil {
			return false
		}
		var tools []any
		return json.Unmarshal(raw, &tools) == nil && len(tools) == count
	})
}
func (w *browserWorker) lifecycle(ctx context.Context, evidence string, s *automation.Service) error {
	ctx, stop := context.WithTimeout(ctx, 60*time.Second)
	defer stop()
	suffix := rand.Text()
	a, b, child := "life-A-"+suffix, "life-B-"+suffix, "life-child-"+suffix
	for _, id := range []string{a, b, child} {
		v := map[string]any{"action": "create", "session": id}
		if id == child {
			v["parent"] = a
		}
		if _, err := w.call(ctx, v); err != nil {
			return err
		}
	}
	l, err := listenFixture()
	if err != nil {
		return err
	}
	server := &http.Server{Handler: http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(rw, `<!doctype html><title>Lifecycle</title><h1>Lifecycle %s</h1><label>Files<input type="file" multiple aria-label="Files" onchange="document.querySelector('#files').textContent=[...this.files].map(f=>f.name+':'+f.size).join('|')"></label><p id="files" role="status">No files</p>`, r.URL.Path)
	})}
	go server.Serve(l)
	defer server.Close()
	base := "http://" + l.Addr().String()
	sequence := 0
	call := func(id, tool string, args map[string]any) (json.RawMessage, error) {
		sequence++
		raw, err := w.call(ctx, map[string]any{"action": "call", "id": rand.Text(), "session": id, "tool": "my_browser_" + tool, "arguments": args})
		if len(raw) > 0 {
			_ = os.WriteFile(filepath.Join(evidence, fmt.Sprintf("lifecycle-%02d-%s-%s.json", sequence, id, tool)), raw, 0600)
		}
		return raw, err
	}
	for _, id := range []string{a, b} {
		if _, err = call(id, "navigate", map[string]any{"url": base + "/" + id}); err != nil {
			return err
		}
	}
	if _, err = w.call(ctx, map[string]any{"action": "dispose", "session": child}); err != nil {
		return err
	}
	if _, err = call(a, "snapshot", map[string]any{}); err != nil {
		return fmt.Errorf("child dispose revoked top: %w", err)
	}
	// Exact 256 KiB total upload budget across two real files. An over-budget
	// attempt must leave the chooser available for the subsequent valid upload.
	files := []string{filepath.Join(filepath.Dir(evidence), "boundary-a.bin"), filepath.Join(filepath.Dir(evidence), "boundary-b.bin")}
	for _, file := range files {
		if err = os.WriteFile(file, make([]byte, 128<<10), 0600); err != nil {
			return err
		}
	}
	over := filepath.Join(filepath.Dir(evidence), "over-budget.bin")
	if err = os.WriteFile(over, make([]byte, (256<<10)+1), 0600); err != nil {
		return err
	}
	if _, err = call(a, "click", map[string]any{"target": "getByLabel('Files', { exact: true })"}); err != nil {
		return err
	}
	raw, err := call(a, "file_upload", map[string]any{"paths": []string{over}})
	if err == nil || !strings.Contains(string(raw), "BROWSER_UPLOAD_TOO_LARGE") {
		return errors.New("over-budget upload was not explicitly refused")
	}
	if _, err = call(a, "file_upload", map[string]any{"paths": files}); err != nil {
		return fmt.Errorf("exact-budget multi-file upload failed: %w", err)
	}
	raw, err = call(a, "snapshot", map[string]any{})
	if err != nil || !strings.Contains(string(raw), "boundary-a.bin:131072|boundary-b.bin:131072") {
		return fmt.Errorf("actual multi-file sizes not observed: %v", err)
	}
	w.mu.Lock()
	initial := len(w.resources)
	w.mu.Unlock()
	id := "cancel-" + suffix
	done := make(chan error, 1)
	started := time.Now()
	go func() {
		_, e := w.call(ctx, map[string]any{"action": "call", "id": id, "session": a, "tool": "my_browser_wait_for", "arguments": map[string]any{"time": 2}})
		done <- e
	}()
	time.Sleep(250 * time.Millisecond)
	raw, err = w.call(ctx, map[string]any{"action": "cancel", "id": id})
	if err != nil {
		return err
	}
	var cancelled struct {
		Cancelled bool `json:"cancelled"`
	}
	if json.Unmarshal(raw, &cancelled) != nil || !cancelled.Cancelled {
		return errors.New("cancellation did not reach active real DSH call")
	}
	select {
	case err = <-done:
	case <-ctx.Done():
		return ctx.Err()
	}
	if err == nil {
		return errors.New("cancelled call unexpectedly succeeded")
	}
	elapsed := time.Since(started)
	if elapsed < 1500*time.Millisecond || elapsed > 8*time.Second {
		return fmt.Errorf("single-call cancellation did not drain bounded action: %s", elapsed)
	}
	if _, err = call(a, "snapshot", map[string]any{}); err != nil {
		return fmt.Errorf("single cancellation destroyed resource: %w", err)
	}
	w.mu.Lock()
	sameCount := len(w.resources) == initial
	var previous []automation.Request
	for _, r := range w.resources {
		previous = append(previous, r.request)
	}
	w.mu.Unlock()
	if !sameCount {
		return errors.New("single cancellation recreated a resource")
	}
	if _, err = s.SetEnabled(ctx, automation.Browser, false); err != nil {
		return err
	}
	if !w.allResourcesClosed() {
		return errors.New("capability off retained owned resources")
	}
	if err = w.awaitSchemas(ctx, a, 0); err != nil {
		return err
	}
	if _, err = s.SetEnabled(ctx, automation.Browser, true); err != nil {
		return err
	}
	if err = w.awaitSchemas(ctx, a, 23); err != nil {
		return err
	}
	for _, r := range previous {
		if _, err = s.Execute(ctx, r); !errors.Is(err, automation.ErrRevoked) {
			return errors.New("old epoch request revived after capability on")
		}
	}
	if _, err = call(a, "navigate", map[string]any{"url": base + "/recovered"}); err != nil {
		return err
	}
	// Kill only this exact Worker PID, not its Job or the browser/native hosts.
	process, err := os.FindProcess(int(w.worker.Diagnostics().PID))
	if err != nil {
		return err
	}
	if err = process.Kill(); err != nil {
		return err
	}
	if err = waitFor(ctx, func() bool { return !s.Snapshot().GatewayConnected && w.allResourcesClosed() }); err != nil {
		return fmt.Errorf("Worker root exit did not revoke resources: %w", err)
	}
	return os.WriteFile(filepath.Join(evidence, "lifecycle-drain.json"), []byte(fmt.Sprintf(`{"cancelledCallMilliseconds":%d,"exactBudgetMultiFileUpload":true,"oversizeUploadRefused":true,"childDisposeKeptTop":true,"offClosedResources":true,"oldEpochRefused":true,"workerRootKilled":true,"resourcesClosed":true}`, elapsed.Milliseconds())), 0600)
}
func (w *browserWorker) recoverAfterWorkerExit(ctx context.Context) error {
	id := "after-worker-" + rand.Text()
	if _, err := w.call(ctx, map[string]any{"action": "create", "session": id}); err != nil {
		return err
	}
	bounded, stop := context.WithTimeout(ctx, 15*time.Second)
	defer stop()
	if err := w.awaitSchemas(bounded, id, 23); err != nil {
		return err
	}
	raw, err := w.call(bounded, map[string]any{"action": "call", "id": rand.Text(), "session": id, "tool": "my_browser_snapshot", "arguments": map[string]any{}})
	if err != nil {
		return err
	}
	if !strings.Contains(string(raw), "about:blank") {
		return errors.New("new Worker did not create its own fresh browser resource")
	}
	return nil
}
