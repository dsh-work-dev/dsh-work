//go:build darwin && cgo && e67presenter && e67nativefixture && !server

package desktopapp

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"github.com/local/dsh-work/internal/daemon"
	"github.com/local/dsh-work/internal/lifecycle"
	"github.com/local/dsh-work/internal/localipc"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// The consumer launches the original built root application. It does not
// replace its Wails host, DSH Session model, registry, or browser owner.
func TestLiveMacWailsPreviewSlot(t *testing.T) {
	app := os.Getenv("DSH_WORK_MAC_PREVIEW_EXECUTABLE")
	publication := os.Getenv("DSH_WORK_CEF_MAC_PUBLICATION")
	cli := os.Getenv("DSH_WORK_EXECUTABLE")
	out := os.Getenv("DSH_WORK_CEF_TEST_EVIDENCE")
	if os.Getenv("CI") != "true" || !filepath.IsAbs(app) || !filepath.IsAbs(publication) || !filepath.IsAbs(cli) || !filepath.IsAbs(out) {
		t.Fatal("requires explicit isolated CI root app, publication, restricted DSH CLI and evidence directory")
	}
	if entries, err := os.ReadDir(out); err == nil {
		if len(entries) != 0 {
			t.Fatal("requires an empty evidence directory for this run")
		}
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.MkdirAll(out, 0700); err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp("/tmp", "dsh-mac-preview-")
	if err != nil {
		t.Fatal(err)
	}
	complete := false
	defer func() {
		if complete {
			_ = os.RemoveAll(root)
		} else {
			t.Log("private failed preview evidence retained at", root)
		}
	}()
	workspace := filepath.Join(root, "workspace")
	if err = os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	nonce := rand.Text()
	type pageState struct {
		Nonce, Input        string
		Clicks, Loads, Tick int
	}
	var mu sync.Mutex
	report := pageState{}
	loads := 0
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/report" {
			mu.Lock()
			defer mu.Unlock()
			if r.Method == http.MethodPost {
				var value pageState
				if json.NewDecoder(io.LimitReader(r.Body, 8192)).Decode(&value) != nil || value.Nonce != nonce {
					http.Error(w, "invalid fixture report", 400)
					return
				}
				value.Loads = loads
				report = value
				w.WriteHeader(204)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(report)
			return
		}
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		loads++
		mu.Unlock()
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Cache-Control", "no-store")
		fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>Formal Mac Wails CEF</title><style>body{margin:0;background:#e6f4ec;color:#143322;font:20px system-ui}h1{font:22px system-ui;position:absolute;left:20px;top:0}input{position:absolute;left:20px;top:50px;width:200px;height:40px;font:20px system-ui;box-sizing:border-box}button{position:absolute;left:20px;top:120px;width:180px;height:40px;font:18px system-ui}output{position:absolute;left:20px;top:180px}</style><h1>Formal Mac same-window page</h1><input aria-label="Fixture input" value=""><button>Click actual page</button><output>0 clicks</output><script>const nonce=%q,input=document.querySelector('input'),button=document.querySelector('button'),output=document.querySelector('output');let clicks=0,tick=0;button.onclick=()=>{output.textContent=++clicks+' clicks'};setInterval(()=>fetch('/report',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({nonce,input:input.value,clicks,tick:++tick})}),250)</script>`, nonce)
	}))
	defer page.Close()
	cfg := map[string]string{"nonce": nonce, "url": page.URL + "/", "workspace": workspace, "evidence": out}
	body, _ := json.Marshal(cfg)
	scenario := filepath.Join(root, "scenario.json")
	if err = os.WriteFile(scenario, body, 0600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{}
	for _, key := range []string{"PATH", "HOME", "USER", "LOGNAME", "TMPDIR", "LANG", "LC_ALL"} {
		if value := os.Getenv(key); value != "" {
			env[key] = value
		}
	}
	env["CI"] = "true"
	env["DSH_WORK_EMBEDDED_CHECKPOINT"] = "1"
	env["DSH_WORK_EMBEDDED_ROOT"] = root
	env["DSH_WORK_EMBEDDED_UI_INSTANCE"] = "1"
	env["DSH_WORK_CEF_MAC_PUBLICATION"] = publication
	env["DSH_WORK_EXECUTABLE"] = cli
	env["DSH_WORK_MAC_PREVIEW_SCENARIO"] = scenario
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	type child struct {
		cmd  *exec.Cmd
		done chan struct{}
		err  error
	}
	start := func(name string, args ...string) *child {
		file, e := os.OpenFile(filepath.Join(root, name+".private.log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			t.Fatal(e)
		}
		cmd := exec.Command(app, args...)
		cmd.Dir = repo
		for k, v := range env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
		cmd.Stdout = file
		cmd.Stderr = file
		if e = cmd.Start(); e != nil {
			file.Close()
			t.Fatal(name, e)
		}
		c := &child{cmd: cmd, done: make(chan struct{})}
		go func() {
			e := cmd.Wait()
			file.Close()
			c.err = e
			close(c.done)
		}()
		return c
	}
	background := start("daemon", "--daemon")
	children := []*child{background}
	var cleanupClient *daemon.Client
	defer func() {
		if !complete && cleanupClient != nil {
			quit, stop := context.WithTimeout(context.Background(), 8*time.Second)
			_ = cleanupClient.Call(quit, "HostService", "RequestQuit", "workspace", nil, nil)
			select {
			case <-background.done:
			case <-quit.Done():
			}
			stop()
		}
		for i := len(children) - 1; i >= 0; i-- {
			c := children[i]
			select {
			case <-c.done:
				continue
			default:
			}
			{
				_ = c.cmd.Process.Kill()
				select {
				case <-c.done:
				case <-time.After(5 * time.Second):
					t.Error("owned app child did not join")
				}
			}
		}
	}()
	pid := uint32(background.cmd.Process.Pid)
	started, err := localipc.ProcessStarted(pid)
	if err != nil {
		t.Fatal("actual daemon instance", err)
	}
	client := daemon.NewProcessClient(root, daemon.ProcessPeer{PID: pid, Started: started})
	cleanupClient = client
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 145*time.Second)
	defer cancel()
	var snapshot daemon.Snapshot
	for {
		callCtx, stop := context.WithTimeout(ctx, time.Second)
		e := client.JSON(callCtx, "/snapshot", nil, &snapshot)
		stop()
		if e == nil && snapshot.Protocol == daemon.Protocol && snapshot.PID == int(pid) {
			break
		}
		select {
		case <-background.done:
			t.Fatal("actual daemon exited before IPC", background.err)
		case <-ctx.Done():
			t.Fatal("actual daemon IPC", ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
	ui := start("ui", "--ui", "workspace")
	children = append(children, ui)
	var result struct {
		Passed, CloseInstanceReturned bool
		FailureStage, FailureCode     string
	}
consumerResult:
	for {
		if data, e := os.ReadFile(filepath.Join(out, "mac-preview-consumer.json")); e == nil && json.Unmarshal(data, &result) == nil {
			if !result.Passed {
				t.Fatal("actual Mac UI consumer", result.FailureStage, result.FailureCode)
			}
			break
		}
		callCtx, stop := context.WithTimeout(ctx, time.Second)
		e := client.JSON(callCtx, "/snapshot", nil, &snapshot)
		stop()
		if e == nil && snapshot.Status.State == lifecycle.StateFailed {
			t.Fatal("actual DSH preview boot failed", snapshot.Status.Phase)
		}
		select {
		case <-ui.done:
			if data, e := os.ReadFile(filepath.Join(out, "mac-preview-consumer.json")); e == nil && json.Unmarshal(data, &result) == nil && result.Passed {
				break consumerResult
			}
			t.Fatal("actual UI exited without final consumer evidence", ui.err, result.FailureStage, result.FailureCode)
		case <-ctx.Done():
			t.Fatal("actual UI consumer", ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
	if !result.CloseInstanceReturned {
		t.Fatal("actual normal CEF CloseInstance missing")
	}
	select {
	case <-ui.done:
		if ui.err != nil {
			t.Fatal("actual normal UI exit", ui.err)
		}
	case <-ctx.Done():
		t.Fatal("actual UI join", ctx.Err())
	}
	for _, name := range []string{"mac-preview-visible.png", "mac-preview-hidden.png", "mac-preview-reopened.png"} {
		file, e := os.Open(filepath.Join(out, name))
		if e != nil {
			t.Fatal(e)
		}
		image, e := png.Decode(file)
		file.Close()
		if e != nil || image.Bounds().Dx() < 100 || image.Bounds().Dy() < 100 {
			t.Fatal("actual AppKit owned-view render", name, e)
		}
	}
	quitCtx, stop := context.WithTimeout(ctx, 20*time.Second)
	quitErr := client.Call(quitCtx, "HostService", "RequestQuit", "workspace", nil, nil)
	stop()
	select {
	case <-background.done:
		if background.err != nil {
			t.Fatal("actual normal daemon exit", background.err, quitErr)
		}
	case <-ctx.Done():
		t.Fatal("actual daemon join", ctx.Err(), quitErr)
	}
	proof := map[string]any{"entry": app, "daemonPID": pid, "daemonStarted": started, "actualUIExitedNormally": true, "actualDaemonExitedNormally": true, "normalCloseInstanceReturned": true, "sameWindowEvidence": "AppKit-offscreen-owned-view-tree; no global screenshot permission requested", "qualification": "single-normal-product-consumer; no fault or Agent permission claim"}
	data, _ := json.MarshalIndent(proof, "", "  ")
	if err = os.WriteFile(filepath.Join(out, "mac-preview-entry.json"), append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	complete = true
}
