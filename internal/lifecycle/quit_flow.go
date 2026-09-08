package lifecycle

import "sync"

// QuitFlow coordinates the composition-edge part of a managed application
// quit. The caller hides its native windows synchronously, while cleanup and
// the final application quit run asynchronously. A failed cleanup releases
// the flow after the caller restores a recovery surface and offers a retry.
//
// The callbacks are deliberately narrow: lifecycle owns the ordering and
// duplicate suppression, while the application edge owns windows, workers
// and the native application object.
type QuitFlow struct {
	mu           sync.Mutex
	inProgress   bool
	stateChanged func()
}

// SetStateChanged installs a short, synchronous observer for quit-flow state
// changes. The observer runs after the flow releases its mutex.
func (f *QuitFlow) SetStateChanged(observer func()) {
	if f == nil {
		return
	}
	f.mu.Lock()
	f.stateChanged = observer
	f.mu.Unlock()
}

func (f *QuitFlow) notifyStateChanged() {
	if f == nil {
		return
	}
	f.mu.Lock()
	observer := f.stateChanged
	f.mu.Unlock()
	if observer != nil {
		observer()
	}
}

// Begin accepts one quit request. hide is run before Begin returns; cleanup
// runs in the background. quit runs only after cleanup succeeds. A second
// request while cleanup is active is ignored.
func (f *QuitFlow) Begin(
	hide func(),
	cleanup func() error,
	quit func(),
	onFailure func(error),
) bool {
	if f == nil {
		return false
	}
	f.mu.Lock()
	if f.inProgress {
		f.mu.Unlock()
		return false
	}
	f.inProgress = true
	f.mu.Unlock()
	f.notifyStateChanged()

	if hide != nil {
		hide()
	}
	go func() {
		var err error
		if cleanup != nil {
			err = cleanup()
		}
		if err != nil {
			if onFailure != nil {
				onFailure(err)
			}
			f.mu.Lock()
			f.inProgress = false
			f.mu.Unlock()
			f.notifyStateChanged()
			return
		}
		if quit != nil {
			quit()
		}
	}()
	return true
}

// InProgress reports whether a managed quit is currently cleaning up.
func (f *QuitFlow) InProgress() bool {
	if f == nil {
		return false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.inProgress
}
