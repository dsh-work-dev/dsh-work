//go:build windows

package embeddedbrowser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/automation/browsernative"
	"golang.org/x/sys/windows"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	win "github.com/local/dsh-work/internal/platform/windows"
	"github.com/local/dsh-work/internal/supervisor"
)

// Only this trusted fixture selects a supported shared-process pressure mode.
// Production LaunchPlans, sandbox and Site Isolation settings are unchanged.
type sharedRendererFixture struct {
	supervisor.Adapter
	failure *atomic.Bool
}
type graphInventoryWorker struct {
	supervisor.Worker
	inventory supervisor.ProcessInventory
	failure   *atomic.Bool
}

func (w *graphInventoryWorker) OwnedProcesses(ctx context.Context) ([]supervisor.ProcessLease, error) {
	if w.failure.Load() {
		return nil, errors.New("controlled owned census failure")
	}
	return w.inventory.OwnedProcesses(ctx)
}

func (a sharedRendererFixture) Start(ctx context.Context, plan supervisor.LaunchPlan, handler supervisor.RawOutputHandler) (supervisor.Worker, error) {
	plan.Args = append(plan.Args, "--process-per-site")
	worker, err := a.Adapter.Start(ctx, plan, handler)
	if err != nil || a.failure == nil {
		return worker, err
	}
	return &graphInventoryWorker{Worker: worker, inventory: worker.(supervisor.ProcessInventory), failure: a.failure}, nil
}

// This preflight measures the locked engine's actual renderer allocation. It
// is not a sustained graph, a fault qualification, or a real Wails UI gate.
func TestLiveRendererInventorySharedPreflight(t *testing.T) { runRendererGraphFixture(t, false, false) }
func TestLiveSharedRendererGraphFault(t *testing.T) {
	if os.Getenv("DSH_WORK_CEF_SHARED_STRESS") != "1" {
		t.Skip("explicit controlled shared renderer topology required")
	}
	runRendererGraphFixture(t, true, false)
}
func TestLiveOSJoinUncertaintyKeepsPage(t *testing.T)     { runRendererGraphFixture(t, false, true) }
func TestLiveFrameGraphUncertaintyKeepsPage(t *testing.T) { runRendererGraphFixture(t, false, true) }
func runRendererGraphFixture(t *testing.T, fault, uncertainty bool) {
	executable := os.Getenv("DSH_WORK_CEF_TEST_EXECUTABLE")
	if executable == "" {
		t.Skip("explicit isolated CEF component required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	type report struct {
		Nonce, Input string
		Tick         int
	}
	var mu sync.Mutex
	reports := map[string]report{}
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/tick" {
			var value report
			if json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&value) == nil {
				mu.Lock()
				reports[r.URL.Query().Get("role")] = value
				mu.Unlock()
			}
			w.WriteHeader(204)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!doctype html><title>owned renderer graph</title><input value="initial"><script>const nonce=crypto.randomUUID(),input=document.querySelector('input');input.value='dirty-'+nonce;let tick=0;setInterval(()=>fetch('/tick?role='+new URL(location.href).searchParams.get('role'),{method:'POST',body:JSON.stringify({Nonce:nonce,Input:input.value,Tick:++tick})}),100)</script>`)
	}))
	defer site.Close()
	failure := &atomic.Bool{}
	osJoinUnknown := strings.Contains(t.Name(), "OSJoin")
	adapter := win.NewJobObjectAdapter()
	mode := "default"
	if os.Getenv("DSH_WORK_CEF_SHARED_STRESS") == "1" {
		adapter = sharedRendererFixture{Adapter: adapter, failure: failure}
		mode = "fixture-process-per-site"
	}
	b, err := Start(ctx, Config{Executable: executable, Profile: filepath.Join(t.TempDir(), "profile"), Supervisor: adapter})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := b.Close(); err != nil || b.CleanupPending() {
			t.Errorf("actual native cleanup: %v pending=%t", err, b.CleanupPending())
		}
	}()
	roles := []string{"A", "B", "C"}
	children := map[string]uint64{}
	frames := map[string]string{}
	identities := map[string]automation.OwnedBrowserIdentity{}
	targets := map[string]*OwnedTarget{}
	for _, role := range roles {
		address := site.URL
		if role == "C" {
			address = strings.Replace(address, "127.0.0.1", "localhost", 1)
		}
		target, e := b.OpenOwnedWebTarget(ctx, address+"/?role="+role)
		if e != nil {
			t.Fatal(e)
		}
		id, e := target.OwnedIdentity(ctx)
		if e != nil {
			t.Fatal(e)
		}
		child, e := strconv.ParseUint(strings.Split(id.Frame, "-")[0], 16, 32)
		if e != nil || child == 0 {
			t.Fatal("unavailable logical child association", id.Frame, e)
		}
		children[role] = child
		frames[role] = id.Frame
		identities[role] = id
		targets[role] = target
	}
	inventory, ok := b.worker.(supervisor.ProcessInventory)
	if !ok {
		t.Fatal("actual Job inventory missing")
	}
	leases, err := inventory.OwnedProcesses(ctx)
	defer func() {
		for _, lease := range leases {
			lease.Close()
		}
	}()
	if err != nil {
		t.Fatal(err)
	}
	renderers := map[uint64]supervisor.ProcessInstance{}
	for _, lease := range leases {
		args, e := lease.Arguments(ctx)
		if e != nil {
			t.Fatal("actual owned command-line unavailable", lease.Instance().PID, e)
		}
		kind := ""
		var child uint64
		for _, arg := range args {
			if value, yes := strings.CutPrefix(arg, "--type="); yes {
				kind = value
			}
			if value, yes := strings.CutPrefix(arg, "--renderer-client-id="); yes {
				child, _ = strconv.ParseUint(value, 10, 32)
			}
		}
		if kind != "renderer" {
			continue
		}
		if child == 0 {
			t.Fatal("renderer role missing logical child id")
		}
		if _, duplicate := renderers[child]; duplicate {
			t.Fatal("ambiguous renderer logical id")
		}
		identity := lease.Instance()
		actual, e1 := os.Stat(identity.Image)
		expected, e2 := os.Stat(executable)
		if e1 != nil || e2 != nil || !os.SameFile(actual, expected) {
			t.Fatal("renderer executable mismatch", e1, e2)
		}
		renderers[child] = identity
	}
	var joined FrameGraphSnapshot
	for {
		joined = b.FrameGraph()
		if joined.ProcessJoinComplete {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("continuous frame/process join unavailable", joined, ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
	for role, frameID := range frames {
		found := false
		for _, frame := range joined.Frames {
			if frame.Frame == frameID && frame.Attached && frame.Main && frame.Child == uint32(children[role]) && joined.MainFrames[frame.Owner] == frameID {
				for _, process := range joined.Processes {
					if process.Child == frame.Child && process.PID == renderers[children[role]].PID && process.Started == renderers[children[role]].Started && !process.Exited {
						found = true
					}
				}
			}
		}
		if !found {
			t.Fatal("continuous graph does not retain actual frame/process association", role, joined)
		}
	}
	result := struct {
		Frames              map[string]string
		LogicalChildren     map[string]uint64
		Renderers           map[uint64]supervisor.ProcessInstance
		SharedAB, DistinctC bool
		Mode                string
		Graph               FrameGraphSnapshot
	}{frames, children, renderers, children["A"] == children["B"], children["A"] != children["C"], mode, joined}
	for _, child := range children {
		if _, found := renderers[child]; !found {
			t.Fatal("logical child has no held actual Job renderer", child)
		}
	}
	if directory := os.Getenv("DSH_WORK_CEF_TEST_EVIDENCE"); directory != "" {
		if err = os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
		data, _ := json.MarshalIndent(result, "", "  ")
		if err = os.WriteFile(filepath.Join(directory, "renderer-allocation.json"), append(data, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("actual logical renderers A=%d B=%d C=%d sharedAB=%t distinctC=%t", children["A"], children["B"], children["C"], result.SharedAB, result.DistinctC)
	if !result.SharedAB || !result.DistinctC {
		t.Fatal("default pinned CEF allocation does not supply the required shared and independent groups; no fault injected")
	}
	if !fault && !uncertainty {
		return
	}
	baseline := map[string]report{}
	for {
		mu.Lock()
		all := true
		for _, role := range roles {
			value := reports[role]
			baseline[role] = value
			if value.Nonce == "" || value.Input != "dirty-"+value.Nonce || value.Tick < 2 {
				all = false
			}
		}
		mu.Unlock()
		if all {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("real dirty/heartbeat baseline unavailable")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if uncertainty {
		end, e := targets["C"].BeginOwnedCall(ctx, automation.OwnedBrowserCall{Identity: identities["C"], Token: "actual-native-before-graph-uncertainty"})
		if e != nil {
			t.Fatal(e)
		}
		defer end()
		if osJoinUnknown {
			failure.Store(true)
			b.renderers.notify()
		} else {
			b.pauseFrameGraph(errors.New("CEF_GRAPH_REVISION_REJECTED"))
		}
		select {
		case <-b.done:
		case <-ctx.Done():
			t.Fatal("OS uncertainty did not invalidate authority")
		}
		b.mu.Lock()
		paused := b.framePauseDone
		b.mu.Unlock()
		select {
		case <-paused:
		case <-ctx.Done():
			t.Fatal("no confirmed native uncertainty pause")
		}
		after := b.FrameGraph()
		expectedReason := "CEF_GRAPH_REVISION_REJECTED"
		if osJoinUnknown {
			expectedReason = "CEF_RENDERER_CENSUS_UNCERTAIN"
		}
		if after.UnknownReason != expectedReason || !after.ControlPauseConfirmed || (!osJoinUnknown && after.TopologyComplete) || after.ProcessJoinComplete {
			t.Fatal("unknown graph published as complete/available", after)
		}
		request := "actual-graph-uncertainty-native-negative"
		result := make(chan controlResult, 1)
		b.mu.Lock()
		b.controls[request] = result
		b.mu.Unlock()
		defer func() { b.mu.Lock(); delete(b.controls, request); b.mu.Unlock() }()
		b.writeMu.Lock()
		e = browsernative.WriteWireMessage(b.conn, map[string]any{"kind": "target.begin", "generation": b.generation, "route": targets["C"].owner, "request": request, "arguments": map[string]any{"identity": identities["C"], "token": "must-not-reuse-old-authority"}})
		b.writeMu.Unlock()
		if e != nil {
			t.Fatal(e)
		}
		select {
		case value := <-result:
			if value.Error != ErrControlPaused.Error() {
				t.Fatal("native final admission escaped graph pause", value)
			}
		case <-ctx.Done():
			t.Fatal("no actual native rejection")
		}
		current, e := targets["C"].OwnedIdentity(ctx)
		if e != nil || current.Document != identities["C"].Document || current.Frame != identities["C"].Frame {
			t.Fatal("graph uncertainty destroyed healthy identity", e)
		}
		mu.Lock()
		boundary := reports["C"]
		mu.Unlock()
		var live report
		for {
			mu.Lock()
			live = reports["C"]
			mu.Unlock()
			if live.Tick > boundary.Tick {
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal("graph pause killed healthy document")
			case <-time.After(10 * time.Millisecond):
			}
		}
		if live.Nonce != baseline["C"].Nonce || live.Input != baseline["C"].Input {
			t.Fatal("graph pause replaced document/input", live)
		}
		if directory := os.Getenv("DSH_WORK_CEF_TEST_EVIDENCE"); directory != "" {
			data, _ := json.MarshalIndent(map[string]any{"beforeGraph": joined, "afterGraph": after, "nativeBeginRejected": true, "healthyAfterBoundary": live, "baseline": baseline, "injection": map[bool]string{true: "controlled OS census failure, actual native pause/dispatch", false: "Go graph continuity error, actual native pause/dispatch"}[osJoinUnknown]}, "", "  ")
			if e = os.WriteFile(filepath.Join(directory, "renderer-graph-unknown.json"), append(data, '\n'), 0600); e != nil {
				t.Fatal(e)
			}
		}
		return
	}
	renderer := renderers[children["A"]]
	handle, e := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, renderer.PID)
	if e != nil {
		t.Fatal(e)
	}
	defer windows.CloseHandle(handle)
	var created, exited, kernel, user windows.Filetime
	if e = windows.GetProcessTimes(handle, &created, &exited, &kernel, &user); e != nil || uint64(created.HighDateTime)<<32|uint64(created.LowDateTime) != renderer.Started {
		t.Fatal("renderer kernel instance changed before injection", e)
	}
	if status, e := windows.WaitForSingleObject(handle, 0); e != nil || status != uint32(windows.WAIT_TIMEOUT) {
		t.Fatal("held renderer no longer live", e)
	}
	if e = windows.TerminateProcess(handle, 91); e != nil {
		t.Fatal(e)
	}
	if status, e := windows.WaitForSingleObject(handle, 3000); e != nil || status != windows.WAIT_OBJECT_0 {
		t.Fatal("actual renderer exit not observed", status, e)
	}
	select {
	case <-b.done:
	case <-ctx.Done():
		t.Fatal("missing actual native termination callback")
	}
	var after FrameGraphSnapshot
	for {
		after = b.FrameGraph()
		if after.Influence.Complete && after.FaultEpoch > 0 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("continuous fault association incomplete", after, ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
	impacted := map[string]bool{}
	for _, frame := range after.Influence.Frames {
		impacted[frame.Frame] = true
	}
	if !impacted[frames["A"]] || !impacted[frames["B"]] || impacted[frames["C"]] {
		t.Fatal("actual shared process affected set incorrect", after.Influence)
	}
	if len(after.Influence.Processes) != 1 || after.Influence.Processes[0].PID != renderer.PID || after.Influence.Processes[0].Started != renderer.Started {
		t.Fatal("fault was not joined to held actual renderer", after.Influence)
	}
	if e = targets["A"].CloseNative(ctx); e != nil {
		t.Fatal("close affected A", e)
	}
	var retained FrameGraphSnapshot
	for {
		retained = b.FrameGraph()
		present := false
		for _, f := range retained.Frames {
			if f.Frame == frames["A"] {
				present = true
			}
		}
		if !present {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("A frame not destroyed")
		case <-time.After(10 * time.Millisecond):
		}
	}
	historical, ok := b.FaultInfluence(after.FaultEpoch)
	if !ok || !reflect.DeepEqual(historical, after.Influence) {
		t.Fatal("fault evidence changed after A destruction", after.Influence, historical)
	}
	healthy, e := targets["C"].OwnedIdentity(ctx)
	if e != nil || healthy.Document != identities["C"].Document || healthy.Frame != identities["C"].Frame {
		t.Fatal("healthy page identity was replaced", healthy, e)
	}
	mu.Lock()
	boundary := reports["C"]
	mu.Unlock()
	var current report
	for {
		mu.Lock()
		current = reports["C"]
		mu.Unlock()
		if current.Tick > boundary.Tick {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("healthy page heartbeat did not continue after fault")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if current.Nonce != baseline["C"].Nonce || current.Input != baseline["C"].Input {
		t.Fatal("healthy document/input changed", current)
	}
	// The known impact set does not authorize selective recovery yet. Original
	// whole-broker pause remains the dispatch boundary, including healthy C.
	if end, e := targets["C"].BeginOwnedCall(ctx, automation.OwnedBrowserCall{Identity: healthy, Token: "must-remain-paused"}); e == nil {
		end()
		t.Fatal("healthy C control resumed without a recovery contract")
	}
	if directory := os.Getenv("DSH_WORK_CEF_TEST_EVIDENCE"); directory != "" {
		data, _ := json.MarshalIndent(map[string]any{"mode": mode, "beforeGraph": joined, "afterGraph": after, "afterAffectedFrameDestroyed": retained, "baseline": baseline, "healthyAfterBoundary": current, "rendererExitObserved": true, "controlRemainsWholeBrokerPaused": true}, "", "  ")
		if e = os.WriteFile(filepath.Join(directory, "renderer-shared-fault.json"), append(data, '\n'), 0600); e != nil {
			t.Fatal(e)
		}
	}
	t.Log("actual shared renderer fault joined both A/B documents; C original dirty document remained live, control stayed paused")

}
