//go:build darwin && cgo && e67nativefixture

package macowner

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/supervisor"
	"github.com/local/dsh-work/internal/workeripc"
)

// One main-path checkpoint: real kernel restrictions, native PTY denial,
// ordinary network/files, and two actual fixed DSH versions with full retirement.
func TestLiveMacRestrictedDSH(t *testing.T) {
	manifest, cli := os.Getenv("DSH_WORK_CEF_MAC_PUBLICATION"), os.Getenv("DSH_WORK_EXECUTABLE")
	if manifest == "" || cli == "" {
		t.Fatal("explicit publication and locked DSH executable required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()
	p, err := OpenPublication(ctx, manifest)
	if err != nil {
		t.Fatal(err)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	owner, err := NewRestrictedDSHAdapter(p, node, cli, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	host, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("network-ok")) }))
	defer web.Close()
	script := filepath.Join(t.TempDir(), "kernel-check.mjs")
	source := `import fs from 'node:fs';import cp from 'node:child_process';import {createRequire} from 'node:module';
const r=createRequire(process.argv[2]);const ptyBackend=r('node-pty');const options={name:'xterm',cols:80,rows:24,cwd:process.cwd(),env:{PATH:process.env.PATH}};
const baseline=ptyBackend.spawn('/bin/sh',['-c','exit 0'],options);await new Promise((resolve,reject)=>baseline.onExit(e=>e.exitCode===0?resolve():reject(Error('PTY baseline failed'))));
if(r(process.argv[3]).apply()!==true)throw Error('sandbox missing');
const spawned=cp.spawnSync('/usr/bin/true');if(spawned.error?.code!=='EPERM')throw Error('native posix_spawn not denied');
let ptyDenied=false;try{const pty=ptyBackend.spawn('/bin/sh',[],options);pty.kill()}catch{ptyDenied=true}if(!ptyDenied)throw Error('native PTY not denied');
const text=await(await fetch(process.argv[4])).text();if(text!=='network-ok')throw Error('network unavailable');
fs.writeFileSync(process.argv[5],'file-ok');if(fs.readFileSync(process.argv[5],'utf8')!=='file-ok')throw Error('files unavailable');
let execDenied=false;try{process.execve('/usr/bin/true',['true'],{})}catch(e){execDenied=e.code==='EPERM'}if(!execDenied)throw Error('native exec not denied');
console.log(JSON.stringify({kernelForkDenied:true,nativeSpawnDenied:true,nativePTYDenied:true,execDenied:true,network:true,files:true}));`
	if err = os.WriteFile(script, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, node, script, owner.cli, filepath.Join(filepath.Dir(host), "dsh-preview-sandbox.node"), web.URL, filepath.Join(t.TempDir(), "file-check"))
	command.Env = os.Environ()
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("kernel checkpoint: %v: %s", err, output)
	}
	var kernel map[string]bool
	if json.Unmarshal(output, &kernel) != nil || len(kernel) != 6 {
		t.Fatalf("invalid kernel result: %s", output)
	}
	env := map[string]string{}
	for _, item := range os.Environ() {
		if k, v, ok := strings.Cut(item, "="); ok {
			env[k] = v
		}
	}
	var instances []Process
	for generation := 0; generation < 2; generation++ {
		w, err := owner.Start(ctx, supervisor.LaunchPlan{GenerationID: "restricted-check", Executable: cli, Args: []string{"--version"}, Env: env, WorkingDirectory: t.TempDir(), ExpectedOrigin: workeripc.Origin}, nil)
		if err != nil {
			if w != nil {
				_ = w.ForceStop(ctx)
				_ = w.Close()
			}
			t.Fatal(err)
		}
		select {
		case <-w.Exited():
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		d := w.Diagnostics()
		if d.Exit.Code != 0 || !strings.Contains(d.StdoutTail, "0.2.2-rc.1") {
			t.Fatalf("fixed DSH version failed: %+v", d)
		}
		if err = w.WaitEmpty(ctx); err != nil {
			t.Fatal(err)
		}
		if generation == 0 {
			// Exact self-owned filesystem failure: the process range is already
			// empty, but this attempt's run retirement cannot complete yet.
			owned := w.(*restrictedWorker)
			blocker := filepath.Join(owned.run, "fixture-cleanup-blocker")
			if err = os.WriteFile(blocker, []byte("held"), 0600); err != nil {
				t.Fatal(err)
			}
			if err = w.Close(); err == nil {
				t.Fatal("unretired run unexpectedly closed")
			}
			before, e := os.ReadDir(owner.root)
			if e != nil {
				t.Fatal(e)
			}
			retry, retryErr := owner.Start(ctx, supervisor.LaunchPlan{GenerationID: "blocked-retry", Executable: cli, Args: []string{"--version"}, Env: env, WorkingDirectory: t.TempDir(), ExpectedOrigin: workeripc.Origin}, nil)
			after, e := os.ReadDir(owner.root)
			if e != nil {
				t.Fatal(e)
			}
			if retry != nil || !errors.Is(retryErr, ErrCleanupPending) || len(before) != len(after) {
				t.Fatalf("same Runtime bypassed pending retirement: %v, %v", retry, retryErr)
			}
			if err = os.Remove(blocker); err != nil {
				t.Fatal(err)
			}
			// The next real Start must retry and finish retirement of this owner.
		} else if err = w.Close(); err != nil {
			t.Fatal(err)
		}
		original := Process{PID: d.PID, Started: d.ProcessStarted}
		actual, err := instance(original)
		if err != nil || actual.State != "gone" && actual.State != "reused" {
			t.Fatalf("original Node not gone: %+v %v", actual, err)
		}
		instances = append(instances, original)
	}
	if instances[0].PID == instances[1].PID && instances[0].Started == instances[1].Started {
		t.Fatal("second generation reused original instance")
	}
	t.Logf("kernel checks=%v; two fixed DSH generations fully retired: %+v", kernel, instances)
}
