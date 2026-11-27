//go:build darwin && arm64 && cgo && e67nativefixture

package macowner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/automation/computerruntime"
	"github.com/local/dsh-work/internal/localipc"
	"github.com/local/dsh-work/internal/supervisor"
)

type capturedAUVAdapter struct {
	*AUVAdapter
	worker *auvWorker
}

func (a *capturedAUVAdapter) Start(ctx context.Context, plan supervisor.LaunchPlan, raw supervisor.RawOutputHandler) (supervisor.Worker, error) {
	worker, err := a.AUVAdapter.Start(ctx, plan, raw)
	a.worker, _ = worker.(*auvWorker)
	return worker, err
}

type liveAUVFixture struct {
	resource automation.Resource
	worker   *auvWorker
	state    journal
	members  []Process
}

func configureLiveAUV(t *testing.T, ctx context.Context, root string) (*AUVAdapter, string, string) {
	t.Helper()
	manifest, dependencies := os.Getenv("DSH_WORK_CEF_MAC_PUBLICATION"), os.Getenv("DSH_WORK_AUV_TEST_DEPENDENCIES")
	if manifest == "" || dependencies == "" {
		t.Skip("requires the built Mac owner publication and pinned AUV dependencies")
	}
	publication, err := OpenPublication(ctx, manifest)
	if err != nil {
		t.Fatal(err)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := NewAUVAdapter(ctx, publication, node, dependencies, root)
	if err != nil {
		t.Fatal(err)
	}
	return adapter, node, dependencies
}

func startLiveAUV(t *testing.T, ctx context.Context, root string) liveAUVFixture {
	t.Helper()
	adapter, node, dependencies := configureLiveAUV(t, ctx, root)
	owner := &capturedAUVAdapter{AUVAdapter: adapter}
	driver, err := automation.AUVExecutable(dependencies)
	if err != nil {
		t.Fatal(err)
	}
	factory := computerruntime.NewFactory(computerruntime.Config{
		Supervisor: owner, Root: root,
		Resolve: func(context.Context, string) (computerruntime.Runtime, error) {
			return computerruntime.Runtime{Node: node, Dependencies: dependencies, Driver: driver}, nil
		},
	})
	resource, err := factory(ctx, "auv")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := resource.Close(); err != nil {
			t.Error(err)
		}
	})
	if owner.worker == nil {
		t.Fatal("no authenticated AUV worker")
	}
	var members []Process
	for {
		owner.worker.mu.Lock()
		generation := owner.worker.state.Generation
		owner.worker.mu.Unlock()
		state, err := loadGeneration(owner.worker.directory, owner.worker.key, generation)
		if err != nil {
			t.Fatal(err)
		}
		for _, record := range state.Owned {
			if record.Role == "auv-native" {
				current, err := instance(record.Process)
				if err == nil && current.State == "live" {
					members = append(members, record.Process)
				}
			}
		}
		broker, guard := state.Broker, state.Owner
		if len(members) == 1 {
			if members[0].Path != driver || members[0].PPID != broker.PID || members[0].PGID != guard.PGID {
				t.Fatalf("AUV native child was not in the owned process group: %+v", members[0])
			}
			members = append(members, broker, guard)
			return liveAUVFixture{resource: resource, worker: owner.worker, state: state, members: members}
		}
		if len(members) > 1 {
			t.Fatal("multiple native servers in one AUV resource")
		}
		select {
		case <-ctx.Done():
			t.Fatal("AUV server never appeared in the authenticated census", ctx.Err())
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func assertLiveAUVGone(t *testing.T, members []Process) {
	t.Helper()
	for _, member := range members {
		actual, err := instance(member)
		if err != nil || actual.State != "gone" && actual.State != "reused" {
			t.Fatalf("owned process survived cleanup: %+v %v", actual, err)
		}
	}
}

func TestLiveMacAUVProcessOwner(t *testing.T) {
	for _, mode := range []string{"close", "adapter-killed"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			fixture := startLiveAUV(t, ctx, t.TempDir())
			if mode == "adapter-killed" {
				owner := fixture.state.Owned[processKey(fixture.state.Broker)].Target
				if owner == nil {
					t.Fatal("adapter kernel identity missing")
				}
				if err := localipc.SignalTarget(*owner, syscall.SIGKILL); err != nil {
					t.Fatal(err)
				}
			}
			if err := fixture.resource.Close(); err != nil {
				t.Fatal("close authenticated AUV process tree", err)
			}
			assertLiveAUVGone(t, fixture.members)
			if _, err := os.Stat(fixture.worker.directory); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("cleanup did not retire the resource directory", err)
			}
			if _, err := os.Stat(fixture.worker.run); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("cleanup did not retire the guarded socket directory", err)
			}
		})
	}
}

type auvDeathReady struct {
	Resource string
	Run      string
	Members  []Process
}

func TestLiveMacAUVHostLossRecovery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	if socket := os.Getenv("DSH_WORK_AUV_DEATH_SOCKET"); socket != "" {
		root := os.Getenv("DSH_WORK_AUV_DEATH_ROOT")
		fixture := startLiveAUV(t, ctx, root)
		c, err := localipc.Dial(ctx, socket)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		peer, err := localipc.Peer(c)
		expectedPID, _ := strconv.ParseUint(os.Getenv("DSH_WORK_AUV_DEATH_PID"), 10, 32)
		expectedStart, _ := strconv.ParseUint(os.Getenv("DSH_WORK_AUV_DEATH_STARTED"), 10, 64)
		if err != nil || peer.PID != uint32(expectedPID) || peer.Started != expectedStart {
			t.Fatal("unexpected host controller", err)
		}
		if err := writeFrame(c, auvDeathReady{Resource: filepath.Dir(fixture.worker.directory), Run: fixture.worker.run, Members: fixture.members}); err != nil {
			t.Fatal(err)
		}
		<-ctx.Done()
		t.Fatal("fixture host did not receive its controlled death")
	}

	root, err := os.MkdirTemp(localipc.SocketTempRoot(), "dsh-auv-death-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if t.Failed() {
			t.Log("retained private owner evidence", root)
		} else {
			_ = os.RemoveAll(root)
		}
	})
	adapter, _, _ := configureLiveAUV(t, ctx, root)
	controller, err := os.MkdirTemp(localipc.SocketTempRoot(), "dsh-auv-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(controller)
	socket := filepath.Join(controller, "ctl.sock")
	listener, err := localipc.Listen(socket, true)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	rollback := context.AfterFunc(ctx, func() { _ = listener.Close() })
	defer rollback()
	pid := uint32(os.Getpid())
	started, err := localipc.ProcessStarted(pid)
	if err != nil {
		t.Fatal(err)
	}
	host, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.CommandContext(ctx, host, "-test.run", "^TestLiveMacAUVHostLossRecovery$", "-test.v")
	child.Stdout, child.Stderr = os.Stdout, os.Stderr
	child.Env = append(os.Environ(), "DSH_WORK_AUV_DEATH_SOCKET="+socket, "DSH_WORK_AUV_DEATH_ROOT="+root,
		fmt.Sprintf("DSH_WORK_AUV_DEATH_PID=%d", pid), fmt.Sprintf("DSH_WORK_AUV_DEATH_STARTED=%d", started))
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = child.Process.Kill(); _ = child.Wait() }()
	c, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	target, err := localipc.TargetFromConn(c)
	if err != nil || target.PID != uint32(child.Process.Pid) {
		t.Fatal("unexpected fixture host", err)
	}
	deadline, _ := ctx.Deadline()
	_ = c.SetDeadline(deadline)
	var ready auvDeathReady
	if err := readFrame(c, &ready); err != nil || len(ready.Members) != 3 {
		t.Fatal("actual fixture engine was not ready before host loss", err)
	}
	nextManifest := os.Getenv("DSH_WORK_TEST_NEXT_CEF_PUBLICATION")
	var next Publication
	if nextManifest != "" {
		next, err = OpenPublication(ctx, nextManifest)
		if err != nil {
			t.Fatal(err)
		}
		if next.Owner == adapter.publication.Owner || sameJobFile(next.Owner, adapter.publication.Owner) {
			t.Fatal("recovery fixture requires distinct owner publications")
		}
		successor, err := NewAUVAdapter(ctx, next, adapter.node, adapter.dependencies, root)
		if err != nil {
			t.Fatal(err)
		}
		bounded, stop := context.WithTimeout(ctx, 350*time.Millisecond)
		err = successor.RecoverResource(bounded, ready.Resource)
		stop()
		if !errors.Is(err, ErrCleanupPending) {
			t.Fatalf("live predecessor was not protected from replacement: %v", err)
		}
		for _, member := range ready.Members {
			actual, err := instance(member)
			if err != nil || actual.State != "live" {
				t.Fatalf("recovery disturbed a live predecessor process: %+v (%v)", actual, err)
			}
		}
	}
	if err := localipc.SignalTarget(target, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	var exit *exec.ExitError
	err = child.Wait()
	if !errors.As(err, &exit) || exit.ProcessState.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
		t.Fatal("fixture host was not forcefully terminated", err)
	}
	if nextManifest != "" {
		t.Setenv("DSH_WORK_CEF_MAC_PUBLICATION", nextManifest)
	}
	reopened := startLiveAUV(t, ctx, root)
	if next.Owner != "" && reopened.state.Owner.Path != next.Owner {
		t.Fatal("successor did not start the replacement publication")
	}
	assertLiveAUVGone(t, ready.Members)
	if _, err := os.Stat(ready.Run); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("restart retained the old guarded AUV endpoint", err)
	}
	if err := adapter.RecoverResource(ctx, ready.Resource); err != nil {
		t.Fatal("repeat authenticated AUV recovery", err)
	}
	if err := reopened.resource.Close(); err != nil {
		t.Fatal(err)
	}
	assertLiveAUVGone(t, reopened.members)
}
