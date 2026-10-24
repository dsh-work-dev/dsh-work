package desktopapp

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/acquisition"
	"github.com/local/dsh-work/internal/hostevents"
)

func TestAcquisitionStateOmitsLogsAndLogCommandsRemainOneShot(t *testing.T) {
	hub := hostevents.New()
	status := acquisition.OperationStatus{OperationID: "op-1", State: acquisition.OperationActive, Step: "output", Log: "old output"}
	publishAcquisitionStatus(hub, status, nil)
	_, initial := hub.Subscribe()
	if len(initial) != 1 || initial[0].Topic != "acquisition" {
		t.Fatalf("initial acquisition snapshot = %+v", initial)
	}
	var state acquisition.OperationStatus
	if err := json.Unmarshal(initial[0].Value, &state); err != nil {
		t.Fatal(err)
	}
	if state.Log != "" {
		t.Fatalf("acquisition topic retained log %q", state.Log)
	}

	subscription, _ := hub.Subscribe()
	defer subscription.Close()
	var emitted []acquisition.OperationStatus
	for range 2 {
		publishAcquisitionStatus(hub, status, func(value acquisition.OperationStatus) { emitted = append(emitted, value) })
	}
	for i := 0; i < 2; i++ {
		select {
		case message := <-subscription.Messages:
			if message.Type != "command" || message.Name != "acquisition-log" {
				t.Fatalf("acquisition stream message = %+v, want acquisition-log command", message)
			}
			var got acquisition.OperationStatus
			if err := json.Unmarshal(message.Data, &got); err != nil {
				t.Fatal(err)
			}
			if got.Log != "old output" {
				t.Fatalf("acquisition-log batch = %+v", got)
			}
		case <-time.After(time.Second):
			t.Fatalf("acquisition-log batch %d was not delivered", i)
		}
	}
	if len(emitted) != 2 || emitted[0].Log != emitted[1].Log {
		t.Fatalf("duplicate in-process acquisition events = %+v", emitted)
	}
}
