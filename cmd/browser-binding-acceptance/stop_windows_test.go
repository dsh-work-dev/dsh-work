//go:build windows

package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/automation/browserruntime"
)

type stoppedResource struct{ started, closed chan struct{} }

func (r *stoppedResource) Execute(ctx context.Context, _ string, _ map[string]any) (any, error) {
	close(r.started)
	<-ctx.Done()
	return nil, ctx.Err()
}
func (r *stoppedResource) Close() error { close(r.closed); return nil }

// The real rc.5 Agent owns the operation signal and supplies the user cause.
// No LLM/turn simulation or browser is needed to distinguish Stop from cancel.
func TestLiveUserStopDuringOwnedAgentActivity(t *testing.T) {
	if os.Getenv("DSH_WORK_LIVE_TEST") != "1" {
		t.Skip("explicit rc.5 stop boundary check")
	}
	root := t.TempDir()
	repo, _ := filepath.Abs("../..")
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "browser-acceptance-owner.txt"), []byte("dsh-work-browser-acceptance-v1"), 0600); err != nil {
		t.Fatal(err)
	}
	config, _ := json.Marshal(integrationConfig{Node: node, CLI: filepath.Join(repo, "tools/dsh/node_modules/@deepseek-ai/dsh/lib/bin.js"), Dependencies: filepath.Join(root, "unused")})
	if err = os.WriteFile(filepath.Join(root, "integration-config.json"), config, 0600); err != nil {
		t.Fatal(err)
	}
	s, _, err := automation.NewDevelopmentAcceptance(root, readyDependencies{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, stop := context.WithTimeout(context.Background(), 35*time.Second)
	defer stop()
	w, err := startBrowserWorker(ctx, root, s, func(string, map[string]any) {})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	r := &stoppedResource{started: make(chan struct{}), closed: make(chan struct{})}
	s.ConfigureBrowserBackend(func(context.Context, automation.Request) (automation.Resource, error) { return r, nil }, browserruntime.ToolSchemas())
	id := "stop-real-agent"
	if _, err = w.call(ctx, map[string]any{"action": "create", "session": id}); err != nil {
		t.Fatal(err)
	}
	if err = w.awaitSchemas(ctx, id, 23); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := w.call(ctx, map[string]any{"action": "maintenance-call", "session": id, "id": "real-stop-call", "tool": "my_browser_wait_for", "arguments": map[string]any{"time": 10}})
		done <- err
	}()
	select {
	case <-r.started:
	case <-ctx.Done():
		t.Fatal("real browser adapter call never started")
	}
	if _, err = w.call(ctx, map[string]any{"action": "stop-agent", "session": id}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-r.closed:
	case <-time.After(3 * time.Second):
		t.Fatal("user Stop merely cancelled one call and retained the top resource")
	}
	select {
	case err = <-done:
		if err == nil {
			t.Fatal("stopped call succeeded")
		}
	case <-ctx.Done():
		t.Fatal("stopped call did not settle")
	}
}
