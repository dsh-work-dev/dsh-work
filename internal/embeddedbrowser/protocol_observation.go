package embeddedbrowser

import (
	"encoding/json"
	"sync"
	"time"
)

// ProtocolObservation contains command metadata only. Route is a local ordinal,
// never the private transport route value. At is observed in the Go owner:
// accepted means the native SendDevToolsMessage call accepted the command;
// response means its result reached this owner, not that the SDK consumed it.
type ProtocolObservation struct {
	Stage  string
	Route  uint64
	ID     int
	Method string
	At     time.Time
}
type commandKey struct {
	route string
	id    int
}
type commandObserver struct {
	mu      sync.Mutex
	emit    func(ProtocolObservation)
	routes  map[string]uint64
	pending map[commandKey]string
	next    uint64
}

func newCommandObserver(emit func(ProtocolObservation)) *commandObserver {
	if emit == nil {
		return nil
	}
	return &commandObserver{emit: emit, routes: map[string]uint64{}, pending: map[commandKey]string{}}
}
func metadataMethod(method string) bool {
	if len(method) < 3 || len(method) > 96 {
		return false
	}
	dot := false
	for _, c := range method {
		if c == '.' {
			if dot {
				return false
			}
			dot = true
			continue
		}
		if (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') {
			return false
		}
	}
	return dot && method[0] != '.' && method[len(method)-1] != '.'
}
func (o *commandObserver) command(route string, raw json.RawMessage) {
	if o == nil {
		return
	}
	var value struct {
		ID     int
		Method string
		Params []json.RawMessage
	}
	if json.Unmarshal(raw, &value) != nil || value.ID <= 0 || value.Method != "devtools.dispatch" || len(value.Params) < 2 {
		return
	}
	var method string
	if json.Unmarshal(value.Params[1], &method) != nil || !metadataMethod(method) {
		return
	}
	o.mu.Lock()
	if len(o.pending) >= 4096 {
		o.mu.Unlock()
		return
	}
	ordinal := o.routes[route]
	if ordinal == 0 {
		o.next++
		ordinal = o.next
		o.routes[route] = ordinal
	}
	o.pending[commandKey{route, value.ID}] = method
	o.mu.Unlock()
	o.emit(ProtocolObservation{Stage: "send", Route: ordinal, ID: value.ID, Method: method, At: time.Now()})
}
func (o *commandObserver) accepted(route string, id int, method string) {
	if o == nil || id <= 0 || !metadataMethod(method) {
		return
	}
	o.mu.Lock()
	ordinal := o.routes[route]
	o.mu.Unlock()
	if ordinal != 0 {
		o.emit(ProtocolObservation{Stage: "accepted", Route: ordinal, ID: id, Method: method, At: time.Now()})
	}
}
func (o *commandObserver) response(route string, raw json.RawMessage) {
	if o == nil {
		return
	}
	var value struct{ ID int }
	if json.Unmarshal(raw, &value) != nil || value.ID <= 0 {
		return
	}
	key := commandKey{route, value.ID}
	o.mu.Lock()
	method := o.pending[key]
	ordinal := o.routes[route]
	delete(o.pending, key)
	o.mu.Unlock()
	if method != "" {
		o.emit(ProtocolObservation{Stage: "response", Route: ordinal, ID: value.ID, Method: method, At: time.Now()})
	}
}
func (o *commandObserver) retire(route string) {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.routes, route)
	for key := range o.pending {
		if key.route == route {
			delete(o.pending, key)
		}
	}
}
