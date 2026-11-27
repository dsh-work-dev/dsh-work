//go:build darwin && cgo

package macowner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBrokerRegistrationAcceptsOnlyThePinnedExecutable(t *testing.T) {
	directory := t.TempDir()
	executable := filepath.Join(directory, "DSHWorkBrowser")
	if err := os.WriteFile(executable, []byte("fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	device, inode, err := fileIdentity(executable)
	if err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(directory, "code-sign-clone")
	if err := os.Link(executable, alias); err != nil {
		t.Fatal(err)
	}
	copyPath := filepath.Join(directory, "other", filepath.Base(executable))
	if err := os.Mkdir(filepath.Dir(copyPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(copyPath, []byte("fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	started := Process{PID: 40, PPID: 30, Started: 12345, Path: executable}
	g := &guard{state: journal{Owner: Process{PID: 30}, Broker: started}, brokerDevice: device, brokerInode: inode}
	g.cfg.Plan.Executable = executable
	for _, test := range []struct {
		name        string
		path        string
		pid, parent uint32
		started     uint64
		want        string
	}{
		{"launch path", executable, 40, 30, 12345, ""},
		{"kernel hard-link alias", alias, 40, 30, 12345, ""},
		{"byte-identical copy", copyPath, 40, 30, 12345, "owner-register-path-mismatch"},
		{"blank kernel path", "", 40, 30, 12345, "owner-register-path-unavailable"},
		{"different process", alias, 41, 30, 12345, "owner-register-process-mismatch"},
		{"reused process", alias, 40, 30, 54321, "owner-register-process-mismatch"},
		{"different parent", alias, 40, 31, 12345, "owner-register-parent-mismatch"},
	} {
		t.Run(test.name, func(t *testing.T) {
			actual := Process{PID: test.pid, PPID: test.parent, Started: test.started, Path: test.path}
			if got := g.brokerRegistrationFailure(actual); got != test.want {
				t.Fatalf("broker registration check = %q, want %q", got, test.want)
			}
		})
	}
	if err := os.Rename(copyPath, executable); err != nil {
		t.Fatal(err)
	}
	if got := g.brokerRegistrationFailure(started); got != "owner-register-path-mismatch" {
		t.Fatalf("replacement at the original path passed the launch pin: %q", got)
	}
}

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

func TestNativeTransportEvidenceAcceptsOnlyFixedMarkers(t *testing.T) {
	g := &guard{}
	w := &nativeEventWriter{guard: g}
	markers := []string{
		"startup-owner-descriptor-failed", "startup-owner-carrier-failed", "startup-owner-registration-failed",
		"startup-owner-registration-identity-failed", "startup-owner-registration-write-failed", "startup-owner-registration-reply-failed",
		"startup-host-carrier-failed", "startup-host-hmac-failed", "startup-owner-initialize-failed",
		"download-writer-admission-failed",
	}
	for index, name := range markers {
		_, _ = w.Write([]byte("DSH_CEF_EVENT " + name + " private-details\n"))
		_, _ = w.Write([]byte("DSH_CEF_EVENT " + name + "\n"))
		if len(g.state.NativeEvents) != index+1 || g.state.NativeEvents[index].Name != name {
			t.Fatal("fixed transport marker was lost or private details escaped", g.state.NativeEvents)
		}
	}
}

func TestMCPLifecycleEvidenceFiltersStderr(t *testing.T) {
	g := &guard{}
	w := &nativeEventWriter{guard: g}
	for _, part := range []string{"DSH_MCP_EV", "ENT inspect-start\n", "DSH_MCP_EVENT inspect-error private-body\n", "DSH_MCP_EVENT stop-private-close\n", strings.Repeat("x", 128) + "DSH_MCP_EVENT tools-ready\n"} {
		if n, err := w.Write([]byte(part)); err != nil || n != len(part) {
			t.Fatal("stderr drain", n, err)
		}
	}
	if len(g.state.NativeEvents) != 2 || g.state.NativeEvents[0].Name != "mcp-inspect-start" || g.state.NativeEvents[1].Name != "mcp-stop-private-close" {
		t.Fatal("non-fixed stderr escaped", g.state.NativeEvents)
	}
}

func TestMCPFailureOriginsFilterPrivateErrorText(t *testing.T) {
	g := &guard{}
	w := &nativeEventWriter{guard: g}
	for _, stage := range []string{"private-end", "private-finish", "cdp-upgrade", "cdp-connected", "uncaught-exception", "unhandled-rejection", "process-exit-zero", "process-exit-nonzero"} {
		_, _ = w.Write([]byte("DSH_MCP_EVENT " + stage + " private-body\n"))
		_, _ = w.Write([]byte("DSH_MCP_EVENT " + stage + "\n"))
		if got := g.state.NativeEvents[len(g.state.NativeEvents)-1].Name; got != "mcp-"+stage {
			t.Fatal("fixed origin missing", stage, got)
		}
	}
	if len(g.state.NativeEvents) != 8 {
		t.Fatal("private error text escaped", g.state.NativeEvents)
	}
}
