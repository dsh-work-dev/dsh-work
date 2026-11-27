//go:build windows

package workeripc_test

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Microsoft/go-winio"
	win "github.com/local/dsh-work/internal/platform/windows"
	"github.com/local/dsh-work/internal/supervisor"
	"github.com/local/dsh-work/internal/workeripc"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// This is a real child executable/OS pipe handshake, not a mocked peer identity.
func TestOwnedProcessPeerFixture(t *testing.T) {
	mode := os.Getenv("DSH_WORK_TEST_PEER_FIXTURE")
	if mode == "" {
		t.Skip("child fixture only")
	}
	if mode == "idle" {
		time.Sleep(20 * time.Second)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, e := winio.DialPipeContext(ctx, os.Getenv("DSH_WORK_BRIDGE_PIPE"))
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	if e = workeripc.AuthenticateClient(c, os.Getenv("DSH_WORK_BRIDGE_TOKEN")); e != nil {
		t.Fatal(e)
	}
	// A hostile peer can claim the expected PID, but admission uses the kernel.
	_ = json.NewEncoder(c).Encode(map[string]string{"claimedPID": os.Getenv("DSH_WORK_TEST_CLAIMED_PID")})
	_, _ = io.Copy(io.Discard, c)
}
func TestLivePipeRejectsForgedPeerAndProcessInstance(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	launch := func(mode string, carrier map[string]string) supervisor.Worker {
		t.Helper()
		env := map[string]string{}
		for _, kv := range os.Environ() {
			k, v, _ := strings.Cut(kv, "=")
			env[k] = v
		}
		for k, v := range carrier {
			env[k] = v
		}
		env["DSH_WORK_TEST_PEER_FIXTURE"] = mode
		w, e := win.NewJobObjectAdapter().Start(ctx, supervisor.LaunchPlan{GenerationID: "peer-fixture", Executable: exe, Args: []string{"-test.run=^TestOwnedProcessPeerFixture$"}, WorkingDirectory: filepath.Dir(exe), ExpectedOrigin: workeripc.Origin, Env: env}, nil)
		if e != nil {
			t.Fatal(e)
		}
		go func() {
			for range w.Events() {
			}
		}()
		t.Cleanup(func() {
			if e := w.Close(); e != nil {
				t.Errorf("fixture cleanup: %v", e)
			}
		})
		return w
	}
	expected := launch("idle", nil)
	identity := expected.Diagnostics()
	if identity.ProcessStarted == 0 {
		t.Fatal("supervisor omitted process instance")
	}
	for _, kind := range []string{"wrong-peer", "wrong-instance", "accepted-instance"} {
		t.Run(kind, func(t *testing.T) {
			transport, e := workeripc.New()
			if e != nil {
				t.Fatal(e)
			}
			defer transport.Close()
			carrier := transport.ChildEnvironment()
			carrier["DSH_WORK_TEST_CLAIMED_PID"] = fmt.Sprint(identity.PID)
			actual := launch("connect", carrier).Diagnostics()
			pid, started := actual.PID, actual.ProcessStarted
			if kind == "wrong-peer" {
				pid, started = identity.PID, identity.ProcessStarted
			}
			if kind == "wrong-instance" {
				started++
			}
			c, e := transport.ConnectProcessInstance(ctx, pid, started)
			if kind == "accepted-instance" {
				if e != nil {
					t.Fatal(e)
				}
				_ = c.Close()
			} else {
				want := "WORKER_IPC_PROCESS_REJECTED"
				if kind == "wrong-instance" {
					want = "WORKER_IPC_PROCESS_INSTANCE_REJECTED"
				}
				if e == nil || e.Error() != want {
					t.Fatalf("admission=%v want %s", e, want)
				}
			}
			t.Logf("real authenticated pipe peer pid=%d creation=%d expected pid=%d creation=%d admission=%v", actual.PID, actual.ProcessStarted, pid, started, e)
		})
	}
}
