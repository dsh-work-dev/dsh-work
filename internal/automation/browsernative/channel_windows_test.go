//go:build windows

package browsernative

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/daemon"
	"github.com/local/dsh-work/internal/settings"
)

// Uses the real SID ACL daemon pipe, admission and mutual HMAC; no browser or
// registration. The distinct real-browser gate is not replaced by this test.
func TestSetupOverAuthenticatedDaemonPipe(t *testing.T) {
	t.Run("EOF", func(t *testing.T) { checkSetupPipe(t, false) })
	t.Run("oversize reported before disconnect", func(t *testing.T) { checkSetupPipe(t, true) })
}
func checkSetupPipe(t *testing.T, oversized bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	root := t.TempDir()
	m, e := settings.New(settings.Config{Path: filepath.Join(root, "settings.json")})
	if e != nil {
		t.Fatal(e)
	}
	s := automation.New(m, nil)
	defer s.Close()
	reported := make(chan string, 1)
	h := &Handler{Service: s, Browser: "chrome", Context: ctx, Observe: func(kind string, fields map[string]any) {
		if kind == "native-error" {
			reported <- fields["error"].(string)
		}
	}}
	l, e := daemon.Listen("binding-test:" + root)
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	server := &http.Server{Handler: h}
	go server.Serve(l)
	defer server.Close()
	client := daemon.NewClient("binding-test:" + root)
	defer client.Close()
	var denied admission
	if e := client.JSON(ctx, BasePath+"/admit", map[string]any{"origin": "chrome-extension://aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/", "protocol": 1}, &denied); e == nil {
		t.Fatal("forbidden origin accepted")
	}
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	defer inR.Close()
	defer inW.Close()
	defer outR.Close()
	defer outW.Close()
	done := make(chan error, 1)
	go func() { defer outW.Close(); done <- Run(ctx, client, Origin, inR, outW) }()
	version, wire := "0.1.0", 0
	if oversized {
		version, wire = "0.2.0", 2
	}
	if e := WriteMessage(inW, map[string]any{"kind": "hello", "source": "test-loaded-source", "protocol": 1, "wireProtocol": wire, "extensionVersion": version, "extensionId": automation.DevelopmentExtensionID, "instance": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}); e != nil {
		t.Fatal(e)
	}
	var reply map[string]any
	if e := ReadMessage(outR, &reply); e != nil || reply["kind"] != "welcome" {
		t.Fatal(reply, e)
	}
	if e := WriteMessage(inW, map[string]any{"id": "bind-1", "kind": "bind", "name": "A"}); e != nil {
		t.Fatal(e)
	}
	if e := ReadMessage(outR, &reply); e != nil || reply["candidate"] == nil {
		t.Fatal(reply, e)
	}
	id, e := s.ConfirmBrowserBinding(ctx, reply["candidate"].(string))
	if e != nil {
		t.Fatal(e)
	}
	if e := s.SelectBrowserBinding(ctx, id); e != nil {
		t.Fatal(e)
	}
	if got := s.Snapshot().Profiles.Bindings[0].LoadedSource; got != "test-loaded-source" {
		t.Fatalf("native hello lost loaded source: %q", got)
	}
	lease, e := s.SelectedBrowserConnection()
	if e != nil {
		t.Fatal(e)
	}
	if e := WriteMessage(inW, map[string]any{"id": "forbidden", "kind": "browser_navigate", "url": "http://localhost/"}); e != nil {
		t.Fatal(e)
	}
	if e := ReadMessage(outR, &reply); e != nil || reply["error"] != "BROWSER_SETUP_OPERATION_REJECTED" {
		t.Fatal(reply, e)
	}
	if oversized {
		// The original native JSON is 2 MiB, not the 12 MiB produced by Go's
		// optional HTML escaping. An unknown route is ignored without actions.
		raw := []byte(`{"kind":"wire","route":"s-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","payload":{"text":"` + strings.Repeat("<", 2<<20) + `"}}`)
		if err := writeFrameLimit(inW, raw, MaxFromExtensionBytes); err != nil {
			t.Fatal(err)
		}
		if err := WriteMessage(inW, map[string]any{"id": "after-html-frame", "kind": "status"}); err != nil {
			t.Fatal(err)
		}
		if err := ReadMessage(outR, &reply); err != nil || reply["id"] != "after-html-frame" {
			t.Fatal("valid native bytes were expanded/rejected during relay", reply, err)
		}
	}
	if err := writeMessageLimit(inW, map[string]any{"kind": "status", "id": "oversized-setup", "padding": strings.Repeat("中", MaxSetupBytes/3)}, MaxFromExtensionBytes); err != nil {
		t.Fatal(err)
	}
	if err := ReadMessage(outR, &reply); err != nil || reply["error"] != "BROWSER_NATIVE_MESSAGE_TOO_LARGE" {
		t.Fatal("setup control limit lost after hello", reply, err)
	}
	if oversized {
		if err := binary.Write(inW, binary.LittleEndian, uint32(MaxFromExtensionBytes+1)); err != nil {
			t.Fatal(err)
		}
	} else {
		inW.Close()
	}
	select {
	case err := <-done:
		if oversized && !errors.Is(err, errNativeMessageTooLarge) {
			t.Fatal("native size error lost", err)
		}
	case <-ctx.Done():
		t.Fatal("native pump did not end")
	}
	if oversized {
		select {
		case cause := <-reported:
			if cause != "BROWSER_NATIVE_MESSAGE_TOO_LARGE" {
				t.Fatal(cause)
			}
		case <-ctx.Done():
			t.Fatal("daemon did not receive explicit native size error")
		}
	}
	for s.ValidateBrowserLease(lease) == nil {
		select {
		case <-ctx.Done():
			t.Fatal("disconnected route remained valid")
		case <-time.After(5 * time.Millisecond):
		}
	}
	if _, e := s.SelectedBrowserConnection(); !errors.Is(e, automation.ErrBrowserOffline) {
		t.Fatal(e)
	}
}
