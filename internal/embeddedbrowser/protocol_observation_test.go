package embeddedbrowser

import (
	"encoding/json"
	"testing"
)

func TestCommandObservationCorrelatesMetadataAndRetiresRoute(t *testing.T) {
	var events []ProtocolObservation
	observer := newCommandObserver(func(value ProtocolObservation) { events = append(events, value) })
	observer.command("private-route-a", json.RawMessage(`{"id":7,"method":"devtools.dispatch","params":[{},"Runtime.evaluate",{"expression":"secret page body"}]}`))
	observer.command("private-route-b", json.RawMessage(`{"id":7,"method":"devtools.dispatch","params":[{},"Page.getFrameTree"]}`))
	observer.accepted("private-route-a", 7, "Runtime.evaluate")
	observer.response("private-route-b", json.RawMessage(`{"id":7,"result":{"secret":"page body"}}`))
	observer.response("private-route-a", json.RawMessage(`{"id":7,"error":{"message":"private error body"}}`))
	if len(events) != 5 || events[0].Route == events[1].Route || events[3].Method != "Page.getFrameTree" || events[4].Method != "Runtime.evaluate" {
		t.Fatal(events)
	}
	for _, value := range events {
		if value.ID != 7 || value.At.IsZero() {
			t.Fatal(value)
		}
	}
	observer.command("private-route-a", json.RawMessage(`{"id":8,"method":"devtools.dispatch","params":[{},"Runtime.evaluate"]}`))
	observer.retire("private-route-a")
	n := len(events)
	observer.response("private-route-a", json.RawMessage(`{"id":8,"result":{}}`))
	observer.command("private-route-b", json.RawMessage(`{"id":9,"method":"devtools.dispatch","params":[{},"page URL\nsecret"]}`))
	if len(events) != n || len(observer.pending) != 0 {
		t.Fatal("retired or invalid metadata emitted", events)
	}
}
