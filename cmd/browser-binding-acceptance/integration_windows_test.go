//go:build windows

package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/automation"
)

func TestLiveIntegrationWorkerPreflight(t *testing.T) {
	if os.Getenv("DSH_WORK_LIVE_TEST") != "1" {
		t.Skip("explicit real rc.5 fixture preflight")
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
	config, _ := json.Marshal(integrationConfig{Node: node, CLI: filepath.Join(repo, "tools/dsh/node_modules/@deepseek-ai/dsh/lib/bin.js"), Dependencies: filepath.Join(root, "unused-no-browser")})
	if err = os.WriteFile(filepath.Join(root, "integration-config.json"), config, 0600); err != nil {
		t.Fatal(err)
	}
	service, _, err := automation.NewDevelopmentAcceptance(root, readyDependencies{})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	created := make(chan map[string]any, 1)
	w, err := startBrowserWorker(ctx, root, service, func(kind string, fields map[string]any) {
		if kind == "browser-resource-create" {
			created <- fields
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if _, err = w.call(ctx, map[string]any{"action": "create", "session": "preflight-top"}); err != nil {
		t.Fatal(err)
	}
	var schemas []struct {
		Name string `json:"name"`
	}
	for i := 0; i < 100; i++ {
		data, e := w.call(ctx, map[string]any{"action": "schemas", "session": "preflight-top"})
		if e != nil {
			t.Fatal(e)
		}
		if json.Unmarshal(data, &schemas) != nil {
			t.Fatalf("invalid actual schema response: %s", data)
		}
		if len(schemas) > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(schemas) != 23 {
		t.Fatalf("actual schema count %d, expected 23", len(schemas))
	}
	for _, schema := range schemas {
		if strings.Contains(schema.Name, "evaluate") || strings.Contains(schema.Name, "run_code") {
			t.Fatal("script tool exposed")
		}
	}
	_, err = w.call(ctx, map[string]any{"action": "call", "id": "offline-call", "session": "preflight-top", "tool": "my_browser_snapshot", "arguments": map[string]any{}})
	if err == nil || !strings.Contains(err.Error(), "BROWSER_OFFLINE") {
		t.Fatalf("missing profile did not fail before resource creation: %v", err)
	}
	select {
	case request := <-created:
		if request["workingDirectory"] != root {
			t.Fatalf("trusted top cwd did not reach Go Factory: %v", request)
		}
	default:
		t.Fatal("formal Factory was not reached")
	}
	t.Log("real exec.agent -> authenticated host -> formal Factory refused missing selected instance; no browser or model calls")
}
