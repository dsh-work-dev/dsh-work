package embeddedbrowser

import (
	"context"
	"errors"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/local/dsh-work/internal/supervisor"
)

type RendererInstance struct {
	Child   uint32 `json:"logicalChild"`
	PID     uint32 `json:"pid"`
	Started uint64 `json:"started"`
	Exited  bool   `json:"exited"`
}
type RendererInfluence struct {
	FaultEpoch    uint64             `json:"faultEpoch"`
	GraphRevision uint64             `json:"graphRevision"`
	Complete      bool               `json:"complete"`
	Processes     []RendererInstance `json:"processes"`
	Frames        []FrameRecord      `json:"frames"`
}
type rendererLease struct {
	process  supervisor.ProcessLease
	identity RendererInstance
}
type rendererFault struct {
	influence RendererInfluence
	process   *rendererLease
	eligible  bool
	resolved  bool
}

type rendererTracker struct {
	broker           *Broker
	inventory        supervisor.ProcessInventory
	image            os.FileInfo
	stop, joined     chan struct{}
	update           chan struct{}
	once             sync.Once
	mu               sync.Mutex
	revision, epoch  uint64
	complete         bool
	established      bool
	artifactRetiring map[string]bool
	joinUncertain    bool
	conflicted       map[uint32]bool
	faults           map[uint64]*rendererFault
	held             map[uint32]*rendererLease
	frames           []FrameRecord
	retired          []supervisor.ProcessLease
	cleanupErr       error
}

func newRendererTracker(b *Broker, executable string) *rendererTracker {
	inventory, ok := b.worker.(supervisor.ProcessInventory)
	if !ok {
		return nil
	}
	image, err := os.Stat(executable)
	if err != nil {
		return nil
	}
	tracker := &rendererTracker{broker: b, inventory: inventory, image: image, stop: make(chan struct{}), joined: make(chan struct{}), update: make(chan struct{}, 1), held: map[uint32]*rendererLease{}}
	go tracker.run()
	return tracker
}
func (r *rendererTracker) notify() {
	select {
	case r.update <- struct{}{}:
	default:
	}
}
func (r *rendererTracker) run() {
	defer close(r.joined)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-r.stop:
			return
		case <-r.update:
			r.refresh()
		case <-ticker.C:
			r.refresh()
		}
	}
}
func rendererChild(args []string) (uint32, bool, error) {
	kind := ""
	id := ""
	kindSeen, idSeen := false, false
	for _, arg := range args {
		if value, ok := strings.CutPrefix(arg, "--type="); ok {
			if kindSeen {
				return 0, false, errors.New("CEF_RENDERER_ROLE_AMBIGUOUS")
			}
			kindSeen = true
			kind = value
		}
		if value, ok := strings.CutPrefix(arg, "--renderer-client-id="); ok {
			if idSeen {
				return 0, false, errors.New("CEF_RENDERER_CHILD_AMBIGUOUS")
			}
			idSeen = true
			id = value
		}
	}
	if kind != "renderer" {
		return 0, false, nil
	}
	child, err := strconv.ParseUint(id, 10, 31)
	if err != nil || child == 0 || strconv.FormatUint(child, 10) != id {
		return 0, false, errors.New("CEF_RENDERER_CHILD_UNAVAILABLE")
	}
	return uint32(child), true, nil
}
func (r *rendererTracker) refresh() {
	snapshot := r.broker.frameGraphOnly()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	leases, inventoryErr := r.inventory.OwnedProcesses(ctx)
	candidates := map[uint32]supervisor.ProcessLease{}
	complete := inventoryErr == nil && snapshot.TopologyComplete
	uncertain := inventoryErr != nil
	reason := "CEF_RENDERER_CENSUS_UNCERTAIN"

	var retired []supervisor.ProcessLease
	var cleanupErr error
	release := func(lease supervisor.ProcessLease) {
		if err := lease.Close(); err != nil {
			complete = false
			retired = append(retired, lease)
			if cleanupErr == nil {
				cleanupErr = err
			}
		}
	}
	// An already joined kernel instance does not need argv re-read as it
	// terminates: its immutable role/child association was verified while live.
	r.mu.Lock()
	known := map[supervisor.ProcessInstance]uint32{}
	for child, held := range r.held {
		known[held.process.Instance()] = child
	}
	r.mu.Unlock()
	ambiguous := map[uint32]bool{}
	for _, lease := range leases {
		if child, ok := known[lease.Instance()]; ok {
			if old := candidates[child]; old != nil {
				complete = false
				uncertain = true
				release(old)
				release(lease)
				delete(candidates, child)
				ambiguous[child] = true
			} else if ambiguous[child] {
				release(lease)
			} else {
				candidates[child] = lease
			}
			continue
		}
		args, err := lease.Arguments(ctx)
		if err != nil {
			reason = "CEF_RENDERER_ARGUMENTS_UNCERTAIN"
			complete = false
			uncertain = true
			release(lease)
			continue
		}
		child, renderer, err := rendererChild(args)
		clear(args)
		if err != nil {
			complete = false
			uncertain = true
			release(lease)
			continue
		}
		if !renderer {
			release(lease)
			continue
		}
		actual, err := os.Stat(lease.Instance().Image)
		if err != nil || !os.SameFile(actual, r.image) {
			complete = false
			uncertain = true
			release(lease)
			continue
		}
		if ambiguous[child] {
			complete = false
			uncertain = true
			release(lease)
			continue
		}
		if old := candidates[child]; old != nil {
			ambiguous[child] = true
			complete = false
			uncertain = true
			release(old)
			release(lease)
			delete(candidates, child)
			continue
		}
		candidates[child] = lease
	}
	wanted := map[uint32]bool{}
	for _, frame := range snapshot.Frames {
		wanted[frame.Child] = true
	}
	r.mu.Lock()
	r.finishIdleArtifactRetirementsLocked(snapshot)
	defer func() {
		pause := r.established && uncertain
		// Every sampled frame must belong to an explicitly revoked artifact.
		// Those routes cannot dispatch. Keep teardown uncertainty pending until
		// the native context/frame close ACK; an unrelated live owner still
		// requires the normal permanent continuity fence.
		retiringOnly := len(r.artifactRetiring) > 0
		for _, frame := range snapshot.Frames {
			if _, retiring := r.artifactRetiring[frame.Owner]; !retiring {
				retiringOnly = false
				break
			}
		}
		if snapshot.UnknownReason == "" && retiringOnly {
			pause = false
		}
		r.mu.Unlock()
		if pause {
			r.broker.pauseRendererGraph(errors.New(reason))
		}
	}()
	if r.conflicted == nil {
		r.conflicted = map[uint32]bool{}
	}
	for child := range ambiguous {
		r.conflicted[child] = true
	}
	for child, lease := range candidates {
		if !wanted[child] {
			release(lease)
			continue
		}
		instance := lease.Instance()
		if old := r.held[child]; old != nil {
			if old.identity.PID != instance.PID || old.identity.Started != instance.Started {
				r.conflicted[child] = true
				complete = false
				uncertain = true
			}
			release(lease)
			continue
		}
		r.held[child] = &rendererLease{process: lease, identity: RendererInstance{Child: child, PID: instance.PID, Started: instance.Started}}
	}
	for child, held := range r.held {
		alive, err := held.process.Alive()
		if err != nil {
			uncertain = true
			complete = false
		}
		held.identity.Exited = !alive && err == nil
		retained := false
		for _, fault := range r.faults {
			if fault.process == held {
				retained = true
				break
			}
		}
		if !wanted[child] && !retained {
			if err = held.process.Close(); err != nil {
				complete = false
				if cleanupErr == nil {
					cleanupErr = err
				}
			} else {
				delete(r.held, child)
			}
		}
	}
	for child := range wanted {
		if r.held[child] == nil {
			complete = false
		}
	}
	if r.cleanupErr == nil {
		r.cleanupErr = cleanupErr
	}
	r.retired = append(r.retired, retired...)
	if len(r.retired) > 0 {
		complete = false
	}
	r.revision = snapshot.Revision
	r.epoch = snapshot.FaultEpoch
	r.frames = snapshot.Frames
	r.joinUncertain = uncertain
	r.complete = complete

}

// fence is called synchronously by the authenticated receive owner before it
// accepts another frame event. The evidence belongs to this fault epoch, not to
// the later live graph. Incomplete evidence stays incomplete for this epoch.
func (r *rendererTracker) fence(graph FrameGraphSnapshot, reported RendererTermination) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.faults == nil {
		r.faults = map[uint64]*rendererFault{}
	}
	if r.faults[graph.FaultEpoch] != nil {
		return
	}
	fault := &rendererFault{influence: RendererInfluence{FaultEpoch: graph.FaultEpoch, GraphRevision: graph.Revision}}
	r.faults[graph.FaultEpoch] = fault
	// Associate the callback's actual main frame with its previously joined
	// kernel instance. Never attribute every process that happens to exit later.
	for _, frame := range graph.Frames {
		if frame.Frame == reported.ReportedMainFrame && frame.Browser == reported.ReportedBrowser && frame.Tab == reported.ReportedTab && frame.Main {
			fault.process = r.held[frame.Child]
			break
		}
	}
	if fault.process == nil {
		fault.resolved = true
		return
	}
	fault.eligible = graph.TopologyComplete && r.established && !r.conflicted[fault.process.identity.Child]
	for _, frame := range graph.Frames {
		if frame.Child == fault.process.identity.Child {
			fault.influence.Frames = append(fault.influence.Frames, frame)
		}
		if r.held[frame.Child] == nil {
			fault.eligible = false
		}
	}
	// The callback may arrive just before the kernel handle becomes signaled.
	// Retain this exact lease and the fence records; snapshot only confirms exit.
}
func (r *rendererTracker) snapshot(revision, epoch uint64) ([]RendererInstance, RendererInfluence, bool) {
	r.mu.Lock()
	uncertain := false
	defer func() {
		r.mu.Unlock()
		if uncertain {
			r.broker.pauseRendererGraph(errors.New("CEF_RENDERER_JOIN_UNCERTAIN"))
		}
	}()
	complete := r.complete && r.revision == revision && r.epoch == epoch

	if complete && len(r.frames) > 0 {
		r.established = true
	}
	var processes []RendererInstance
	for _, held := range r.held {
		identity := held.identity
		alive, err := held.process.Alive()
		if err != nil {
			complete = false
			uncertain = r.established
		}
		identity.Exited = err == nil && !alive
		processes = append(processes, identity)
	}
	sort.Slice(processes, func(i, j int) bool { return processes[i].Child < processes[j].Child })
	influence := RendererInfluence{FaultEpoch: epoch, GraphRevision: revision, Complete: complete}
	if epoch != 0 {
		if fault := r.faults[epoch]; fault != nil {
			if !fault.resolved && fault.process != nil {
				alive, err := fault.process.process.Alive()
				if err != nil {
					fault.resolved = true
					fault.eligible = false
				} else if !alive {
					identity := fault.process.identity
					identity.Exited = true
					fault.influence.Processes = []RendererInstance{identity}
					fault.influence.Complete = fault.eligible
					fault.resolved = true
				}
			}
			influence = fault.influence
			influence.Processes = append([]RendererInstance(nil), influence.Processes...)
			influence.Frames = append([]FrameRecord(nil), influence.Frames...)
		}
	}
	return processes, influence, complete
}
func (r *rendererTracker) close() (bool, error) {
	r.once.Do(func() { close(r.stop) })
	select {
	case <-r.joined:
	case <-time.After(2 * time.Second):
		return false, errors.New("CEF_RENDERER_GRAPH_JOIN_PENDING")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var result error
	remaining := r.retired[:0]
	for _, lease := range r.retired {
		if err := lease.Close(); err != nil {
			result = errors.Join(result, err)
			remaining = append(remaining, lease)
		}
	}
	r.retired = remaining
	for child, held := range r.held {
		if err := held.process.Close(); err != nil {
			result = errors.Join(result, err)
		} else {
			delete(r.held, child)
		}
	}
	return len(r.held) == 0 && len(r.retired) == 0, errors.Join(r.cleanupErr, result)
}

// arm starts continuity enforcement when an OS-joined graph has been consumed
// or real control is admitted. New/unbound startup frames alone are pending,
// not evidence that a previously used association has been lost.
func (r *rendererTracker) arm() error {
	r.mu.Lock()
	r.established = true
	uncertain := r.joinUncertain
	r.mu.Unlock()
	if uncertain {
		cause := errors.New("CEF_RENDERER_JOIN_UNCERTAIN")
		r.broker.pauseRendererGraph(cause)
		return cause
	}
	return nil
}

// Only Artifact.Revoke calls this after its Go route/resources are invalidated.
// This is a pending teardown fence, not permission to control an unknown graph.
func (r *rendererTracker) beginArtifactRetirement(owner string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.artifactRetiring == nil {
		r.artifactRetiring = map[string]bool{}
	}
	if _, exists := r.artifactRetiring[owner]; !exists {
		r.artifactRetiring[owner] = false
	}
}

// Called only after the authenticated native context/frame/resource drain ACK.
// A prior fault is never cleared. A new document must establish a fresh census.
func (r *rendererTracker) finishArtifactRetirement(owner string) {
	graph := r.broker.frameGraphOnly()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.artifactRetiring[owner] = true
	r.finishIdleArtifactRetirementsLocked(graph)
}

func (r *rendererTracker) finishIdleArtifactRetirementsLocked(graph FrameGraphSnapshot) {
	if len(r.artifactRetiring) == 0 {
		return
	}
	for _, complete := range r.artifactRetiring {
		if !complete {
			return
		}
	}
	if graph.TopologyComplete && len(graph.Frames) == 0 && graph.FaultEpoch == 0 && graph.UnknownReason == "" && !r.broker.ControlPaused() {
		clear(r.artifactRetiring)
		r.established = false
		r.complete = false
	}
}
