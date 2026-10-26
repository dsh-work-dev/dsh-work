package hostevents

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

func TestMessageJSONUsesOnlyFieldsForItsRowType(t *testing.T) {
	command := Message{Type: "command", Name: "notification-failure", Window: "", ExpiresAt: 1234, Data: json.RawMessage("true")}
	transient := Message{Type: "command", Name: "acquisition-log", Window: "", Data: json.RawMessage(`{"log":"line"}`)}
	state := Message{Type: "state", Topic: "worker-url", Value: json.RawMessage(`"url"`)}
	ping := Message{Type: "ping"}
	for _, test := range []struct {
		message Message
		keys    []string
	}{
		{state, []string{"type", "topic", "value"}},
		{command, []string{"type", "name", "window", "expiresAt", "data"}},
		{transient, []string{"type", "name", "window", "data"}},
		{ping, []string{"type"}},
	} {
		data, err := json.Marshal(test.message)
		if err != nil {
			t.Fatal(err)
		}
		var decoded map[string]json.RawMessage
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatal(err)
		}
		if len(decoded) != len(test.keys) {
			t.Fatalf("message fields = %s", data)
		}
		for _, key := range test.keys {
			if _, ok := decoded[key]; !ok {
				t.Errorf("message %s omitted %q", data, key)
			}
		}
	}
}

func TestSubscribeWritesTopicsInSetOrderAndSuppressesEquivalentValues(t *testing.T) {
	hub := New()
	for _, topic := range []string{"lifecycle", "worker-url", "update", "preferences", "acquisition"} {
		hub.SetTopic(topic, topic)
	}
	subscription, initial := hub.Subscribe()
	defer subscription.Close()
	if len(initial) != 5 {
		t.Fatalf("initial messages = %d, want 5", len(initial))
	}
	for i, topic := range []string{"lifecycle", "worker-url", "update", "preferences", "acquisition"} {
		if initial[i].Type != "state" || initial[i].Topic != topic {
			t.Fatalf("initial[%d] = %+v, want state topic %q", i, initial[i], topic)
		}
	}
	hub.SetTopic("update", "update")
	select {
	case message := <-subscription.Messages:
		t.Fatalf("unchanged topic was pushed: %+v", message)
	default:
	}
	hub.SetTopic("update", "ready")
	if message := <-subscription.Messages; message.Topic != "update" || string(message.Value) != `"ready"` {
		t.Fatalf("changed state message = %+v", message)
	}
}

func TestStagedCommandsAreBoundedOrderedAndExpire(t *testing.T) {
	now := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	hub := NewWithClock(func() time.Time { return now })
	hub.SendCommand("", "expired", time.Second, false)
	now = now.Add(2 * time.Second)
	for i := 0; i < commandBuffer+1; i++ {
		hub.SendCommand("", "queued", time.Minute, i)
	}
	_, initial := hub.Subscribe()
	if len(initial) != commandBuffer {
		t.Fatalf("staged commands = %d, want %d", len(initial), commandBuffer)
	}
	for i, message := range initial {
		var value int
		if message.Type != "command" || message.Name != "queued" || message.Window != "" {
			t.Fatalf("initial[%d] = %+v", i, message)
		}
		if err := json.Unmarshal(message.Data, &value); err != nil {
			t.Fatal(err)
		}
		if value != i+1 {
			t.Fatalf("initial[%d] value = %d, want %d", i, value, i+1)
		}
	}
}

func TestAcquisitionLogsAreNotReplayedButIdenticalLiveBatchesAreDelivered(t *testing.T) {
	hub := New()
	hub.SendTransient("", "acquisition-log", map[string]string{"log": "old"})
	initialSubscription, initial := hub.Subscribe()
	defer initialSubscription.Close()
	if len(initial) != 0 {
		t.Fatalf("new subscription received prior acquisition log: %+v", initial)
	}

	subscription, _ := hub.Subscribe()
	defer subscription.Close()
	data := map[string]string{"log": "same batch"}
	hub.SendTransient("", "acquisition-log", data)
	hub.SendTransient("", "acquisition-log", data)
	for i := 0; i < 2; i++ {
		select {
		case message := <-subscription.Messages:
			if message.Type != "command" || message.Name != "acquisition-log" || string(message.Data) != `{"log":"same batch"}` {
				t.Fatalf("live acquisition batch %d = %+v", i, message)
			}
		case <-time.After(time.Second):
			t.Fatalf("live acquisition batch %d was not delivered", i)
		}
	}
}

func TestSendTransientWithoutSubscribersIsNotStaged(t *testing.T) {
	hub := New()
	hub.SendTransient("workspace", "transient", "dropped")

	subscription, initial := hub.Subscribe()
	defer subscription.Close()
	if len(initial) != 0 {
		t.Fatalf("transient command appeared in initial subscription: %+v", initial)
	}
	select {
	case message := <-subscription.Messages:
		t.Fatalf("transient command was staged for a later subscriber: %+v", message)
	default:
	}
}

func TestSendTransientBroadcastsToCurrentSubscribersWithoutExpiry(t *testing.T) {
	hub := New()
	subscription, _ := hub.Subscribe()
	defer subscription.Close()
	hub.SendTransient("workspace", "transient", "now")
	select {
	case message := <-subscription.Messages:
		if message.Type != "command" || message.Name != "transient" || message.Window != "workspace" || message.ExpiresAt != 0 || string(message.Data) != `"now"` {
			t.Fatalf("transient command = %+v", message)
		}
	case <-time.After(time.Second):
		t.Fatal("transient command was not delivered to the current subscriber")
	}
}

func TestFullSubscriberQueueClosesSubscription(t *testing.T) {
	hub := New()
	subscription, _ := hub.Subscribe()
	defer subscription.Close()
	for i := 0; i < subscriberBuffer+1; i++ {
		hub.SetTopic("state", fmt.Sprintf("%d", i))
	}
	select {
	case <-subscription.Done:
	case <-time.After(time.Second):
		t.Fatal("full subscription queue remained open")
	}
}
