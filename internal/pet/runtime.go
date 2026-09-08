package pet

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	IssueUnsupportedEvent  = "pet.unsupported-event"
	IssueStaleGeneration   = "pet.stale-generation"
	IssueDuplicateEvent    = "pet.duplicate-event"
	IssueMissingGeneration = "pet.missing-generation"
	IssueUnsafeEvent       = "pet.unsafe-event"
	IssueUnknownState      = "pet.unknown-state"
	IssueUnknownAction     = "pet.unknown-action"
	IssueMissingTrack      = "pet.missing-track"
	IssueUnsupportedRender = "pet.unsupported-renderer"
	IssueRendererApply     = "pet.renderer-apply-failed"
	IssueOutOfOrderEvent   = "pet.out-of-order-event"
)

type BaseState string

const (
	BaseStarting  BaseState = "starting"
	BaseIdle      BaseState = "idle"
	BaseWorking   BaseState = "working"
	BaseWaiting   BaseState = "waiting"
	BaseReviewing BaseState = "reviewing"
	BaseCompleted BaseState = "completed"
	BaseFailed    BaseState = "failed"
	BaseOffline   BaseState = "offline"
)

type EffectiveVisibility string

const (
	VisibilityVisible EffectiveVisibility = "visible"
	VisibilityHidden  EffectiveVisibility = "hidden"
	VisibilityPaused  EffectiveVisibility = "paused"
)

type SelectionStatus string

const (
	SelectionNone        SelectionStatus = "none"
	SelectionReady       SelectionStatus = "ready"
	SelectionUnavailable SelectionStatus = "unavailable"
	SelectionInvalid     SelectionStatus = "invalid"
	SelectionSwitching   SelectionStatus = "switching"
)

type OverlayStatus string

const (
	OverlayStopped   OverlayStatus = "stopped"
	OverlayHidden    OverlayStatus = "hidden"
	OverlayVisible   OverlayStatus = "visible"
	OverlayPreparing OverlayStatus = "preparing"
	OverlayFailed    OverlayStatus = "failed"
)

// PetInputEvent is the only event shape accepted by the runtime. Payload is
// intentionally untyped at this seam because the event port owns decoding;
// runtime projection only reads a small allow-listed set of scalar values.
type PetInputEvent struct {
	EventID       string         `json:"eventId"`
	Generation    string         `json:"generation"`
	Sequence      uint64         `json:"sequence"`
	MonotonicTime int64          `json:"monotonicTime"`
	Type          string         `json:"type"`
	Payload       map[string]any `json:"payload,omitempty"`
	Source        string         `json:"source,omitempty"`
}

type LookSnapshot struct {
	Mode        string  `json:"mode"`
	Angle       float64 `json:"angle"`
	Direction   int     `json:"direction"`
	NormalizedX float64 `json:"normalizedX"`
	NormalizedY float64 `json:"normalizedY"`
}

// PetRuntimeSnapshot is a read-only projection consumed by a renderer or the
// overlay. It contains no source-format row/column knowledge.
type PetRuntimeSnapshot struct {
	BaseState           BaseState           `json:"baseState"`
	Action              string              `json:"action,omitempty"`
	BaseTrackID         string              `json:"baseTrackId,omitempty"`
	BaseFrameIndex      int                 `json:"baseFrameIndex,omitempty"`
	ActionTrackID       string              `json:"actionTrackId,omitempty"`
	ActionFrameIndex    int                 `json:"actionFrameIndex,omitempty"`
	LookTrackID         string              `json:"lookTrackId,omitempty"`
	LookFrameIndex      int                 `json:"lookFrameIndex"`
	Look                LookSnapshot        `json:"look"`
	Progress            float64             `json:"progress"`
	TrackID             string              `json:"trackId"`
	FrameIndex          int                 `json:"frameIndex"`
	EffectiveVisibility EffectiveVisibility `json:"effectiveVisibility,omitempty"`
	OverlayStatus       OverlayStatus       `json:"overlayStatus,omitempty"`
	SelectionStatus     SelectionStatus     `json:"selectionStatus,omitempty"`
	ReducedMotion       bool                `json:"reducedMotion"`
	Diagnostics         []Issue             `json:"diagnostics,omitempty"`
	At                  int64               `json:"at"`
}

// RuntimeState is the small service-level projection required by Settings.
// Rendering details remain in PetRuntimeSnapshot.
type RuntimeState struct {
	SelectedKey               *string             `json:"selectedKey"`
	PersistedVisibilityIntent string              `json:"persistedVisibilityIntent"`
	EffectiveVisibility       EffectiveVisibility `json:"effectiveVisibility"`
	SelectionStatus           SelectionStatus     `json:"selectionStatus"`
	OverlayStatus             OverlayStatus       `json:"overlayStatus"`
	FailureCode               string              `json:"failureCode,omitempty"`
}

type RuntimeConfig struct {
	Clock      func() time.Time
	Generation string
	Deadzone   float64
}

type PetRuntime interface {
	Start(context.Context, PetDefinition) error
	Dispatch(PetInputEvent) error
	Snapshot(time.Time) PetRuntimeSnapshot
	SetHidden(bool)
	SetReducedMotion(bool)
	Stop(context.Context) error
}

type Runtime struct {
	mu             sync.RWMutex
	clock          func() time.Time
	generation     string
	deadzone       float64
	definition     *PetDefinition
	base           BaseState
	baseOverride   string
	action         string
	actionSince    time.Time
	baseSince      time.Time
	progress       float64
	look           LookSnapshot
	hidden         bool
	reduced        bool
	stopped        bool
	seen           map[string]struct{}
	lastSequence   uint64
	lastEventAt    int64
	lastEventClock time.Time
	seenOrder      []string
	diagnostics    []Issue
}

var _ PetRuntime = (*Runtime)(nil)

func NewRuntime(config RuntimeConfig) *Runtime {
	clock := config.Clock
	if clock == nil {
		clock = time.Now
	}
	deadzone := config.Deadzone
	if deadzone <= 0 {
		deadzone = 8
	}
	generation, _ := safeString(strings.TrimSpace(config.Generation), 128)
	return &Runtime{
		clock:      clock,
		generation: generation,
		deadzone:   deadzone,
		seen:       make(map[string]struct{}),
		base:       BaseIdle,
		look:       LookSnapshot{Mode: "neutral", Direction: -1},
	}
}

func (r *Runtime) Start(ctx context.Context, definition PetDefinition) error {
	if err := runtimeContextError(ctx); err != nil {
		return err
	}
	definition = definition.clone()
	if definition.Fallback.Idle == "" {
		definition.Fallback.Idle = "idle"
	}
	if _, ok := definition.Tracks[definition.Fallback.Idle]; !ok {
		return &DiagnosticError{Issues: []Issue{{Code: IssueMissingTrack, Severity: SeverityError, Retryable: false, SafeFallbackSummary: "the Pet has no usable idle track"}}}
	}
	now := r.clock()
	r.mu.Lock()
	r.definition = &definition
	r.base = BaseIdle
	r.baseOverride = ""
	r.action = ""
	r.actionSince = now
	r.baseSince = now
	r.progress = 0
	r.look = LookSnapshot{Mode: "neutral", Direction: -1}
	r.stopped = false
	r.seen = make(map[string]struct{})
	r.seenOrder = nil
	r.lastSequence = 0
	r.lastEventAt = 0
	r.lastEventClock = time.Time{}
	r.diagnostics = nil
	r.mu.Unlock()
	return nil
}

func (r *Runtime) Dispatch(event PetInputEvent) error {
	if r == nil {
		return errors.New("pet runtime is unavailable")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.definition == nil || r.stopped {
		return nil
	}
	if event.Generation != "" {
		if _, ok := safeString(event.Generation, 128); !ok {
			r.addDiagnosticLocked(IssueUnsafeEvent, SeverityWarning, false)
			return nil
		}
		if r.generation == "" {
			r.setGenerationLocked(event.Generation)
		} else if event.Generation != r.generation {
			r.addDiagnosticLocked(IssueStaleGeneration, SeverityInfo, false)
			return nil
		}
	} else if r.generation != "" {
		r.addDiagnosticLocked(IssueMissingGeneration, SeverityInfo, false)
		return nil
	}
	if event.EventID != "" {
		if _, ok := safeString(event.EventID, 128); !ok {
			r.addDiagnosticLocked(IssueUnsafeEvent, SeverityWarning, false)
			event.EventID = ""
		} else {
			if _, exists := r.seen[event.EventID]; exists {
				r.addDiagnosticLocked(IssueDuplicateEvent, SeverityInfo, false)
				return nil
			}
			const maxSeenEvents = 2048
			if len(r.seenOrder) >= maxSeenEvents {
				oldest := r.seenOrder[0]
				delete(r.seen, oldest)
				r.seenOrder = r.seenOrder[1:]
			}
			r.seen[event.EventID] = struct{}{}
			r.seenOrder = append(r.seenOrder, event.EventID)
		}
	}
	if event.Sequence > 0 && event.Sequence <= r.lastSequence {
		r.addDiagnosticLocked(IssueOutOfOrderEvent, SeverityInfo, false)
		return nil
	}
	if event.MonotonicTime > 0 && r.lastEventAt > 0 && event.MonotonicTime < r.lastEventAt {
		r.addDiagnosticLocked(IssueOutOfOrderEvent, SeverityInfo, false)
		return nil
	}
	now := r.eventTimeLocked(event)
	if event.Sequence > 0 {
		r.lastSequence = event.Sequence
	}
	if event.MonotonicTime > 0 {
		r.lastEventAt = event.MonotonicTime
		r.lastEventClock = now
	}
	switch strings.ToLower(strings.TrimSpace(event.Type)) {
	case "host.starting":
		r.setBaseLocked(BaseStarting, now)
	case "host.ready":
		r.setBaseLocked(BaseIdle, now)
	case "host.offline":
		r.setBaseLocked(BaseOffline, now)
		r.action = ""
	case "task.started", "task.working":
		r.setBaseLocked(BaseWorking, now)
	case "task.waiting", "task.blocked", "task.limited":
		if r.setBaseLocked(BaseWaiting, now) {
			r.triggerActionLocked("attention", now)
		}
	case "task.reviewing":
		if r.setBaseLocked(BaseReviewing, now) {
			r.triggerActionLocked("attention", now)
		}
	case "task.completed":
		if r.setBaseLocked(BaseCompleted, now) {
			if !r.triggerActionLocked("celebrate", now) {
				r.triggerActionLocked("wave", now)
			}
		}
	case "task.failed":
		if r.setBaseLocked(BaseFailed, now) {
			if !r.triggerActionLocked("attention", now) {
				// The failed base track remains the visual result when no action
				// is declared.
				r.action = ""
			}
		}
	case "task.cancelled", "task.interrupted":
		r.setBaseLocked(BaseIdle, now)
		r.action = ""
	case "task.progress":
		r.progress = clamp01(numberValue(event.Payload, "progress"))
	case "pointer.enter", "pointer.move":
		if !r.hidden {
			r.updateLookLocked(event.Payload)
		}
	case "pointer.leave":
		r.look = LookSnapshot{Mode: "neutral", Direction: -1}
	case "accessibility.reduced-motion.changed":
		r.reduced = boolValue(event.Payload, "enabled")
	case "window.visibility.changed":
		if value, ok := event.Payload["visible"].(bool); ok {
			r.setHiddenLocked(!value, now)
		}
	case "display.changed", "pointer.down", "pointer.up", "click", "drag.start", "drag.move", "drag.end":
		// These events are valid at the port boundary. Their window policy is
		// owned by the overlay; they do not alter task truth by themselves.
	default:
		r.addDiagnosticLocked(IssueUnsupportedEvent, SeverityWarning, false)
	}
	return nil
}

func (r *Runtime) Snapshot(at time.Time) PetRuntimeSnapshot {
	if r == nil {
		return PetRuntimeSnapshot{BaseState: BaseIdle, Look: LookSnapshot{Mode: "neutral", Direction: -1}, LookFrameIndex: -1}
	}
	if at.IsZero() {
		at = r.clock()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.definition == nil || r.stopped {
		return PetRuntimeSnapshot{BaseState: BaseIdle, EffectiveVisibility: VisibilityHidden, OverlayStatus: OverlayStopped, Look: r.look, LookFrameIndex: -1, At: at.UnixNano()}
	}
	if !r.hidden {
		r.finishTransientLocked(at)
	}
	baseTrackID := r.baseTrackLocked()
	baseTrack := r.trackSpecLocked(baseTrackID)
	if len(baseTrack.Frames) == 0 {
		r.addDiagnosticLocked(IssueMissingTrack, SeverityWarning, false)
		baseTrackID = r.definition.Fallback.Idle
		baseTrack = r.trackSpecLocked(baseTrackID)
	}
	baseFrame := firstTrackFrame(baseTrack)
	if len(baseTrack.Frames) > 0 && !r.reduced && !r.hidden {
		baseFrame = frameAt(baseTrack, at.Sub(r.baseSince))
	}
	actionTrackID := ""
	actionFrame := -1
	if r.action != "" {
		actionSpec, actionOK := r.definition.Actions[r.action]
		if actionOK {
			actionTrackID = actionSpec.Track
			actionTrack := r.trackSpecLocked(actionTrackID)
			actionFrame = firstTrackFrame(actionTrack)
			if len(actionTrack.Frames) > 0 && !r.reduced && !r.hidden {
				actionFrame = frameAt(actionTrack, at.Sub(r.actionSince))
			}
		}
	}
	lookTrackID, lookFrame := r.lookFrameLocked()
	visualTrackID, visualFrame := baseTrackID, baseFrame
	if actionTrackID != "" && actionFrame >= 0 {
		visualTrackID, visualFrame = actionTrackID, actionFrame
	} else if lookTrackID != "" && lookFrame >= 0 {
		visualTrackID, visualFrame = lookTrackID, lookFrame
	}
	diagnostics := boundedIssues(r.diagnostics)
	effectiveVisibility := VisibilityVisible
	overlayStatus := OverlayVisible
	if r.hidden {
		effectiveVisibility = VisibilityHidden
		overlayStatus = OverlayHidden
	}
	return PetRuntimeSnapshot{
		BaseState:           r.base,
		Action:              r.action,
		BaseTrackID:         baseTrackID,
		BaseFrameIndex:      baseFrame,
		ActionTrackID:       actionTrackID,
		ActionFrameIndex:    actionFrame,
		LookTrackID:         lookTrackID,
		LookFrameIndex:      lookFrame,
		Look:                r.look,
		Progress:            r.progress,
		TrackID:             visualTrackID,
		FrameIndex:          visualFrame,
		EffectiveVisibility: effectiveVisibility,
		OverlayStatus:       overlayStatus,
		SelectionStatus:     SelectionReady,
		ReducedMotion:       r.reduced,
		Diagnostics:         diagnostics,
		At:                  at.UnixNano(),
	}
}

func (r *Runtime) SetHidden(hidden bool) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.setHiddenLocked(hidden, r.clock())
	r.mu.Unlock()
}

// SetGeneration moves the runtime to a new Host generation boundary. Event
// ordering and duplicate identity state never crosses that boundary.
func (r *Runtime) SetGeneration(generation string) {
	if r == nil {
		return
	}
	generation, ok := safeString(strings.TrimSpace(generation), 128)
	if !ok {
		return
	}
	r.mu.Lock()
	r.setGenerationLocked(generation)
	r.mu.Unlock()
}

func (r *Runtime) SetReducedMotion(reduced bool) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.reduced = reduced
	r.mu.Unlock()
}

func (r *Runtime) Stop(ctx context.Context) error {
	if err := runtimeContextError(ctx); err != nil {
		return err
	}
	if r == nil {
		return nil
	}
	r.mu.Lock()
	r.stopped = true
	r.definition = nil
	r.action = ""
	r.mu.Unlock()
	return nil
}

func (r *Runtime) setGenerationLocked(generation string) {
	if r.generation == generation {
		return
	}
	r.generation = generation
	r.seen = make(map[string]struct{})
	r.seenOrder = nil
	r.lastSequence = 0
	r.lastEventAt = 0
	r.lastEventClock = time.Time{}
}

func (r *Runtime) setBaseLocked(state BaseState, now time.Time) bool {
	if !knownBaseState(state) {
		r.addDiagnosticLocked(IssueUnknownState, SeverityWarning, false)
		state = BaseIdle
	}
	if r.base != state {
		r.base = state
		r.baseOverride = ""
		r.baseSince = now
		if state != BaseCompleted && state != BaseFailed {
			r.action = ""
		}
		return true
	}
	return false
}

func (r *Runtime) setHiddenLocked(hidden bool, now time.Time) {
	if r.hidden == hidden {
		return
	}
	r.hidden = hidden
	if !hidden {
		// Restart the current channel from its first frame. This preserves the
		// latest normalized state without replaying time spent while hidden.
		r.actionSince = now
		r.baseSince = now
	}
}

func (r *Runtime) triggerActionLocked(name string, now time.Time) bool {
	spec, ok := r.definition.Actions[name]
	if !ok {
		if name != "" {
			r.addDiagnosticLocked(IssueUnknownAction, SeverityInfo, false)
		}
		return false
	}
	if _, ok := r.definition.Tracks[spec.Track]; !ok {
		r.addDiagnosticLocked(IssueMissingTrack, SeverityWarning, false)
		return false
	}
	if r.action != "" {
		current, exists := r.definition.Actions[r.action]
		if exists && !current.Interruptible && current.Priority >= spec.Priority {
			return false
		}
	}
	r.action = name
	r.actionSince = now
	return true
}

func (r *Runtime) baseTrackLocked() string {
	if r.baseOverride != "" {
		if _, ok := r.definition.Tracks[r.baseOverride]; ok {
			return r.baseOverride
		}
	}
	if state, ok := r.definition.States[string(r.base)]; ok {
		if _, trackOK := r.definition.Tracks[state.Track]; trackOK {
			return state.Track
		}
		if state.Fallback != "" {
			if _, trackOK := r.definition.Tracks[state.Fallback]; trackOK {
				return state.Fallback
			}
		}
	}
	return r.definition.Fallback.Idle
}

func (r *Runtime) trackSpecLocked(trackID string) TrackSpec {
	track := r.definition.Tracks[trackID]
	if r.action == "" {
		if state, ok := r.definition.States[string(r.base)]; ok && state.Track == trackID {
			track.Loop = state.Loop
			if state.Fallback != "" {
				track.Fallback = state.Fallback
			}
		}
	}
	return track
}

func (r *Runtime) lookFrameLocked() (string, int) {
	if r.look.Mode == "neutral" || r.hidden || r.reduced {
		return "", -1
	}
	if r.definition.Directions != nil && r.look.Direction >= 0 && r.look.Direction < len(r.definition.Directions.Directions) {
		return r.baseTrackLocked(), r.definition.Directions.Directions[r.look.Direction].FrameIndex
	}
	keys := make([]string, 0)
	for key, track := range r.definition.Tracks {
		if track.Channel == TrackLook && len(track.Frames) > 0 {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return "", -1
	}
	sort.Strings(keys)
	trackID := keys[0]
	track := r.definition.Tracks[trackID]
	index := r.look.Direction
	if index < 0 {
		index = 0
	}
	index %= len(track.Frames)
	return trackID, track.Frames[index].Index
}

func firstTrackFrame(track TrackSpec) int {
	if len(track.Frames) == 0 {
		return 0
	}
	return track.Frames[0].Index
}

func (r *Runtime) finishTransientLocked(at time.Time) {
	if r.action != "" {
		spec, ok := r.definition.Actions[r.action]
		track, trackOK := r.definition.Tracks[spec.Track]
		duration := time.Duration(0)
		if ok && trackOK {
			if track.TimeoutMS > 0 {
				duration = time.Duration(track.TimeoutMS) * time.Millisecond
			} else if !track.Loop {
				duration = trackDuration(track) * time.Duration(maxInt(track.RepeatCount, 1))
			}
		}
		if ok && trackOK && duration > 0 && at.Sub(r.actionSince) >= duration {
			r.action = ""
			fallback := spec.Fallback
			if fallback == "" {
				fallback = track.Fallback
			}
			if fallback != "" {
				if _, fallbackOK := r.definition.Tracks[fallback]; fallbackOK {
					r.baseOverride = fallback
				}
			}
			r.baseSince = at
		}
	}
	if r.action == "" {
		track := r.trackSpecLocked(r.baseTrackLocked())
		if !track.Loop {
			duration := trackDuration(track) * time.Duration(maxInt(track.RepeatCount, 1))
			if track.TimeoutMS > 0 {
				duration = time.Duration(track.TimeoutMS) * time.Millisecond
			}
			if duration > 0 && at.Sub(r.baseSince) >= duration {
				fallback := track.Fallback
				if fallback == "" {
					fallback = r.definition.Fallback.Idle
				}
				if _, ok := r.definition.Tracks[fallback]; ok {
					r.baseOverride = fallback
					r.baseSince = at
				}
			}
		}
	}
	if (r.base == BaseCompleted || r.base == BaseFailed) && r.action == "" {
		hold := 1500 * time.Millisecond
		if r.base == BaseFailed {
			hold = 2500 * time.Millisecond
		}
		if at.Sub(r.baseSince) >= hold {
			r.base = BaseIdle
			r.baseOverride = ""
			r.baseSince = at
		}
	}
}

func frameAt(track TrackSpec, elapsed time.Duration) int {
	if len(track.Frames) == 0 {
		return 0
	}
	if elapsed < 0 {
		elapsed = 0
	}
	total := trackDuration(track)
	if total <= 0 {
		return track.Frames[0].Index
	}
	if !track.Loop {
		repeats := maxInt(track.RepeatCount, 1)
		fullDuration := total * time.Duration(repeats)
		if elapsed >= fullDuration {
			return track.Frames[len(track.Frames)-1].Index
		}
		elapsed %= total
	}
	if track.Loop {
		elapsed %= total
	}
	for _, frame := range track.Frames {
		duration := time.Duration(frame.DurationMS) * time.Millisecond
		if elapsed < duration {
			return frame.Index
		}
		elapsed -= duration
	}
	return track.Frames[len(track.Frames)-1].Index
}

func trackDuration(track TrackSpec) time.Duration {
	var total time.Duration
	for _, frame := range track.Frames {
		if frame.DurationMS > 0 {
			total += time.Duration(frame.DurationMS) * time.Millisecond
		}
	}
	return total
}

func (r *Runtime) updateLookLocked(payload map[string]any) {
	x := numberValue(payload, "x")
	y := numberValue(payload, "y")
	gazeX := numberValue(payload, "gazeX")
	gazeY := numberValue(payload, "gazeY")
	dx := x - gazeX
	dy := y - gazeY
	if dx == 0 && dy == 0 {
		r.look = LookSnapshot{Mode: "neutral", Direction: -1}
		return
	}
	if math.Hypot(dx, dy) <= r.deadzone {
		r.look = LookSnapshot{Mode: "neutral", Direction: -1}
		return
	}
	angle := math.Atan2(dx, -dy) * 180 / math.Pi
	if angle < 0 {
		angle += 360
	}
	r.look = LookSnapshot{Mode: "directional", Angle: angle, Direction: -1}
	if r.definition.Directions != nil && len(r.definition.Directions.Directions) > 0 {
		index := int(math.Floor((angle+11.25)/22.5)) % len(r.definition.Directions.Directions)
		r.look.Direction = index
	}
	if r.definition.Inputs != nil && r.definition.Inputs["look.continuous"] {
		r.look.Mode = "continuous"
		r.look.NormalizedX = clampSigned(dx / 256)
		r.look.NormalizedY = clampSigned(dy / 256)
	}
}

func (r *Runtime) eventTimeLocked(event PetInputEvent) time.Time {
	if event.MonotonicTime > 0 && r.lastEventAt > 0 && !r.lastEventClock.IsZero() {
		return r.lastEventClock.Add(time.Duration(event.MonotonicTime - r.lastEventAt))
	}
	return r.clock()
}

func (r *Runtime) addDiagnosticLocked(code string, severity IssueSeverity, retryable bool) {
	for _, issue := range r.diagnostics {
		if issue.Code == code {
			return
		}
	}
	r.diagnostics = appendIssue(r.diagnostics, Issue{Code: code, Severity: severity, Retryable: retryable, SafeFallbackSummary: defaultIssueSummary(code)})
}

func knownBaseState(state BaseState) bool {
	switch state {
	case BaseStarting, BaseIdle, BaseWorking, BaseWaiting, BaseReviewing, BaseCompleted, BaseFailed, BaseOffline:
		return true
	default:
		return false
	}
}

func runtimeContextError(ctx context.Context) error {
	if ctx == nil || ctx.Err() == nil {
		return nil
	}
	return ctx.Err()
}

func numberValue(payload map[string]any, key string) float64 {
	if payload == nil {
		return 0
	}
	value := payload[key]
	switch value := value.(type) {
	case float64:
		return value
	case float32:
		return float64(value)
	case int:
		return float64(value)
	case int64:
		return float64(value)
	case uint64:
		return float64(value)
	case json.Number:
		parsed, _ := value.Float64()
		return parsed
	case jsonNumber:
		parsed, _ := strconv.ParseFloat(string(value), 64)
		return parsed
	case string:
		parsed, _ := strconv.ParseFloat(strings.TrimSpace(value), 64)
		return parsed
	default:
		return 0
	}
}

// jsonNumber is kept local to avoid importing encoding/json into the runtime
// just to accept one event-port representation.
type jsonNumber string

func boolValue(payload map[string]any, key string) bool {
	if payload == nil {
		return false
	}
	value, ok := payload[key]
	if !ok {
		return false
	}
	if result, ok := value.(bool); ok {
		return result
	}
	return strings.EqualFold(strings.TrimSpace(toString(value)), "true")
}

func toString(value any) string {
	switch value := value.(type) {
	case string:
		return value
	case bool:
		if value {
			return "true"
		}
		return "false"
	default:
		return ""
	}
}

func clamp01(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func clampSigned(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	if value < -1 {
		return -1
	}
	if value > 1 {
		return 1
	}
	return value
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}
