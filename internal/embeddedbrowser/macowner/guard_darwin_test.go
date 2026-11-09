//go:build darwin && cgo

package macowner

import (
	"strings"
	"testing"
)

func TestNativeLifecycleEvidenceFiltersStderr(t *testing.T) {
	g := &guard{}
	w := &nativeEventWriter{guard: g}
	for _, part := range []string{"private page or error text\nDSH_CEF_EV", "ENT shutdown-enter\n", strings.Repeat("x", 128) + "DSH_CEF_EVENT before-close\n", "DSH_CEF_EVENT shutdown-returned\n"} {
		if n, err := w.Write([]byte(part)); err != nil || n != len(part) {
			t.Fatal("stderr drain", n, err)
		}
	}
	if len(g.state.NativeEvents) != 2 || g.state.NativeEvents[0].Name != "shutdown-enter" || g.state.NativeEvents[1].Name != "shutdown-returned" {
		t.Fatal("fixed events not isolated", g.state.NativeEvents)
	}
	for i := 0; i < 70; i++ {
		_, _ = w.Write([]byte("DSH_CEF_EVENT before-close\n"))
	}
	if len(g.state.NativeEvents) != 64 || !g.state.NativeEventsTruncated {
		t.Fatal("unbounded native diagnostic events")
	}
}
