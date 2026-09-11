//go:build windows

package workeripc_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/dshactivity"
	"github.com/local/dsh-work/internal/dshadapter"
	"github.com/local/dsh-work/internal/platform"
	"github.com/local/dsh-work/internal/supervisor"
	"github.com/local/dsh-work/internal/workeripc"
	"github.com/local/dsh-work/internal/workspacecontext"
)

func TestRealWorkerPipe(t *testing.T) {
	if os.Getenv("DSH_WORK_PIPE_TEST") != "1" {
		t.Skip("set DSH_WORK_PIPE_TEST=1")
	}
	root, _ := filepath.Abs("../..")
	home := t.TempDir()
	transport, err := workeripc.New()
	if err != nil {
		t.Fatal(err)
	}
	defer transport.Close()
	activity := dshactivity.New(filepath.Join(home, "activity"))
	defer activity.Close()
	patch, err := activity.Prepare("pipe-test")
	if err != nil {
		t.Fatal(err)
	}
	patch, err = transport.Prepare(filepath.Join(home, "carrier"), filepath.Join(home, "data", "profiles", "web"), patch)
	if err != nil {
		t.Fatal(err)
	}
	deps := platform.New()
	adapter := dshadapter.New(deps.CommandExecutor, dshadapter.SupportedVersion)
	plan, err := adapter.BuildLaunchPlan(dshadapter.LaunchContext{GenerationID: "pipe-test", Runtime: dshadapter.Runtime{Path: filepath.Join(root, "tools/dsh/run-dsh.cmd"), Version: dshadapter.SupportedVersion}, BootstrapDirectory: home, DataDirectory: filepath.Join(home, "data"), Profile: "web", HostPatch: patch, Workspace: workspacecontext.Context{GenerationID: "pipe-test", State: workspacecontext.StateSelectionRequired}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	ready := make(chan string, 1)
	worker, err := deps.Supervisor.Start(ctx, plan, func(_ supervisor.OutputStream, line string) {
		if r, ok := adapter.ParseReadyAnnouncement(line); ok {
			select {
			case ready <- r.URL:
			default:
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		c, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		worker.ForceStop(c)
		worker.WaitEmpty(c)
		worker.Close()
	}()
	var auth string
	select {
	case auth = <-ready:
	case <-worker.Exited():
		t.Fatalf("Worker exited: %+v", worker.Diagnostics())
	case <-ctx.Done():
		t.Fatalf("startup timed out: %+v", worker.Diagnostics())
	}
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Transport: transport.HTTP, Jar: jar, Timeout: 10 * time.Second}
	res, err := client.Get(auth)
	if err != nil {
		t.Fatalf("request failed; worker=%+v", worker.Diagnostics())
	}
	data, err := io.ReadAll(res.Body)
	res.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 200 || !strings.Contains(string(data), "__DSH_BOOT__") {
		t.Fatalf("invalid real DSH HTML: %d %.200s", res.StatusCode, data)
	}
	activity.Start(ctx, auth, transport.HTTP)
	deadline := time.Now().Add(5 * time.Second)
	for !activity.Snapshot().Connected && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if !activity.Snapshot().Connected {
		t.Fatal("activity plugin did not connect over pipe")
	}
	request, _ := http.NewRequestWithContext(ctx, "POST", workeripc.Origin+"/.dsh/remote-stream", strings.NewReader(`{"endpoint":"missing-endpoint","payload":{}}`))
	request.Header.Set("Origin", workeripc.Origin)
	request.Header.Set("Content-Type", "application/json")
	res, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 400 {
		t.Fatalf("unknown stream endpoint: %d", res.StatusCode)
	}
	anonymous := &http.Client{Transport: transport.HTTP, Timeout: time.Second}
	request, _ = http.NewRequestWithContext(ctx, "POST", workeripc.Origin+"/.dsh/remote-stream", strings.NewReader(`{}`))
	request.Header.Set("Origin", workeripc.Origin)
	res, err = anonymous.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatalf("anonymous remote stream: %d", res.StatusCode)
	}
	encoded, _ := json.Marshal(map[string]any{"htmlBytes": len(data), "activityConnected": true, "workerPID": worker.Diagnostics().PID})
	t.Log(string(encoded))
}
