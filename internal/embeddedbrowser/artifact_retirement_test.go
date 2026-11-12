package embeddedbrowser

import (
	"context"
	"errors"
	"testing"

	"github.com/local/dsh-work/internal/supervisor"
)

type retiredArtifactCensus struct{}

func (retiredArtifactCensus) OwnedProcesses(context.Context) ([]supervisor.ProcessLease, error) {
	return nil, errors.New("owned terminating process image unavailable")
}

// Deterministic ACK/last-main-event ordering; real chooser/engine evidence is
// TestLiveArtifactProductSelection, not this scheduling contract.
func TestArtifactRetirementRequiresACKAndFinalEmptyGraph(t *testing.T) {
	b := &Broker{done: make(chan struct{}), closed: make(chan struct{}), frames: &frameGraph{revision: 8, frames: map[string]FrameRecord{}, mains: map[frameTab]string{{owner: "artifact", tab: 1}: "old-frame"}}}
	r := &rendererTracker{broker: b, inventory: retiredArtifactCensus{}, held: map[uint32]*rendererLease{}, established: true}
	b.renderers = r
	r.beginArtifactRetirement("artifact")
	r.refresh()
	if b.ControlPaused() || !r.established || !r.joinUncertain {
		t.Fatal("pending retirement was faulted or treated as a reliable census")
	}
	r.finishArtifactRetirement("artifact")
	if !r.established || len(r.artifactRetiring) != 1 {
		t.Fatal("context ACK without final main-frame event reset continuity")
	}
	b.frames.mains = map[frameTab]string{}
	b.frames.revision++
	r.refresh()
	if b.ControlPaused() || r.established || r.complete || !r.joinUncertain || len(r.artifactRetiring) != 0 {
		t.Fatal("fully retired idle scope did not remain pending fresh census")
	}
	// A completed teardown is not an automatic recovery from an earlier fault.
	close(b.done)
	r.established = true
	r.beginArtifactRetirement("old-fault")
	r.finishArtifactRetirement("old-fault")
	if !r.established || !b.ControlPaused() {
		t.Fatal("artifact close cleared an existing fault")
	}
}
