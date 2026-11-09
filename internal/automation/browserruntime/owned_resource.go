package browserruntime

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/local/dsh-work/internal/automation"
)

// An observer/MCP process is disposable; the broker-owned target is not. A
// document transition retires the complete old executor before any new permit
// can install a new executor. No old SDK task survives into a fresh guard.
type ownedResource struct {
	mu               sync.Mutex
	ctx              context.Context
	cancel           context.CancelFunc
	current          automation.Resource
	reopen           automation.Factory
	target           automation.OwnedBrowserTarget
	identity         automation.OwnedBrowserIdentity
	executorIdentity automation.OwnedBrowserIdentity
	closed           bool
	closeMu          sync.Mutex
	revokeOnce       sync.Once
	closeErr         error
	failure          error
}

func (r *ownedResource) OwnedBrowserBinding() automation.OwnedBrowserIdentity { return r.identity }
func (r *ownedResource) retireLocked() error {
	if r.current == nil {
		return nil
	}
	err := r.current.Close()
	if err != nil {
		r.failure = errors.Join(r.failure, err)
		return err
	}
	r.current = nil
	r.executorIdentity = automation.OwnedBrowserIdentity{}
	return nil
}
func (r *ownedResource) Execute(ctx context.Context, tool string, args map[string]any) (any, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.failure != nil {
		return nil, errors.Join(automation.ErrRevoked, r.failure)
	}
	call, ok := automation.OwnedBrowserCallFromContext(ctx)
	if !ok {
		return nil, automation.ErrRevoked
	}
	// Autonomous navigation or scope changes between calls invalidate the old SDK
	// even though no preceding Execute observed that transition.
	if r.current != nil && r.executorIdentity != call.Identity {
		if e := r.retireLocked(); e != nil {
			return nil, e
		}
	}
	if r.current == nil {
		var err error
		r.current, err = r.reopen(r.ctx, call.Owner)
		if err != nil {
			r.failure = err
			return nil, err
		}
		r.executorIdentity = r.current.(interface {
			OwnedBrowserBinding() automation.OwnedBrowserIdentity
		}).OwnedBrowserBinding()
	}
	value, callErr := r.current.Execute(ctx, tool, args)
	// Caller cancellation is not a cleanup context. Obtain independent engine
	// evidence on errors too; ambiguous observation always retires the executor.
	check, stop := context.WithTimeout(context.Background(), 3*time.Second)
	actual, stateErr := r.target.OwnedIdentity(check)
	stop()
	if stateErr != nil {
		return nil, errors.Join(callErr, stateErr, r.retireLocked())
	}
	scopeChanged := actual.ViewLease != call.Identity.ViewLease || actual.PresentationEpoch != call.Identity.PresentationEpoch || actual.ViewLease != "" && !actual.ViewVisible || !actual.FrameScopeReady || actual.ScopeEpoch != call.Identity.ScopeEpoch || actual.BrokerGeneration != call.Identity.BrokerGeneration || actual.Target != call.Identity.Target || actual.ArtifactLease != call.Identity.ArtifactLease || actual.ArtifactOrigin != call.Identity.ArtifactOrigin
	if scopeChanged {
		return nil, errors.Join(errors.New("BROWSER_FRAME_SCOPE_CHANGED"), callErr, r.retireLocked())
	}
	if callErr != nil {
		return nil, errors.Join(callErr, r.retireLocked())
	}
	if actual.Document != call.Identity.Document {
		if e := r.retireLocked(); e != nil {
			return nil, e
		}
		return automation.OwnedBrowserNavigation{Identity: actual, Result: value}, nil
	}
	if actual != call.Identity {
		return nil, errors.Join(errors.New("BROWSER_TARGET_IDENTITY_CHANGED"), r.retireLocked())
	}
	return value, nil
}
func (r *ownedResource) Close() error {
	r.closeMu.Lock()
	defer r.closeMu.Unlock()
	r.revokeOnce.Do(func() {
		r.cancel()
		ctx, stop := context.WithTimeout(context.Background(), 3*time.Second)
		r.closeErr = errors.Join(r.closeErr, r.target.RevokeOwned(ctx))
		stop()
	})
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	if r.current != nil {
		err := r.current.Close()
		r.closeErr = errors.Join(r.closeErr, err)
		pending := err != nil
		if owner, ok := r.current.(interface{ CleanupPending() bool }); ok {
			pending = owner.CleanupPending()
		}
		if !pending {
			r.current = nil
			r.executorIdentity = automation.OwnedBrowserIdentity{}
		}
	}
	return errors.Join(r.closeErr, r.failure)
}
func (r *ownedResource) AutomationTerminal() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.failure != nil {
		return true
	}
	if terminal, ok := r.current.(interface{ AutomationTerminal() bool }); ok {
		return terminal.AutomationTerminal()
	}
	return false
}

func (r *ownedResource) CleanupPending() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.current != nil
}
