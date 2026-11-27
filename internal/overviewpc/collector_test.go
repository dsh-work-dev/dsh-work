package overviewpc

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
)

func TestCPUCounterDelta(t *testing.T) {
	before := cpu.TimesStat{User: 10, System: 5, Idle: 85}
	after := cpu.TimesStat{User: 15, System: 10, Idle: 95}
	if got := cpuDelta(before, after); got == nil || *got != 50 {
		t.Fatalf("CPU delta = %v", got)
	}
	if got := cpuDelta(before, before); got != nil {
		t.Fatalf("non-advancing counters fabricated %v", *got)
	}
	if got := cpuDelta(after, before); got != nil {
		t.Fatal("reset counters produced a value")
	}
}

func TestHistoryRetainsOnlyObservedHour(t *testing.T) {
	now := time.Now()
	points := []Point{{At: now.Add(-time.Hour - time.Second)}, {At: now.Add(-time.Hour)}, {At: now}}
	got := retainHistory(points, now)
	if len(got) != 2 || !got[0].At.Equal(now.Add(-time.Hour)) {
		t.Fatalf("retained %#v", got)
	}
	if got := retainHistory([]Point{{At: now}}, now); len(got) != 1 {
		t.Fatal("history padded unobserved time")
	}
}

func TestSamplerInitialUnknownCPUAndDetachedSnapshots(t *testing.T) {
	c := New(Config{DataPath: t.TempDir(), Interval: time.Hour, OwnedProcesses: func(context.Context) (Ownership, error) { return Ownership{}, nil }})
	defer c.Close()
	s, err := c.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if s.CPUPercent != nil {
		t.Fatal("first machine CPU sample is not warmed")
	}
	if s.Memory == nil || s.Disk == nil {
		t.Fatalf("real machine metrics unavailable: %v", s.Errors)
	}
	if len(s.History) != 1 {
		t.Fatalf("initial history contains %d points", len(s.History))
	}
	if s.Agents.MemoryBytes == nil || *s.Agents.MemoryBytes != 0 {
		t.Fatal("confirmed empty Worker must show zero memory")
	}
	s.Memory.UsedBytes = 0
	s.History[0].At = time.Time{}
	s.Errors["injected"] = "mutation"
	next, _ := c.Snapshot(context.Background())
	if next.History[0].At.IsZero() || next.Errors["injected"] != "" {
		t.Fatal("caller mutated sampler state")
	}
}

func TestOwnershipUnavailableNeverFabricatesZero(t *testing.T) {
	c := &Collector{config: Config{OwnedProcesses: func(context.Context) (Ownership, error) { return Ownership{}, errors.New("job query failed") }}}
	got := c.sampleAgents(context.Background(), time.Now())
	if got.CPUPercent != nil || got.MemoryBytes != nil || got.Error == "" {
		t.Fatalf("unavailable ownership = %#v", got)
	}
}

func TestOwnershipChangeRejectsSample(t *testing.T) {
	calls := 0
	c := &Collector{config: Config{OwnedProcesses: func(context.Context) (Ownership, error) {
		calls++
		return Ownership{Generation: string(rune('a' + calls))}, nil
	}}}
	got := c.sampleAgents(context.Background(), time.Now())
	if got.CPUPercent != nil || got.MemoryBytes != nil || got.Error == "" {
		t.Fatalf("changed ownership accepted: %#v", got)
	}
}

func TestMembershipChurnWithinGenerationKeepsAgentTotals(t *testing.T) {
	self := int32(os.Getpid())
	calls := 0
	c := &Collector{processes: map[int32]processCounter{}, config: Config{OwnedProcesses: func(context.Context) (Ownership, error) {
		calls++
		// A short-lived tool appears in one listing but not the next.
		if calls%2 == 0 {
			return Ownership{Generation: "g", PIDs: []int32{self}}, nil
		}
		return Ownership{Generation: "g", PIDs: []int32{self, 999999}}, nil
	}}}
	start := time.Now()
	c.sampleAgents(context.Background(), start)
	c.previousAt = start
	got := c.sampleAgents(context.Background(), start.Add(time.Second))
	if got.Error != "" || got.CPUPercent == nil || got.MemoryBytes == nil {
		t.Fatalf("tool churn made agent totals unavailable: %#v", got)
	}
}

func TestRolesNameWorkerShellsAndTools(t *testing.T) {
	processes := []Process{
		{PID: 9, Parent: 1, Name: "cmd.exe"},
		{PID: 10, Parent: 9, Name: "node.exe"},
		{PID: 11, Parent: 10, Name: "cmd.exe"},
		{PID: 12, Parent: 11, Name: "conhost.exe"},
		{PID: 13, Parent: 10, Name: "node.exe"},
		{PID: 14, Parent: 11, Name: "go.exe"},
		{PID: 15, Parent: 10, Name: "msedge.exe"},
		{PID: 16, Parent: 11, Name: "node.exe"},
	}
	assignRoles(processes)
	want := []string{RoleWorker, RoleWorker, RoleShell, RoleConsole, RoleMCP, RoleTool, RoleBrowser, RoleMCP}
	for i, p := range processes {
		if p.Role != want[i] {
			t.Fatalf("%d %s role = %q, want %q", p.PID, p.Name, p.Role, want[i])
		}
	}
}
