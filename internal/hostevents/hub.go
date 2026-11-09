// Package hostevents owns daemon state topics and expiring, one-shot commands.
// It is transport-neutral so the same vocabulary can back local or remote
// surfaces without depending on IPC or desktop frameworks.
package hostevents

import (
	"encoding/json"
	"sync"
	"time"
)

const (
	subscriberBuffer = 256
	commandBuffer    = 64
)

// Message is one state update, one-shot command, or stream heartbeat.
type Message struct {
	Type      string          `json:"type"`
	Topic     string          `json:"topic,omitempty"`
	Value     json.RawMessage `json:"value,omitempty"`
	Name      string          `json:"name,omitempty"`
	Window    string          `json:"window"`
	ExpiresAt int64           `json:"expiresAt,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
}

func (m Message) MarshalJSON() ([]byte, error) {
	type encoded struct {
		Type      string          `json:"type"`
		Topic     string          `json:"topic,omitempty"`
		Value     json.RawMessage `json:"value,omitempty"`
		Name      string          `json:"name,omitempty"`
		Window    *string         `json:"window,omitempty"`
		ExpiresAt *int64          `json:"expiresAt,omitempty"`
		Data      json.RawMessage `json:"data,omitempty"`
	}
	value := encoded{Type: m.Type, Topic: m.Topic, Value: m.Value, Name: m.Name, Data: m.Data}
	if m.Type == "command" {
		window := m.Window
		value.Window = &window
		if m.ExpiresAt != 0 {
			expiresAt := m.ExpiresAt
			value.ExpiresAt = &expiresAt
		}
	}
	return json.Marshal(value)
}

// Subscription receives messages committed after its initial snapshot.
// Initial messages are returned separately so a stream writer can write them
// before reading the live queue. Both are captured while the Hub lock is held.
type Subscription struct {
	Messages <-chan Message
	Done     <-chan struct{}

	hub *Hub
	id  uint64
}

func (s Subscription) Close() {
	if s.hub != nil {
		s.hub.unsubscribe(s.id)
	}
}

type subscriber struct {
	windows  map[string]bool
	messages chan Message
	done     chan struct{}
}

// Hub holds the current value of each topic and bounded queues for live
// subscribers. The order in which topics are first set is the snapshot order.
type Hub struct {
	mu          sync.Mutex
	now         func() time.Time
	topics      map[string]json.RawMessage
	topicOrder  []string
	commands    []Message
	subscribers map[uint64]*subscriber
	nextID      uint64
}

func New() *Hub { return NewWithClock(time.Now) }

// NewWithClock provides deterministic expiry behavior to the protocol tests.
func NewWithClock(now func() time.Time) *Hub {
	if now == nil {
		now = time.Now
	}
	return &Hub{
		now:         now,
		topics:      make(map[string]json.RawMessage),
		subscribers: make(map[uint64]*subscriber),
	}
}

// SetTopic retains only the newest JSON value and suppresses equivalent
// updates. A value that cannot be encoded is ignored.
func (h *Hub) SetTopic(topic string, value any) {
	if h == nil || topic == "" {
		return
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if previous, ok := h.topics[topic]; ok && string(previous) == string(raw) {
		return
	}
	if _, ok := h.topics[topic]; !ok {
		h.topicOrder = append(h.topicOrder, topic)
	}
	h.topics[topic] = append(json.RawMessage(nil), raw...)
	h.broadcastLocked(Message{Type: "state", Topic: topic, Value: append(json.RawMessage(nil), raw...)})
}

// SendCommand delivers a command once. When there is no subscriber it keeps
// the command until the next subscription, subject to the TTL and queue cap.
func (h *Hub) SendCommand(window, name string, ttl time.Duration, data any) {
	if h == nil || name == "" {
		return
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.now()
	message := Message{Type: "command", Name: name, Window: window, ExpiresAt: now.Add(ttl).UnixMilli(), Data: append(json.RawMessage(nil), raw...)}
	if !h.hasSubscriberLocked(window) {
		if len(h.commands) == commandBuffer {
			copy(h.commands, h.commands[1:])
			h.commands = h.commands[:len(h.commands)-1]
		}
		h.commands = append(h.commands, message)
		return
	}
	h.broadcastLocked(message)
}

// SendTransient delivers a command only to current subscribers. It never
// queues the command for a later subscription and carries no expiry timestamp.
func (h *Hub) SendTransient(window, name string, data any) {
	if h == nil || name == "" {
		return
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.subscribers) == 0 {
		return
	}
	h.broadcastLocked(Message{Type: "command", Name: name, Window: window, Data: append(json.RawMessage(nil), raw...)})
}

// Subscribe atomically snapshots topics, takes unexpired staged commands and
// begins buffering later messages in a 256-entry queue.
func (h *Hub) Subscribe() (Subscription, []Message) { return h.subscribe(nil) }

// SubscribeWindows scopes commands to admitted windows. Unmatched staged
// commands remain queued for their owner; state topics remain shared.
func (h *Hub) SubscribeWindows(windows []string) (Subscription, []Message) {
	allowed := make(map[string]bool, len(windows))
	for _, window := range windows {
		allowed[window] = true
	}
	return h.subscribe(allowed)
}
func (h *Hub) subscribe(windows map[string]bool) (Subscription, []Message) {
	if h == nil {
		closed := make(chan struct{})
		close(closed)
		return Subscription{Messages: make(chan Message), Done: closed}, nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.nextID++
	current := &subscriber{windows: windows, messages: make(chan Message, subscriberBuffer), done: make(chan struct{})}
	h.subscribers[h.nextID] = current

	initial := make([]Message, 0, len(h.topicOrder)+len(h.commands))
	for _, topic := range h.topicOrder {
		initial = append(initial, Message{Type: "state", Topic: topic, Value: append(json.RawMessage(nil), h.topics[topic]...)})
	}
	now := h.now().UnixMilli()
	var retained []Message
	for _, message := range h.commands {
		if message.ExpiresAt <= now {
			continue
		}
		if current.accepts(message.Window) {
			initial = append(initial, cloneMessage(message))
		} else {
			retained = append(retained, message)
		}
	}
	h.commands = retained
	return Subscription{Messages: current.messages, Done: current.done, hub: h, id: h.nextID}, initial
}

func (h *Hub) broadcastLocked(message Message) {
	for id, current := range h.subscribers {
		if message.Type == "command" && !current.accepts(message.Window) {
			continue
		}
		select {
		case current.messages <- cloneMessage(message):
		default:
			// A slow reader must not stall state producers. Closing Done tells
			// the stream owner to tear down this connection.
			delete(h.subscribers, id)
			close(current.done)
		}
	}
}

func (h *Hub) unsubscribe(id uint64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if current, ok := h.subscribers[id]; ok {
		delete(h.subscribers, id)
		close(current.done)
	}
}

func cloneMessage(message Message) Message {
	message.Value = append(json.RawMessage(nil), message.Value...)
	message.Data = append(json.RawMessage(nil), message.Data...)
	return message
}

func (s *subscriber) accepts(window string) bool {
	return window == "" || s.windows == nil || s.windows[window]
}
func (h *Hub) hasSubscriberLocked(window string) bool {
	for _, subscriber := range h.subscribers {
		if subscriber.accepts(window) {
			return true
		}
	}
	return false
}
