package pet

import (
	"context"
	"testing"
	"time"
)

func runtimeTestDefinition() PetDefinition {
	frame := func(index int, duration int) FrameRef {
		return FrameRef{Index: index, DurationMS: duration}
	}
	return PetDefinition{
		Tracks: map[string]TrackSpec{
			"idle":      {ID: "idle", Channel: TrackBase, Frames: []FrameRef{frame(0, 100), frame(1, 100)}, Loop: true},
			"working":   {ID: "working", Channel: TrackBase, Frames: []FrameRef{frame(2, 100), frame(3, 100)}, Loop: true},
			"waiting":   {ID: "waiting", Channel: TrackBase, Frames: []FrameRef{frame(4, 100)}, Loop: false, Fallback: "idle"},
			"attention": {ID: "attention", Channel: TrackAction, Frames: []FrameRef{frame(5, 100)}, Loop: false, Fallback: "idle"},
		},
		States: map[string]StateSpec{
			"idle":    {Track: "idle", Loop: true, Fallback: "idle"},
			"working": {Track: "working", Loop: true, Fallback: "idle"},
			"waiting": {Track: "waiting", Loop: false, Fallback: "idle"},
		},
		Actions: map[string]ActionSpec{
			"attention": {Track: "attention", Priority: 1, Interruptible: true, Fallback: "idle"},
		},
		Fallback: FallbackSpec{Idle: "idle"},
	}
}

func TestRuntimeUsesGenerationAndEventIdentityIdempotently(t *testing.T) {
	now := time.Unix(100, 0)
	runtime := NewRuntime(RuntimeConfig{Clock: func() time.Time { return now }, Generation: "g1"})
	if err := runtime.Start(context.Background(), runtimeTestDefinition()); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Dispatch(PetInputEvent{EventID: "waiting-1", Generation: "g1", Sequence: 1, MonotonicTime: 10, Type: "task.waiting"}); err != nil {
		t.Fatal(err)
	}
	first := runtime.Snapshot(now)
	if first.BaseState != BaseWaiting || first.Action != "attention" {
		t.Fatalf("first waiting snapshot = %+v", first)
	}
	if err := runtime.Dispatch(PetInputEvent{EventID: "waiting-1", Generation: "g1", Sequence: 2, MonotonicTime: 20, Type: "task.waiting"}); err != nil {
		t.Fatal(err)
	}
	duplicate := runtime.Snapshot(now)
	if duplicate.Action != first.Action || duplicate.ActionFrameIndex != first.ActionFrameIndex {
		t.Fatalf("duplicate changed one-shot action: first=%+v duplicate=%+v", first, duplicate)
	}
	if err := runtime.Dispatch(PetInputEvent{EventID: "stale-generation", Generation: "g2", Sequence: 3, MonotonicTime: 30, Type: "task.completed"}); err != nil {
		t.Fatal(err)
	}
	stale := runtime.Snapshot(now)
	if stale.BaseState != BaseWaiting {
		t.Fatalf("stale generation changed state: %+v", stale)
	}
	if !hasRuntimeIssue(stale.Diagnostics, IssueDuplicateEvent) || !hasRuntimeIssue(stale.Diagnostics, IssueStaleGeneration) {
		t.Fatalf("diagnostics = %+v", stale.Diagnostics)
	}
}

func TestRuntimeRejectsEventsWithoutTheActiveGeneration(t *testing.T) {
	now := time.Unix(150, 0)
	runtime := NewRuntime(RuntimeConfig{Clock: func() time.Time { return now }, Generation: "g1"})
	if err := runtime.Start(context.Background(), runtimeTestDefinition()); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Dispatch(PetInputEvent{EventID: "missing-generation", Type: "task.completed"}); err != nil {
		t.Fatal(err)
	}
	snapshot := runtime.Snapshot(now)
	if snapshot.BaseState != BaseIdle || !hasRuntimeIssue(snapshot.Diagnostics, IssueMissingGeneration) {
		t.Fatalf("missing-generation event changed state or diagnostics = %+v", snapshot)
	}
}

func TestRuntimeRejectsOutOfOrderSequenceAndUsesMonotonicDelta(t *testing.T) {
	now := time.Unix(200, 0)
	runtime := NewRuntime(RuntimeConfig{Clock: func() time.Time { return now }, Generation: "g1"})
	if err := runtime.Start(context.Background(), runtimeTestDefinition()); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Dispatch(PetInputEvent{Generation: "g1", Sequence: 2, MonotonicTime: 100, Type: "task.working"}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Dispatch(PetInputEvent{Generation: "g1", Sequence: 1, MonotonicTime: 90, Type: "task.cancelled"}); err != nil {
		t.Fatal(err)
	}
	snapshot := runtime.Snapshot(now)
	if snapshot.BaseState != BaseWorking {
		t.Fatalf("out-of-order event changed state: %+v", snapshot)
	}
	if !hasRuntimeIssue(snapshot.Diagnostics, IssueOutOfOrderEvent) {
		t.Fatalf("diagnostics = %+v", snapshot.Diagnostics)
	}

	if err := runtime.Dispatch(PetInputEvent{Generation: "g1", Sequence: 3, MonotonicTime: 200, Type: "task.waiting"}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(100 * time.Millisecond)
	if snapshot := runtime.Snapshot(now); snapshot.Action != "attention" {
		t.Fatalf("monotonic event did not enter action channel: %+v", snapshot)
	}
}

func TestRuntimeHiddenStateStopsTimeAndLookWithoutChangingTruth(t *testing.T) {
	now := time.Unix(300, 0)
	runtime := NewRuntime(RuntimeConfig{Clock: func() time.Time { return now }})
	if err := runtime.Start(context.Background(), runtimeTestDefinition()); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Dispatch(PetInputEvent{Type: "task.working"}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(50 * time.Millisecond)
	runtime.SetHidden(true)
	hidden := runtime.Snapshot(now)
	if hidden.EffectiveVisibility != VisibilityHidden || hidden.OverlayStatus != OverlayHidden || hidden.BaseState != BaseWorking {
		t.Fatalf("hidden snapshot = %+v", hidden)
	}
	if hidden.LookFrameIndex != -1 {
		t.Fatalf("hidden look frame = %d, want no look frame", hidden.LookFrameIndex)
	}
	now = now.Add(10 * time.Second)
	stillHidden := runtime.Snapshot(now)
	if stillHidden.BaseState != BaseWorking || stillHidden.FrameIndex != hidden.FrameIndex {
		t.Fatalf("hidden time advanced state/frame: before=%+v after=%+v", hidden, stillHidden)
	}
	if err := runtime.Dispatch(PetInputEvent{Type: "pointer.move", Payload: map[string]any{"x": 100, "y": 0, "gazeX": 0, "gazeY": 0}}); err != nil {
		t.Fatal(err)
	}
	if hiddenLook := runtime.Snapshot(now); hiddenLook.Look.Mode != "neutral" {
		t.Fatalf("hidden pointer event changed look state: %+v", hiddenLook.Look)
	}
	runtime.SetHidden(false)
	shown := runtime.Snapshot(now)
	if shown.EffectiveVisibility != VisibilityVisible || shown.BaseState != BaseWorking || shown.FrameIndex != 2 {
		t.Fatalf("shown snapshot replayed hidden time: %+v", shown)
	}
}

func TestRuntimeReducedMotionUsesTheStaticFrame(t *testing.T) {
	now := time.Unix(350, 0)
	runtime := NewRuntime(RuntimeConfig{Clock: func() time.Time { return now }})
	if err := runtime.Start(context.Background(), runtimeTestDefinition()); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Dispatch(PetInputEvent{Type: "task.working"}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(90 * time.Millisecond)
	runtime.SetReducedMotion(true)
	snapshot := runtime.Snapshot(now)
	if !snapshot.ReducedMotion || snapshot.FrameIndex != 2 {
		t.Fatalf("reduced-motion snapshot = %+v, want the first working frame", snapshot)
	}
}

func TestRuntimeUsesActionFallbackWhenItDiffersFromTrackFallback(t *testing.T) {
	now := time.Unix(375, 0)
	definition := runtimeTestDefinition()
	definition.Actions["attention"] = ActionSpec{Track: "attention", Priority: 1, Interruptible: true, Fallback: "working"}
	runtime := NewRuntime(RuntimeConfig{Clock: func() time.Time { return now }})
	if err := runtime.Start(context.Background(), definition); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Dispatch(PetInputEvent{Type: "task.waiting"}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(100 * time.Millisecond)
	snapshot := runtime.Snapshot(now)
	if snapshot.Action != "" || snapshot.TrackID != "working" {
		t.Fatalf("action fallback snapshot = %+v, want working fallback", snapshot)
	}
}

func TestRuntimeProjectsDirectionalLookWithoutReplacingBaseState(t *testing.T) {
	now := time.Unix(400, 0)
	definition := runtimeTestDefinition()
	directions := make([]Direction, 16)
	for index := range directions {
		directions[index] = Direction{Angle: float64(index) * 22.5, FrameIndex: 100 + index}
	}
	definition.Directions = &DirectionProfile{Directions: directions, NeutralPolicy: "deadzone-to-idle"}
	runtime := NewRuntime(RuntimeConfig{Clock: func() time.Time { return now }})
	if err := runtime.Start(context.Background(), definition); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Dispatch(PetInputEvent{Type: "pointer.move", Payload: map[string]any{"x": 100, "y": 0, "gazeX": 0, "gazeY": 0}}); err != nil {
		t.Fatal(err)
	}
	snapshot := runtime.Snapshot(now)
	if snapshot.BaseState != BaseIdle || snapshot.Look.Mode != "directional" || snapshot.Look.Direction != 4 || snapshot.LookFrameIndex != 104 || snapshot.FrameIndex != 104 {
		t.Fatalf("directional snapshot = %+v", snapshot)
	}
}

func hasRuntimeIssue(issues []Issue, code string) bool {
	for _, issue := range issues {
		if issue.Code == code {
			return true
		}
	}
	return false
}
