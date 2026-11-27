package embeddedbrowser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/automation/browsernative"
)

func TestPausedPresentationHidesObsoleteGuestAndRetainsFailedOwner(t *testing.T) {
	for _, mode := range []string{"unmount", "resize"} {
		t.Run(mode, func(t *testing.T) {
			host, peer := net.Pipe()
			defer peer.Close()
			defer host.Close()
			b := &Broker{conn: host, generation: "fixture", done: make(chan struct{}), closed: make(chan struct{}), routes: map[string]*route{}, controls: map[string]chan controlResult{}}
			b.invalidate(errors.New("CEF_RENDERER_SCOPE_UNCERTAIN"))
			go b.receive()
			target := &OwnedTarget{broker: b, owner: "owned"}
			owner := &drainOwner{done: make(chan struct{})}
			originalBounds := PixelBounds{X: 1, Y: 80, Width: 300, Height: 200}
			p := &presentation{target: target, owner: owner, view: ViewState{State: "ready", Visible: true, Lease: "same-lease", Document: "same-document", Bounds: originalBounds}}
			r := NewPresenterRegistry(b)
			requested := originalBounds
			visible := false
			if mode == "resize" {
				requested.Width = 200
				visible = true
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			for attempt := 0; attempt < 2; attempt++ {
				result := make(chan error, 1)
				go func() { _, e := r.pausePresentationLocked(ctx, p, requested, visible); result <- e }()
				var command struct {
					Kind, Generation, Route, Request string
					Arguments                        map[string]any
				}
				if e := browsernative.ReadWireMessage(peer, &command); e != nil {
					t.Fatal(e)
				}
				if command.Kind != "target.hide" || command.Route != "owned" {
					t.Fatal("paused geometry issued control", command)
				}
				// No hide ACK yet: old visible state and owner remain.
				if !p.view.Visible || p.target != target || p.owner != owner {
					t.Fatal("unacknowledged hide claimed success")
				}
				response := map[string]any{"kind": "target.result", "generation": "fixture", "request": command.Request, "result": map[string]any{}}
				if attempt == 0 {
					response["error"] = "CEF_HIDE_INJECTED_FAILURE"
				}
				if e := browsernative.WriteWireMessage(peer, response); e != nil {
					t.Fatal(e)
				}
				e := <-result
				if attempt == 0 {
					if e == nil || !p.view.Visible {
						t.Fatal("failed hide lost actual visibility", e)
					}
				} else if e != nil || p.view.Visible {
					t.Fatal("retry failed", e, p.view)
				}
			}
			if p.view.State != "controlPaused" || p.view.Document != "same-document" || p.view.Lease != "same-lease" || p.view.Bounds != originalBounds || p.target != target || owner.closes.Load() != 0 {
				raw, _ := json.Marshal(p.view)
				t.Fatal("paused hide retired document or fabricated bounds", string(raw))
			}
		})
	}
}

func TestPresenterFrameEvidenceUsesTrustedReadyIdentityAndClearsOnLoad(t *testing.T) {
	host, peer := net.Pipe()
	defer host.Close()
	defer peer.Close()
	b := &Broker{conn: host, generation: "fixture", done: make(chan struct{}), closed: make(chan struct{}), routes: map[string]*route{}, controls: map[string]chan controlResult{}}
	go b.receive()
	target := &OwnedTarget{broker: b, owner: "owned", nativeClosed: make(chan struct{})}
	p := &presentation{target: target, view: ViewState{Failure: "CEF_TARGET_NOT_READY"}}
	r := NewPresenterRegistry(b)
	served := make(chan error, 1)
	go func() {
		for i, kind := range []string{"target.navigation", "target.state", "target.navigation"} {
			var command struct{ Kind, Request string }
			if err := browsernative.ReadWireMessage(peer, &command); err != nil {
				served <- err
				return
			}
			if command.Kind != kind {
				served <- fmt.Errorf("unexpected command %s", command.Kind)
				return
			}
			var result any = map[string]any{"url": "http://fixture.test/", "loading": i == 2}
			if kind == "target.state" {
				result = map[string]any{"brokerGeneration": "fixture", "target": "1", "frame": "5-TRUSTED-CEF-FRAME", "document": "2", "url": "http://fixture.test/", "viewVisible": true, "scopeEpoch": "1", "frameScopeReady": true}
			}
			if err := browsernative.WriteWireMessage(peer, map[string]any{"kind": "target.result", "generation": "fixture", "request": command.Request, "result": result}); err != nil {
				served <- err
				return
			}
		}
		served <- nil
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := r.refreshLocked(ctx, p); err != nil || p.view.State != "ready" || p.view.Failure != "" || p.view.Frame != "5-TRUSTED-CEF-FRAME" {
		t.Fatal("trusted frame not projected", p.view, err)
	}
	if err := r.refreshLocked(ctx, p); err != nil || p.view.State != "loading" || p.view.Frame != "" {
		t.Fatal("loading view retained old frame", p.view, err)
	}
	if err := <-served; err != nil {
		t.Fatal(err)
	}
}

func TestPresenterUpdateKeepsNativeFailureAfterAcknowledgedGeometry(t *testing.T) {
	for _, test := range []struct {
		name           string
		navigation     map[string]any
		state, failure string
		visible        bool
	}{
		{
			name:       "native dialog restore failed",
			navigation: map[string]any{"result": map[string]string{"presentationFailure": "CEF_DIALOG_RESTORE_FAILED"}},
			state:      "presentationFailed", failure: "CEF_DIALOG_RESTORE_FAILED",
		},
		{
			name:       "navigation still unavailable",
			navigation: map[string]any{"error": "CEF_TARGET_NOT_READY"},
			state:      "loading", failure: "CEF_TARGET_NOT_READY", visible: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			host, peer := net.Pipe()
			defer host.Close()
			defer peer.Close()
			if err := peer.SetDeadline(time.Now().Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			b := &Broker{conn: host, generation: "fixture", done: make(chan struct{}), closed: make(chan struct{})}
			go b.receive()
			r := NewPresenterRegistry(b)
			p := &presentation{
				epoch: 2, target: &OwnedTarget{broker: b, owner: "owned"},
				view: ViewState{Lease: "lease", State: "ready", URL: "https://fixture.test/", Frame: "old-frame",
					Visible: true, Epoch: "1", Popup: &WebPopup{}, Dialog: &WebDialog{}, PopupVisible: true, DialogVisible: true},
			}
			served := make(chan error, 1)
			go func() {
				for _, kind := range []string{"target.present", "target.navigation"} {
					var command struct {
						Kind, Route, Request string
						Arguments            struct {
							Epoch   string
							Bounds  PixelBounds
							Visible bool
						}
					}
					if err := browsernative.ReadWireMessage(peer, &command); err != nil {
						served <- err
						return
					}
					if command.Kind != kind || command.Route != "owned" {
						served <- fmt.Errorf("unexpected command %s for %s", command.Kind, command.Route)
						return
					}
					if kind == "target.present" && (!command.Arguments.Visible || command.Arguments.Epoch != "2" || command.Arguments.Bounds != (PixelBounds{Width: 400, Height: 300})) {
						served <- fmt.Errorf("invalid presentation arguments: %+v", command.Arguments)
						return
					}
					response := map[string]any{"kind": "target.result", "generation": "fixture", "request": command.Request}
					if kind == "target.navigation" {
						for key, value := range test.navigation {
							response[key] = value
						}
					}
					if err := browsernative.WriteWireMessage(peer, response); err != nil {
						served <- err
						return
					}
				}
				served <- nil
			}()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			bounds := PixelBounds{Width: 400, Height: 300}
			view, err := r.presentLocked(ctx, p, bounds, true, map[string]any{})
			if wireErr := <-served; wireErr != nil {
				t.Fatal(wireErr)
			}
			if err != nil || view.State != test.state || view.Failure != test.failure || view.Visible != test.visible ||
				view.Epoch != "2" || view.Bounds != bounds || view.URL != "https://fixture.test/" || view.Lease != "lease" || view.Frame != "" {
				t.Fatalf("wrong state after native presentation acknowledgement: %+v (%v)", view, err)
			}
			if view.Popup != nil || view.Dialog != nil || view.PopupVisible || view.DialogVisible {
				t.Fatal("failed presentation retained an interactive overlay")
			}
		})
	}
}
