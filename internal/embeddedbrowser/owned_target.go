package embeddedbrowser

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/local/dsh-work/internal/automation"
)

type controlResult struct {
	Request, Error string
	Result         json.RawMessage
}

func (b *Broker) controlCall(ctx context.Context, kind, route string, args any) (json.RawMessage, error) {
	ctx, stop := context.WithTimeout(ctx, 5*time.Second)
	defer stop()
	id := rand.Text()
	ch := make(chan controlResult, 1)
	b.mu.Lock()
	if b.controls == nil {
		b.controls = map[string]chan controlResult{}
	}
	b.controls[id] = ch
	b.mu.Unlock()
	defer func() { b.mu.Lock(); delete(b.controls, id); b.mu.Unlock() }()
	if err := b.write(map[string]any{"kind": kind, "route": route, "request": id, "arguments": args}); err != nil {
		return nil, err
	}
	if b.observe != nil {
		b.observe("control", kind+"-dispatched")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case result := <-ch:
		if result.Error != "" {
			return nil, errors.New(result.Error)
		}
		return result.Result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-b.closed:
		return nil, ErrClosed
	}
}

// OwnedTarget is a trusted Go registry object, not a URL/provider selector that
// can be constructed by a tool. Its route is claimed once by Service's resource.
type OwnedTarget struct {
	broker           *Broker
	route            *route
	artifact         *Artifact
	mu, call         sync.Mutex
	claimed          bool
	owner            string
	nativeClosed     chan struct{}
	nativeClosedOnce sync.Once
}

// OpenOwnedWebTarget retains the explicit non-presenter engine path used by
// private transport and Service qualification. Product registry uses managed.
func (b *Broker) OpenOwnedWebTarget(ctx context.Context, address string) (*OwnedTarget, error) {
	return b.openOwnedWebTarget(ctx, address, false)
}
func (b *Broker) openManagedWebTarget(ctx context.Context, address string) (*OwnedTarget, error) {
	return b.openOwnedWebTarget(ctx, address, true)
}
func (b *Broker) openOwnedWebTarget(ctx context.Context, address string, managed bool) (*OwnedTarget, error) {
	if _, err := automation.BrowserOrigin(address); err != nil {
		return nil, err
	}
	r, err := b.openURL(ctx, address, managed)
	if err != nil {
		return nil, err
	}
	target := &OwnedTarget{broker: b, route: r.(*route), owner: r.(*route).id}
	target.register()
	return target, nil
}
func (a *Artifact) OwnedTarget() *OwnedTarget {
	target := &OwnedTarget{broker: a.broker, route: a.route, artifact: a, owner: a.route.id}
	target.register()
	return target
}
func (t *OwnedTarget) OwnedIdentity(ctx context.Context) (automation.OwnedBrowserIdentity, error) {
	ctx, stop := context.WithTimeout(ctx, 10*time.Second)
	defer stop()
	for {
		select {
		case <-t.nativeClosed:
			return automation.OwnedBrowserIdentity{}, ErrClosed
		default:
		}
		if t.artifact != nil {
			t.artifact.mu.Lock()
			revoked := t.artifact.revoked
			t.artifact.mu.Unlock()
			if revoked {
				return automation.OwnedBrowserIdentity{}, ErrArtifactRevoked
			}
		}
		raw, err := t.broker.controlCall(ctx, "target.state", t.owner, nil)
		if err == nil {
			var state automation.OwnedBrowserIdentity
			if json.Unmarshal(raw, &state) != nil {
				return state, errors.New("CEF_TARGET_PROTOCOL_REJECTED")
			}
			if state.BrokerGeneration != t.broker.generation {
				return state, errors.New("CEF_GENERATION_REJECTED")
			}
			if t.artifact != nil && state.ArtifactLease != t.artifact.id {
				return state, ErrArtifactRevoked
			}
			return state, nil
		}
		if !strings.HasPrefix(err.Error(), "CEF_TARGET_NOT_READY") {
			return automation.OwnedBrowserIdentity{}, err
		}
		select {
		case <-ctx.Done():
			return automation.OwnedBrowserIdentity{}, errors.Join(ctx.Err(), err)
		case <-time.After(10 * time.Millisecond):
		}
	}
}
func (t *OwnedTarget) OpenOwnedRoute(ctx context.Context, _ string) (automation.BrowserRoute, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if t.claimed {
		select {
		case <-t.route.done:
		default:
			return nil, automation.ErrRevoked
		}
		r := &route{broker: t.broker, id: rand.Text(), messages: make(chan json.RawMessage, 256), done: make(chan struct{})}
		t.broker.mu.Lock()
		t.broker.routes[r.id] = r
		t.broker.mu.Unlock()
		if _, err := t.broker.controlCall(ctx, "target.attach", t.owner, map[string]any{"route": r.id}); err != nil {
			r.Close()
			return nil, err
		}
		t.route = r
		return r, nil
	}
	if t.artifact != nil {
		t.artifact.mu.Lock()
		defer t.artifact.mu.Unlock()
		if t.artifact.revoked || t.artifact.claimed {
			return nil, ErrArtifactRevoked
		}
		t.artifact.claimed = true
	}
	t.claimed = true
	return t.route, nil
}
func (t *OwnedTarget) BeginOwnedCall(ctx context.Context, call automation.OwnedBrowserCall) (func() error, error) {
	t.call.Lock()
	if _, err := t.broker.controlCall(ctx, "target.begin", t.owner, call); err != nil {
		cleanup := t.confirmGuardRevoked()
		t.call.Unlock()
		return nil, errors.Join(err, cleanup)
	}
	return func() error {
		defer t.call.Unlock()
		ctx, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		_, err := t.broker.controlCall(ctx, "target.end", t.owner, map[string]any{"token": call.Token})
		if err != nil {
			return errors.Join(err, t.confirmGuardRevoked())
		}
		return nil
	}, nil
}
func (t *OwnedTarget) RevokeOwned(ctx context.Context) error {
	_, err := t.broker.controlCall(ctx, "target.revoke", t.owner, nil)
	return err
}

// A canceled request cannot prove target.begin did not take effect. Keep the
// call owner until an independent revoke ACK, or invalidate all transport and
// let the broker's existing bounded terminal cleanup owner finish shutdown.
func (t *OwnedTarget) confirmGuardRevoked() error {
	ctx, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	if err := t.RevokeOwned(ctx); err != nil {
		failure := errors.Join(errors.New("CEF_GUARD_CLEANUP_UNCONFIRMED"), err)
		t.broker.abort(failure)
		return failure
	}
	return nil
}
func (t *OwnedTarget) register() {
	t.nativeClosed = make(chan struct{})
	t.broker.mu.Lock()
	defer t.broker.mu.Unlock()
	if t.broker.targets == nil {
		t.broker.targets = map[string][]*OwnedTarget{}
	}
	t.broker.targets[t.owner] = append(t.broker.targets[t.owner], t)
}
func (t *OwnedTarget) CloseNative(ctx context.Context) error {
	select {
	case <-t.nativeClosed:
		return nil
	default:
	}
	_, err := t.broker.controlCall(ctx, "target.close", t.owner, nil)
	if err != nil {
		select {
		case <-t.nativeClosed:
			return nil
		default:
		}
		return fmt.Errorf("CEF_NATIVE_CLOSE_DISPATCH: %w", err)
	}
	select {
	case <-t.nativeClosed:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("CEF_NATIVE_CLOSE_BARRIER: %w", ctx.Err())
	case <-t.broker.closed:
		return fmt.Errorf("CEF_NATIVE_CLOSE_BARRIER: %w", t.broker.failure())
	}
}
func (t *OwnedTarget) Navigate(ctx context.Context, address string) error {
	if _, err := automation.BrowserOrigin(address); err != nil {
		return err
	}
	_, err := t.broker.controlCall(ctx, "target.navigate", t.owner, map[string]any{"url": address})
	return err
}
