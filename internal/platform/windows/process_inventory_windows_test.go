//go:build windows

package windows

import (
	"context"
	"fmt"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/supervisor"
)

func TestProcessInventoryOwnedHelper(t *testing.T) {
	if os.Getenv("E67_INVENTORY_HELPER") != "1" {
		return
	}
	fmt.Println("inventory-owned-ready")
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		if _, err := os.Stat(os.Getenv("E67_INVENTORY_STOP")); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	os.Exit(93)
}

func TestJobProcessInventoryRetainsActualOwnedInstance(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	stop := root + "/stop"
	ready := make(chan struct{}, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	worker, err := NewJobObjectAdapter().Start(ctx, supervisor.LaunchPlan{GenerationID: "e67-real-job-inventory", Executable: exe, Args: []string{"-test.run=^TestProcessInventoryOwnedHelper$", "--", "--type=renderer", "--renderer-client-id=73"}, Env: map[string]string{"E67_INVENTORY_HELPER": "1", "E67_INVENTORY_STOP": stop}, WorkingDirectory: root, ExpectedOrigin: "http://127.0.0.1:4321"}, func(_ supervisor.OutputStream, line string) {
		if line == "inventory-owned-ready" {
			select {
			case ready <- struct{}{}:
			default:
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if e := worker.Close(); e != nil {
			t.Error(e)
		}
	}()
	select {
	case <-ready:
	case <-ctx.Done():
		t.Fatal("own helper readiness", ctx.Err())
	}
	inventory, ok := worker.(supervisor.ProcessInventory)
	if !ok {
		t.Fatal("actual Job inventory not implemented")
	}
	leases, err := inventory.OwnedProcesses(ctx)
	defer func() {
		for _, p := range leases {
			if e := p.Close(); e != nil {
				t.Error(e)
			}
		}
	}()
	if err != nil {
		t.Fatal("actual owned process inventory", len(leases), err)
	}
	d := worker.Diagnostics()
	var held supervisor.ProcessLease
	for _, lease := range leases {
		if lease.Instance().PID == d.PID {
			held = lease
		}
	}
	if held == nil {
		t.Fatal("actual root missing from its Job inventory")
	}
	actual := held.Instance()
	if actual.PID != d.PID || actual.Started == 0 || actual.Started != d.ProcessStarted {
		t.Fatal("process instance does not match actual launch", actual, d.PID, d.ProcessStarted)
	}
	expected, e1 := os.Stat(exe)
	image, e2 := os.Stat(actual.Image)
	if e1 != nil || e2 != nil || !os.SameFile(expected, image) {
		t.Fatal("owned executable identity", e1, e2)
	}
	args, err := held.Arguments(ctx)
	if err != nil || !slices.Contains(args, "--renderer-client-id=73") || !slices.Contains(args, "--type=renderer") {
		t.Fatal("held command line role projection unavailable", err)
	}
	// This parent is ours, but it does not belong to the child's Job. Reject
	// before querying its image/arguments; no unrelated user process is read.
	foreign, e := worker.(*jobWorker).openOwnedProcess(uint32(os.Getpid()))
	if foreign != nil {
		foreign.Close()
		t.Fatal("non-Job process was leased")
	}
	if e == nil {
		t.Fatal("non-Job instance was not rejected")
	}
	if err = os.WriteFile(stop, nil, 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-worker.Exited():
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if r := worker.ExitResult(); r.Code != 0 || r.Err != "" {
		t.Fatal("own helper exit", r)
	}
	if live, e := held.Alive(); e != nil || live {
		t.Fatal("held instance did not preserve actual exited state", live, e)
	}
	if _, e := held.Arguments(ctx); e == nil {
		t.Fatal("exited instance supplied new arguments")
	}
	if err = worker.WaitEmpty(ctx); err != nil {
		t.Fatal(err)
	}
	empty, e := inventory.OwnedProcesses(ctx)
	for _, p := range empty {
		p.Close()
	}
	if e != nil || len(empty) != 0 {
		t.Fatal("actual empty Job not empty", len(empty), e)
	}
	if err = held.Close(); err != nil {
		t.Fatal(err)
	}
	if _, e = held.Alive(); e == nil {
		t.Fatal("closed lease remained usable")
	}
}
