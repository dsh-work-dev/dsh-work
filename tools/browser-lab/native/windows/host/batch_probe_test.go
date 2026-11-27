package main

import "testing"

// The previous per-poll NewCallback exhausted Go's fixed callback table.
func TestGuestWindowProbeReusesCallback(t *testing.T) {
	for i := 0; i < 10000; i++ {
		if rows := GuestTopLevelWindows(^uint32(0)); len(rows) != 0 {
			t.Fatal("impossible PID matched")
		}
	}
}
