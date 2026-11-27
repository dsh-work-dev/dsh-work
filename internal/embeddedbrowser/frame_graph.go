package embeddedbrowser

import (
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
)

// FrameRecord is lifetime evidence, never Agent authority. Child is the CEF
// logical renderer ID encoded in Frame, not an OS PID. Document names a native
// load epoch; OwnedDocument is the existing main-document authorization epoch.
// Detached records survive BFCache until the actual Destroyed callback.
type FrameRecord struct {
	Owner         string `json:"owner"`
	Tab           int    `json:"tab"`
	Browser       int    `json:"browser"`
	Frame         string `json:"frame"`
	Parent        string `json:"parent"`
	Child         uint32 `json:"logicalChild"`
	Document      string `json:"document"`
	OwnedDocument string `json:"ownedDocument,omitempty"`
	Main          bool   `json:"main"`
	Attached      bool   `json:"attached"`
}

type FrameGraphSnapshot struct {
	Generation string            `json:"generation"`
	Revision   uint64            `json:"revision"`
	FaultEpoch uint64            `json:"faultEpoch"`
	Frames     []FrameRecord     `json:"frames"`
	MainFrames map[string]string `json:"mainFrames"`
	// This first layer records CEF events only. Kernel instance joins are an
	// independent prerequisite; a complete-looking frame set is not a full map.
	TopologyComplete      bool               `json:"topologyComplete"`
	ProcessJoinComplete   bool               `json:"processJoinComplete"`
	UnknownReason         string             `json:"unknownReason,omitempty"`
	ControlPauseConfirmed bool               `json:"controlPauseConfirmed"`
	Processes             []RendererInstance `json:"processes,omitempty"`
	Influence             RendererInfluence  `json:"influence"`
}

type frameEvent struct {
	Revision   string `json:"revision"`
	FaultEpoch string `json:"faultEpoch"`
	Action     string `json:"action"`
	Previous   string `json:"previous"`
	Reattached bool   `json:"reattached"`
	FrameRecord
}
type frameGraph struct {
	invalidReason   string
	revision, fault uint64
	frames          map[string]FrameRecord
	mains           map[string]string
}

func graphSequence(raw string) (uint64, error) {
	value, e := strconv.ParseUint(raw, 10, 64)
	if e != nil || strconv.FormatUint(value, 10) != raw {
		return 0, errors.New("CEF_GRAPH_SEQUENCE_REJECTED")
	}
	return value, nil
}
func frameChild(id string) (uint32, error) {
	prefix, suffix, ok := strings.Cut(id, "-")
	value, e := strconv.ParseUint(prefix, 16, 31)
	if !ok || e != nil || value == 0 || len(suffix) != 32 {
		return 0, errors.New("CEF_GRAPH_FRAME_REJECTED")
	}
	if _, e = strconv.ParseUint(suffix[:16], 16, 64); e != nil {
		return 0, errors.New("CEF_GRAPH_FRAME_REJECTED")
	}
	if _, e = strconv.ParseUint(suffix[16:], 16, 64); e != nil {
		return 0, errors.New("CEF_GRAPH_FRAME_REJECTED")
	}
	return uint32(value), nil
}
func (g *frameGraph) apply(raw json.RawMessage) error {
	var event frameEvent
	if e := json.Unmarshal(raw, &event); e != nil {
		return errors.New("CEF_GRAPH_EVENT_REJECTED")
	}
	revision, e := graphSequence(event.Revision)
	if e != nil || revision != g.revision+1 {
		return errors.New("CEF_GRAPH_REVISION_REJECTED")
	}
	fault, e := graphSequence(event.FaultEpoch)
	if e != nil || fault != g.fault {
		return errors.New("CEF_GRAPH_FAULT_EPOCH_REJECTED")
	}
	if event.Owner == "" || len(event.Owner) > 128 || event.Tab <= 0 || event.Browser < 0 {
		return errors.New("CEF_GRAPH_OWNER_REJECTED")
	}
	if g.frames == nil {
		g.frames = map[string]FrameRecord{}
		g.mains = map[string]string{}
	}
	if event.Action == "main-changed" {
		if event.Frame != "" {
			if _, e = frameChild(event.Frame); e != nil {
				return e
			}
			frame, ok := g.frames[event.Frame]
			if !ok || frame.Owner != event.Owner || frame.Tab != event.Tab || !frame.Main {
				return errors.New("CEF_GRAPH_MAIN_REJECTED")
			}
		}
		if previous := g.mains[event.Owner]; previous != event.Previous {
			return errors.New("CEF_GRAPH_PREVIOUS_MAIN_REJECTED")
		}
		if event.Frame == "" {
			delete(g.mains, event.Owner)
		} else {
			g.mains[event.Owner] = event.Frame
		}
		g.revision = revision
		return nil
	}
	child, e := frameChild(event.Frame)
	if e != nil {
		return e
	}
	event.Child = child
	if event.Parent != "" {
		if _, e = frameChild(event.Parent); e != nil {
			return e
		}
	}
	if event.Document == "" {
		return errors.New("CEF_GRAPH_DOCUMENT_REJECTED")
	}
	if value, err := graphSequence(event.Document); err != nil || value == 0 {
		e = errors.New("CEF_GRAPH_DOCUMENT_REJECTED")
		return e
	}
	if event.OwnedDocument != "" {
		if value, err := graphSequence(event.OwnedDocument); err != nil || value == 0 {
			e = errors.New("CEF_GRAPH_DOCUMENT_REJECTED")
			return e
		}
	}
	current, exists := g.frames[event.Frame]
	if event.Action == "created" {
		if exists || event.Attached {
			return errors.New("CEF_GRAPH_CREATE_REJECTED")
		}
	} else {
		if !exists || current.Owner != event.Owner || current.Tab != event.Tab || current.Main != event.Main {
			return errors.New("CEF_GRAPH_EDGE_REJECTED")
		}
		if current.Browser > 0 && event.Browser != current.Browser {
			return errors.New("CEF_GRAPH_BROWSER_CHANGED")
		}
		switch event.Action {
		case "bound":
			if event.Browser <= 0 {
				return errors.New("CEF_GRAPH_BROWSER_REJECTED")
			}
		case "attached":
			if !event.Attached || event.Browser <= 0 {
				return errors.New("CEF_GRAPH_ATTACH_REJECTED")
			}
		case "detached", "destroyed":
			if event.Attached {
				return errors.New("CEF_GRAPH_DETACH_REJECTED")
			}
		case "document", "owned-document":
		default:
			return errors.New("CEF_GRAPH_ACTION_REJECTED")
		}
	}
	if event.Action == "destroyed" {
		delete(g.frames, event.Frame)
	} else {
		g.frames[event.Frame] = event.FrameRecord
	}
	g.revision = revision
	return nil
}

// FrameGraph is the continuously maintained authenticated CEF graph. It does
// not query DevTools or reconstruct ownership from a trace/page-supplied ID.
func (b *Broker) frameGraphOnly() FrameGraphSnapshot {
	b.mu.Lock()
	defer b.mu.Unlock()
	snapshot := FrameGraphSnapshot{Generation: b.generation, MainFrames: map[string]string{}}
	if b.frames == nil {
		return snapshot
	}
	snapshot.Revision = b.frames.revision
	snapshot.FaultEpoch = b.frames.fault
	for _, frame := range b.frames.frames {
		snapshot.Frames = append(snapshot.Frames, frame)
	}
	sort.Slice(snapshot.Frames, func(i, j int) bool { return snapshot.Frames[i].Frame < snapshot.Frames[j].Frame })
	for owner, frame := range b.frames.mains {
		snapshot.MainFrames[owner] = frame
	}
	snapshot.TopologyComplete = b.frames.invalidReason == "" && b.frames.coherent()
	snapshot.UnknownReason = b.frames.invalidReason
	if snapshot.UnknownReason == "" {
		snapshot.UnknownReason = b.rendererJoinReason
	}
	snapshot.ControlPauseConfirmed = b.controlPauseConfirmed
	return snapshot
}

func (b *Broker) FrameGraph() FrameGraphSnapshot {
	snapshot := b.frameGraphOnly()
	if b.renderers != nil {
		snapshot.Processes, snapshot.Influence, snapshot.ProcessJoinComplete = b.renderers.snapshot(snapshot.Revision, snapshot.FaultEpoch)
		snapshot.ProcessJoinComplete = snapshot.ProcessJoinComplete && snapshot.TopologyComplete && snapshot.UnknownReason == ""
		if snapshot.FaultEpoch == 0 {
			snapshot.Influence.Complete = snapshot.Influence.Complete && snapshot.TopologyComplete && snapshot.UnknownReason == ""
		}
	}
	return snapshot
}

func (g *frameGraph) coherent() bool {
	for owner, main := range g.mains {
		frame, ok := g.frames[main]
		if !ok || !frame.Main || frame.Owner != owner {
			return false
		}
	}
	for _, frame := range g.frames {
		if frame.Browser <= 0 {
			return false
		}
		seen := map[string]bool{}
		current := frame
		for {
			if seen[current.Frame] {
				return false
			}
			seen[current.Frame] = true
			if current.Parent == "" {
				if !current.Main || current.OwnedDocument == "" {
					return false
				}
				break
			}
			parent, ok := g.frames[current.Parent]
			if !ok || parent.Owner != frame.Owner || parent.Tab != frame.Tab || parent.Browser != frame.Browser {
				return false
			}
			current = parent
		}
	}
	return true
}

// FaultInfluence returns the retained fence for one callback epoch. Later
// callbacks and live frame retirement do not replace this record.
func (b *Broker) FaultInfluence(epoch uint64) (RendererInfluence, bool) {
	if b.renderers == nil {
		return RendererInfluence{}, false
	}
	_, value, _ := b.renderers.snapshot(0, epoch)
	return value, value.FaultEpoch == epoch && epoch != 0
}
