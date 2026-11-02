package browsernative

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"strings"
	"testing"
)

func TestSetupFrameUsesCompleteUTF8MessageSize(t *testing.T) {
	var out bytes.Buffer
	// The quotes are part of the native message, not just the string value.
	if e := WriteMessage(&out, strings.Repeat("a", MaxSetupBytes-2)); e != nil {
		t.Fatal(e)
	}
	if n := binary.LittleEndian.Uint32(out.Bytes()[:4]); n != MaxSetupBytes {
		t.Fatal(n)
	}
	var decoded string
	if e := ReadMessage(&out, &decoded); e != nil || len(decoded) != MaxSetupBytes-2 {
		t.Fatal(e)
	}
	for _, value := range []any{strings.Repeat("a", MaxSetupBytes-1), map[string]any{"kind": "bind", "name": strings.Repeat("中", MaxSetupBytes/3)}} {
		out.Reset()
		if e := WriteMessage(&out, value); e == nil || out.Len() != 0 {
			t.Fatal("oversize emitted stdout bytes")
		}
	}
	head := make([]byte, 4)
	binary.LittleEndian.PutUint32(head, MaxSetupBytes+1)
	var raw json.RawMessage
	if e := ReadMessage(bytes.NewReader(head), &raw); e == nil {
		t.Fatal("oversize header allocated/read body")
	}
	out.Reset()
	if e := Run(context.Background(), nil, "chrome-extension://aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/", bytes.NewReader(nil), &out); e == nil || out.Len() != 0 {
		t.Fatal("foreign source reached transport")
	}
}

func TestWireLimitsCountCompleteUTF8Envelope(t *testing.T) {
	for _, limit := range []int{MaxToExtensionBytes, MaxFromExtensionBytes} {
		v := map[string]any{"kind": "wire", "route": "s-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "payload": map[string]any{"text": ""}}
		base, _ := json.Marshal(v)
		n := limit - len(base)
		payload := v["payload"].(map[string]any)
		payload["text"] = strings.Repeat("中", n/3) + strings.Repeat("a", n%3)
		var out bytes.Buffer
		if err := writeMessageLimit(&out, v, limit); err != nil {
			t.Fatal(err)
		}
		if got := binary.LittleEndian.Uint32(out.Bytes()[:4]); got != uint32(limit) {
			t.Fatal(got, limit)
		}
		var raw json.RawMessage
		if err := readMessageLimit(&out, &raw, uint32(limit)); err != nil {
			t.Fatal(err)
		}
		payload["text"] = payload["text"].(string) + "中"
		out.Reset()
		if err := writeMessageLimit(&out, v, limit); err == nil || out.Len() != 0 {
			t.Fatal("oversize native envelope emitted bytes")
		}
		header := make([]byte, 4)
		binary.LittleEndian.PutUint32(header, uint32(limit+1))
		if err := readMessageLimit(bytes.NewReader(header), &raw, uint32(limit)); err == nil {
			t.Fatal("oversize header read body")
		}
	}
}
