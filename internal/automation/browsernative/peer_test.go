package browsernative

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/settings"
)

func TestNativeRoutesStayOnSelectedInstance(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	m, err := settings.New(settings.Config{Path: filepath.Join(t.TempDir(), "settings.json")})
	if err != nil {
		t.Fatal(err)
	}
	s := automation.New(m, nil)
	defer s.Close()
	id, err := s.BrowserConnected("chrome", automation.BrowserHello{Protocol: 1, WireProtocol: 2, ExtensionVersion: "0.2.0", ExtensionID: automation.DevelopmentExtensionID, Instance: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"})
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := s.RequestBrowserBinding(id, "A")
	if err != nil {
		t.Fatal(err)
	}
	binding, err := s.ConfirmBrowserBinding(ctx, candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SelectBrowserBinding(ctx, binding); err != nil {
		t.Fatal(err)
	}
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	p := &nativePeer{service: s, id: id, conn: a, routes: map[string]*nativeRoute{}}
	defer p.disconnect(errRouteClosed)
	if err = s.AttachBrowserPeer(id, p); err != nil {
		t.Fatal(err)
	}
	frames := make(chan map[string]json.RawMessage, 16)
	go func() {
		for {
			var v map[string]json.RawMessage
			if readMessageLimit(b, &v, MaxToExtensionBytes) != nil {
				return
			}
			frames <- v
		}
	}()
	lease, err := s.SelectedBrowserConnection()
	if err != nil {
		t.Fatal(err)
	}
	route, err := s.OpenBrowserRoute(ctx, lease, "会话 A")
	if err != nil {
		t.Fatal(err)
	}
	var routeID string
	select {
	case frame := <-frames:
		json.Unmarshal(frame["route"], &routeID)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	other, err := s.OpenBrowserRoute(ctx, lease, "会话 B")
	if err != nil {
		t.Fatal(err)
	}
	<-frames
	p.receive("wire", "s-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", json.RawMessage(`{"id":9}`), "")
	select {
	case <-route.Messages():
		t.Fatal("foreign route delivered")
	default:
	}
	p.receive("wire", routeID, json.RawMessage(`{"id":1,"result":{}}`), "")
	select {
	case <-route.Messages():
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := route.Close(); err != nil {
		t.Fatal(err)
	}
	<-frames
	select {
	case <-other.Done():
		t.Fatal("closing A released B")
	default:
	}
	if err := s.SelectBrowserBinding(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if err := other.Send(ctx, json.RawMessage(`{"id":2}`)); err == nil {
		t.Fatal("old lease sent after deselection")
	}
	if _, err := s.OpenBrowserRoute(ctx, lease, "late"); err == nil {
		t.Fatal("old lease reopened route")
	}
	select {
	case <-other.Done():
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}
