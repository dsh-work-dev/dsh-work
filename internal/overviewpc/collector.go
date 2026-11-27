package overviewpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/process"
)

type processCounter struct {
	created int64
	seconds float64
}

type Collector struct {
	config             Config
	mu                 sync.RWMutex
	snapshot           Snapshot
	cancel             context.CancelFunc
	done               chan struct{}
	previous           *cpu.TimesStat
	previousAt         time.Time
	previousGeneration string
	processes          map[int32]processCounter
}

// New starts one sampler. Its lifetime belongs to the daemon, not an open page.
// The initial sample contains real memory/disk values and an unwarmed CPU value.
func New(config Config) *Collector {
	if config.Interval <= 0 {
		config.Interval = 5 * time.Second
	}
	ctx, cancel := context.WithCancel(context.Background())
	c := &Collector{config: config, cancel: cancel, done: make(chan struct{}), processes: make(map[int32]processCounter)}
	c.sample(ctx, time.Now())
	go func() {
		defer close(c.done)
		ticker := time.NewTicker(config.Interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				c.sample(ctx, time.Now())
			}
		}
	}()
	return c
}

func (c *Collector) Close() error { c.cancel(); <-c.done; return nil }

// Snapshot returns a detached view; callers cannot mutate retained history.
// Collection errors are per-field so the healthy machine metrics remain usable.
func (c *Collector) Snapshot(ctx context.Context) (Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	data, err := json.Marshal(c.snapshot)
	if err != nil {
		return Snapshot{}, err
	}
	var result Snapshot
	err = json.Unmarshal(data, &result)
	return result, err
}

func (c *Collector) sample(ctx context.Context, at time.Time) {
	s := Snapshot{SampledAt: at, Errors: make(map[string]string)}
	times, err := cpu.TimesWithContext(ctx, false)
	if err != nil || len(times) != 1 {
		if err == nil {
			err = errors.New("machine CPU counters unavailable")
		}
		s.Errors["cpu"] = err.Error()
		c.previous = nil
	} else {
		if c.previous != nil {
			s.CPUPercent = cpuDelta(*c.previous, times[0])
		}
		if c.previous != nil && s.CPUPercent == nil {
			s.Errors["cpu"] = "CPU counters did not advance"
		}
		c.previous = &times[0]
	}
	if value, err := mem.VirtualMemoryWithContext(ctx); err != nil {
		s.Errors["memory"] = err.Error()
	} else if value.Total == 0 {
		s.Errors["memory"] = "memory capacity unavailable"
	} else {
		s.Memory = &Usage{UsedBytes: value.Used, TotalBytes: value.Total, Percent: value.UsedPercent}
	}
	if c.config.DataPath == "" {
		s.Errors["disk"] = "data directory unavailable"
	} else if value, err := disk.UsageWithContext(ctx, c.config.DataPath); err != nil {
		s.Errors["disk"] = err.Error()
	} else if value.Total == 0 {
		s.Errors["disk"] = "disk capacity unavailable"
	} else {
		s.Disk = &DiskUsage{Usage: Usage{UsedBytes: value.Used, TotalBytes: value.Total, Percent: value.UsedPercent}, Path: c.config.DataPath}
	}
	s.Agents = c.sampleAgents(ctx, at)
	point := Point{At: at, CPUPercent: s.CPUPercent, AgentCPUPercent: s.Agents.CPUPercent, AgentMemoryBytes: s.Agents.MemoryBytes}
	if s.Memory != nil {
		value := s.Memory.Percent
		point.MemoryPercent = &value
	}
	if s.Disk != nil {
		value := s.Disk.Percent
		point.DiskPercent = &value
	}
	c.mu.Lock()
	s.History = append(c.snapshot.History, point)
	s.History = retainHistory(s.History, at)
	c.snapshot = s
	c.mu.Unlock()
	c.previousAt = at
}

func retainHistory(points []Point, at time.Time) []Point {
	cutoff := at.Add(-time.Hour)
	first := 0
	for first < len(points) && points[first].At.Before(cutoff) {
		first++
	}
	return append([]Point(nil), points[first:]...)
}

func cpuDelta(before, after cpu.TimesStat) *float64 {
	// Guest time is already included in Linux user/nice counters.
	total := func(t cpu.TimesStat) float64 {
		return t.User + t.System + t.Idle + t.Nice + t.Iowait + t.Irq + t.Softirq + t.Steal
	}
	delta := total(after) - total(before)
	idle := (after.Idle + after.Iowait) - (before.Idle + before.Iowait)
	if delta <= 0 || idle < 0 || idle > delta {
		return nil
	}
	value := math.Max(0, math.Min(100, 100*(delta-idle)/delta))
	return &value
}

func (c *Collector) sampleAgents(ctx context.Context, at time.Time) AgentUsage {
	result := AgentUsage{Processes: []Process{}}
	fail := func(err error) AgentUsage {
		c.processes = make(map[int32]processCounter)
		c.previousGeneration = ""
		return AgentUsage{Processes: []Process{}, Error: err.Error()}
	}
	if c.config.OwnedProcesses == nil {
		return fail(errors.New("Worker ownership source unavailable"))
	}
	owner, err := c.config.OwnedProcesses(ctx)
	if err != nil {
		return fail(err)
	}
	if len(owner.PIDs) > 0 && owner.Generation == "" {
		return fail(errors.New("Worker generation unavailable"))
	}
	next := make(map[int32]processCounter)
	var memory uint64
	var percentage float64
	completeMemory, completeCPU := true, true
	for _, pid := range owner.PIDs {
		if _, duplicate := next[pid]; duplicate {
			continue
		}
		p := Process{PID: pid}
		proc, err := process.NewProcessWithContext(ctx, pid)
		if err != nil {
			// A process that exited since the Job listing is gone, not unmeasured.
			if errors.Is(err, process.ErrorProcessNotRunning) || errors.Is(err, os.ErrNotExist) {
				continue
			}
			p.Error = err.Error()
			completeMemory = false
			completeCPU = false
			result.Processes = append(result.Processes, p)
			continue
		}
		p.Name, _ = proc.NameWithContext(ctx)
		p.Parent, _ = proc.PpidWithContext(ctx)
		created, createErr := proc.CreateTimeWithContext(ctx)
		counter, cpuErr := proc.TimesWithContext(ctx)
		if createErr == nil && cpuErr == nil {
			now := processCounter{created: created, seconds: counter.User + counter.System}
			next[pid] = now
			previous, ok := c.processes[pid]
			elapsed := at.Sub(c.previousAt).Seconds()
			sameGeneration := owner.Generation == c.previousGeneration && !c.previousAt.IsZero() && elapsed > 0
			var used float64
			measured := false
			switch {
			case sameGeneration && ok && previous.created == created && now.seconds >= previous.seconds:
				used, measured = now.seconds-previous.seconds, true
			case sameGeneration && created >= c.previousAt.UnixMilli():
				// Started since the last sample: all of its CPU time falls inside this interval.
				used, measured = now.seconds, true
			}
			if measured {
				value := math.Min(100, 100*used/elapsed/float64(runtime.NumCPU()))
				p.CPUPercent = &value
				percentage += value
			} else {
				completeCPU = false
			}
		} else {
			completeCPU = false
			p.Error = fmt.Sprint(errors.Join(createErr, cpuErr))
		}
		if info, err := proc.MemoryInfoWithContext(ctx); err != nil {
			completeMemory = false
			if p.Error == "" {
				p.Error = err.Error()
			} else {
				p.Error += "; " + err.Error()
			}
		} else {
			value := info.RSS
			p.MemoryBytes = &value
			memory += value
		}
		result.Processes = append(result.Processes, p)
	}
	// Membership is re-read after OS counters: a Worker restart must not attribute another
	// generation's processes to this one. Processes that start or exit within one generation are
	// ordinary tool churn; creation times above already keep a recycled PID from inheriting counters.
	confirmed, err := c.config.OwnedProcesses(ctx)
	if err != nil {
		return fail(err)
	}
	if confirmed.Generation != owner.Generation {
		return fail(errors.New("Worker generation changed during sampling"))
	}
	assignRoles(result.Processes)
	c.processes = next
	c.previousGeneration = owner.Generation
	if completeMemory {
		result.MemoryBytes = &memory
	}
	if completeCPU {
		percentage = math.Min(100, percentage)
		result.CPUPercent = &percentage
	}
	return result
}

// assignRoles names each Job process by what it is to the user. The Worker is the first node
// process down the Job tree; a launcher shell above it is part of starting DSH, not an agent shell.
func assignRoles(processes []Process) {
	byPID := make(map[int32]*Process, len(processes))
	for i := range processes {
		byPID[processes[i].PID] = &processes[i]
	}
	base := func(p *Process) string { return strings.TrimSuffix(strings.ToLower(p.Name), ".exe") }
	// launcher reports whether every Job ancestor of p is a shell or console, i.e. nothing ran it but the launch chain.
	launcher := func(p *Process) bool {
		for depth, parent := 0, byPID[p.Parent]; parent != nil && depth < len(processes); depth, parent = depth+1, byPID[parent.Parent] {
			if name := base(parent); name != "cmd" && name != "conhost" && name != "powershell" && name != "pwsh" && name != "sh" && name != "bash" {
				return false
			}
		}
		return true
	}
	for i := range processes {
		p := &processes[i]
		name := base(p)
		switch {
		case p.Name == "":
		case name == "node" && launcher(p):
			p.Role = RoleWorker
		case byPID[p.Parent] == nil:
			// The Job root that starts the Worker.
			p.Role = RoleWorker
		case name == "conhost" || name == "openconsole":
			p.Role = RoleConsole
		case name == "cmd" || name == "powershell" || name == "pwsh" || name == "bash" || name == "sh" || name == "zsh":
			p.Role = RoleShell
		case name == "msedge" || name == "chrome" || name == "chromium" || name == "firefox":
			p.Role = RoleBrowser
		case name == "node" || name == "npx" || name == "uvx" || name == "python" || name == "uv":
			p.Role = RoleMCP
		default:
			p.Role = RoleTool
		}
	}
}
