package embeddedbrowser

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestFrameGraphRetainsDetachedDocumentAndRejectsMissingEvents(t *testing.T) {
	graph := &frameGraph{}
	frame := "5-00112233445566778899AABBCCDDEEFF"
	revision := uint64(0)
	apply := func(action string, attached bool) {
		t.Helper()
		revision++
		event := frameEvent{Revision: fmt.Sprint(revision), FaultEpoch: "0", Action: action, FrameRecord: FrameRecord{Owner: "own-target", Tab: 1, Browser: 1, Frame: frame, Document: "1", Main: true, Attached: attached}}
		raw, _ := json.Marshal(event)
		if e := graph.apply(raw); e != nil {
			t.Fatal(action, e)
		}
	}
	apply("created", false)
	revision++
	main, _ := json.Marshal(frameEvent{Revision: fmt.Sprint(revision), FaultEpoch: "0", Action: "main-changed", FrameRecord: FrameRecord{Owner: "own-target", Tab: 1, Browser: 1, Frame: frame, Main: true}})
	if e := graph.apply(main); e != nil {
		t.Fatal(e)
	}
	apply("attached", true)
	apply("detached", false)
	if node := graph.frames[frame]; node.Attached || node.Document != "1" || node.Child != 5 {
		t.Fatal("detached document association lost", node)
	}
	apply("attached", true)
	if graph.frames[frame].Document != "1" {
		t.Fatal("reattachment invented a new document")
	}
	before := graph.revision
	malformed, _ := json.Marshal(frameEvent{Revision: fmt.Sprint(before + 2), FaultEpoch: "0", Action: "destroyed", FrameRecord: graph.frames[frame]})
	if graph.apply(malformed) == nil || graph.revision != before || len(graph.frames) != 1 {
		t.Fatal("missing lifecycle event accepted")
	}
	apply("destroyed", false)
	if len(graph.frames) != 0 {
		t.Fatal("actual destruction retained live association")
	}
	revision++
	terminal, _ := json.Marshal(frameEvent{Revision: fmt.Sprint(revision), FaultEpoch: "0", Action: "main-changed", Previous: frame, FrameRecord: FrameRecord{Owner: "own-target", Tab: 1, Browser: 1, Main: true}})
	if e := graph.apply(terminal); e != nil || len(graph.mains) != 0 {
		t.Fatal("final main lifetime not removed", e)
	}
}

func TestFrameGraphTracksEachTabMainIndependently(t *testing.T) {
	graph := &frameGraph{}
	for tab := 1; tab <= 2; tab++ {
		frame := fmt.Sprintf("%X-00112233445566778899AABBCCDDEEFF", tab)
		created, _ := json.Marshal(frameEvent{Revision: fmt.Sprint(2*tab - 1), FaultEpoch: "0", Action: "created", FrameRecord: FrameRecord{Owner: "same-route", Tab: tab, Browser: tab, Frame: frame, Document: fmt.Sprint(tab), Main: true}})
		if err := graph.apply(created); err != nil {
			t.Fatal(err)
		}
		changed, _ := json.Marshal(frameEvent{Revision: fmt.Sprint(2 * tab), FaultEpoch: "0", Action: "main-changed", FrameRecord: FrameRecord{Owner: "same-route", Tab: tab, Browser: tab, Frame: frame, Main: true}})
		if err := graph.apply(changed); err != nil {
			t.Fatalf("tab %d initial main: %v", tab, err)
		}
	}
	if len(graph.mains) != 2 || graph.mains[frameTab{"same-route", 1}] == graph.mains[frameTab{"same-route", 2}] {
		t.Fatalf("two live tab mains missing: %+v", graph.mains)
	}
	wrongPrevious, _ := json.Marshal(frameEvent{Revision: "5", FaultEpoch: "0", Action: "main-changed", Previous: graph.mains[frameTab{"same-route", 1}], FrameRecord: FrameRecord{Owner: "same-route", Tab: 2, Browser: 2, Main: true}})
	if err := graph.apply(wrongPrevious); err == nil || err.Error() != "CEF_GRAPH_PREVIOUS_MAIN_REJECTED" || graph.revision != 4 {
		t.Fatalf("cross-tab previous main accepted: %v", err)
	}
}
