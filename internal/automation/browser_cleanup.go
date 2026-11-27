package automation

import (
	"errors"
	"sync"
)

// A stopped scope is never executable again. Only its failed owned cleanup is
// retained, independently of the live entries and of other UI conversations.
type browserCleanupOwner struct {
	mu                sync.Mutex
	entry             *entry
	session, lifetime string
}

func (s *Service) finishBrowserCleanup(owner *browserCleanupOwner, retry bool) error {
	owner.mu.Lock()
	defer owner.mu.Unlock()
	e := owner.entry
	<-e.closed
	bound, owned := e.resource.(interface{ OwnedBrowserBinding() OwnedBrowserIdentity })
	owned = owned && bound.OwnedBrowserBinding().BrokerGeneration != ""
	var result error
	pending := false
	if owned {
		result = e.closeErr
		if retry {
			result = e.resource.Close()
		}
		pending = result != nil
		if state, ok := e.resource.(interface{ CleanupPending() bool }); ok {
			pending = state.CleanupPending()
		}
		if retry && !pending {
			// The initial failure was already returned/logged. Completion is
			// usable even if the resource preserves that historical error.
			result = nil
		}
		if pending && result == nil {
			result = errors.New("BROWSER_OWNED_CLEANUP_PENDING")
		}
	}
	if !pending {
		s.mu.Lock()
		if s.browserCleanup[e] == owner {
			delete(s.browserCleanup, e)
		}
		s.mu.Unlock()
	}
	return result
}
