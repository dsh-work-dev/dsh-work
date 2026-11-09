package daemon

import (
	"context"
	"github.com/local/dsh-work/internal/hostevents"
	"net/http"
	"sync"
)

// UIStreamAdmission is constructed by the daemon's native-window registry.
// Nothing in the HTTP request supplies the allowed surface set or process key.
type UIStreamAdmission struct {
	Key     string
	Windows []string
	Done    <-chan struct{}
	Retire  func()
}
type uiStreamOwner struct {
	start, write, mu sync.Mutex
	id               uint64
	cancel           context.CancelFunc
	subscription     *hostevents.Subscription
}

func (s *Server) serveUIStream(w http.ResponseWriter, r *http.Request) {
	admission, err := s.UIStreamScope(r.Context(), r.URL.Query().Get("windowLease"))
	if err != nil || admission.Key == "" || len(admission.Windows) == 0 || admission.Done == nil {
		http.Error(w, "UI stream admission required", 403)
		return
	}
	select {
	case <-admission.Done:
		http.Error(w, "UI window lease closed", 409)
		return
	default:
	}
	s.uiStreamsMu.Lock()
	if s.uiStreams == nil {
		s.uiStreams = map[string]*uiStreamOwner{}
	}
	owner := s.uiStreams[admission.Key]
	if owner == nil {
		owner = &uiStreamOwner{}
		s.uiStreams[admission.Key] = owner
		// The window lease owns retirement even when no HTTP handler remains.
		go func(key string, current *uiStreamOwner, done <-chan struct{}) {
			<-done
			current.start.Lock()
			current.mu.Lock()
			cancel, sub := current.cancel, current.subscription
			current.mu.Unlock()
			if cancel != nil {
				cancel()
			}
			if sub != nil {
				sub.Close()
			}
			current.start.Unlock()
			s.uiStreamsMu.Lock()
			if s.uiStreams[key] == current {
				delete(s.uiStreams, key)
			}
			s.uiStreamsMu.Unlock()
		}(admission.Key, owner, admission.Done)
	}
	s.uiStreamsMu.Unlock()
	ctx, cancel := context.WithCancel(r.Context())
	owner.start.Lock()
	// Waiting for the predecessor does not extend admission. Recheck before
	// cancelling it or consuming any staged commands.
	if ctx.Err() != nil {
		owner.start.Unlock()
		cancel()
		return
	}
	select {
	case <-admission.Done:
		owner.start.Unlock()
		cancel()
		http.Error(w, "UI window lease closed", 409)
		return
	default:
	}
	owner.mu.Lock()
	previous, previousSub := owner.cancel, owner.subscription
	owner.mu.Unlock()
	if previous != nil {
		previous()
	}
	if previousSub != nil {
		previousSub.Close()
	}
	owner.write.Lock()
	if ctx.Err() != nil {
		owner.write.Unlock()
		owner.start.Unlock()
		cancel()
		return
	}
	select {
	case <-admission.Done:
		owner.write.Unlock()
		owner.start.Unlock()
		cancel()
		http.Error(w, "UI window lease closed", 409)
		return
	default:
	}
	owner.mu.Lock()
	owner.id++
	id := owner.id
	owner.cancel = cancel
	subscription, initial := s.Hub.SubscribeWindows(admission.Windows)
	owner.subscription = &subscription
	owner.mu.Unlock()
	owner.write.Unlock()
	owner.start.Unlock()
	stopped := make(chan struct{})
	go func() {
		select {
		case <-admission.Done:
			cancel()
		case <-ctx.Done():
		case <-stopped:
		}
	}()
	defer close(stopped)
	defer func() {
		owner.start.Lock()
		defer owner.start.Unlock()
		cancel()
		owner.mu.Lock()
		current := owner.id == id
		if current {
			owner.cancel = nil
			owner.subscription = nil
		}
		owner.mu.Unlock()
		if current && admission.Retire != nil {
			admission.Retire()
		}
	}()
	s.writeStream(w, ctx, subscription, initial, &owner.write)
}
