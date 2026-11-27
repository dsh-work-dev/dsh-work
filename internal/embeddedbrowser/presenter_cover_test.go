package embeddedbrowser

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/automation/browsernative"
)

func TestPresenterLayoutChangesOnNewDisplayRevision(t *testing.T) {
	bounds := PixelBounds{X: 20, Y: 70, Width: 500, Height: 400}
	p := &presentation{view: ViewState{Bounds: bounds, Visible: true, State: "ready"}}
	if !p.matchesLayout(bounds, true, 0) {
		t.Fatal("same display and bounds should reuse the acknowledged layout")
	}
	if p.matchesLayout(bounds, true, 1) {
		t.Fatal("a new display revision must submit the same bounds to native again")
	}
	p.displayRevision = 1
	p.view.State = "presentationFailed"
	if p.matchesLayout(bounds, true, 1) {
		t.Fatal("a failed native layout cannot be reused")
	}
}

func TestCoverPublishesPreviewOnlyAfterNativeHide(t *testing.T) {
	host, peer := net.Pipe()
	t.Cleanup(func() { host.Close(); peer.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := peer.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	b := &Broker{conn: host, generation: "fixture", done: make(chan struct{}), closed: make(chan struct{}), controls: map[string]chan controlResult{}}
	go b.receive()
	r := NewPresenterRegistry(b)
	owner := PresenterPeer{PID: 42, Started: 1}
	p := &presentation{peer: owner, surface: "workbench", target: &OwnedTarget{broker: b, owner: "fixture-target"}, epoch: 5,
		view: ViewState{Visible: true, State: "ready", Epoch: "5", Lease: "same-view", Document: "same-document", Frame: "same-frame"}}
	r.views[presentationKey("workbench", "slot")] = p
	respond := func(kind string, result map[string]any, failure string) map[string]any {
		t.Helper()
		var request struct {
			Kind, Request, Route string
			Arguments            map[string]any
		}
		if err := browsernative.ReadWireMessage(peer, &request); err != nil {
			t.Fatal(err)
		}
		if request.Kind != kind || request.Route != "fixture-target" {
			t.Fatalf("unexpected command: %+v", request)
		}
		response := map[string]any{"kind": "target.result", "generation": "fixture", "request": request.Request, "result": result}
		if failure != "" {
			response["error"] = failure
		}
		if err := browsernative.WriteWireMessage(peer, response); err != nil {
			t.Fatal(err)
		}
		return request.Arguments
	}
	for _, attempt := range []struct{ epoch, failure string }{{"6", "CEF_FIXTURE_HIDE_FAILED"}, {"7", ""}} {
		type result struct {
			previews []Preview
			err      error
		}
		out := make(chan result, 1)
		go func() {
			previews, err := r.Cover(ctx, owner, "workbench")
			out <- result{previews, err}
		}()
		shot := respond("target.preview", map[string]any{"data": "fixture-pixels"}, "")
		if shot["view"] != "same-view" || shot["presentation"] != "5" || shot["document"] != "same-document" || shot["frame"] != "same-frame" {
			t.Fatalf("capture lost visible frame identity: %+v", shot)
		}
		if hide := respond("target.hide", nil, attempt.failure); hide["epoch"] != attempt.epoch {
			t.Fatalf("unexpected hide epoch: %+v", hide)
		}
		got := <-out
		if attempt.failure != "" {
			if got.err == nil || len(got.previews) != 0 || !p.view.Visible || p.view.Epoch != "5" {
				t.Fatalf("failed hide published a new state: %+v %+v", got, p.view)
			}
			continue
		}
		if got.err != nil || len(got.previews) != 1 || p.view.Visible || p.view.Epoch != "7" {
			t.Fatalf("acknowledged hide did not publish consistent state: %+v %+v", got, p.view)
		}
		if preview := got.previews[0]; preview.Slot != "slot" || preview.Lease != "same-view" || preview.Document != "same-document" || preview.Frame != "same-frame" {
			t.Fatalf("preview lost hidden view identity: %+v", preview)
		}
	}
}
