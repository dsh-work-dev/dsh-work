package desktopbridge

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestWebSocketRejectsRoutesOutsideGatewayMux(t *testing.T) {
	meta, err := json.Marshal(map[string]any{"URL": "/__work/probe-ws", "Generation": "g1"})
	if err != nil {
		t.Fatal(err)
	}
	conn := &testConn{ctx: context.Background(), in: make(chan []byte, 1), out: make(chan []byte, 1)}
	conn.in <- meta
	bridge := &Bridge{Origin: "http://127.0.0.1:1", Generation: "g1"}
	if err := bridge.WebSocket(conn); err == nil || !strings.Contains(err.Error(), "unsupported Worker WebSocket route") {
		t.Fatalf("non-Gateway route was not rejected before dialing: %v", err)
	}
}
