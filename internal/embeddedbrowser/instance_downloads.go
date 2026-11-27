package embeddedbrowser

import (
	"context"
	"errors"
	"sync"

	"golang.org/x/sync/errgroup"
)

// PrepareCloseInstance separates interactive download waiting from the existing
// bounded native disposal call. Only the admitted native owner can acquire it.
func (r *InstancePresenterRegistry) PrepareCloseInstance(ctx context.Context, peer PresenterPeer, surface string) error {
	r.mu.Lock()
	i := r.surfaces[surface]
	r.mu.Unlock()
	if i == nil {
		return nil
	}
	i.serial.Lock()
	if i.peer != peer {
		i.serial.Unlock()
		return errors.New("CEF_INSTANCE_OWNER_REJECTED")
	}
	if i.downloadResume != nil {
		preparing := i.downloadPreparing
		i.serial.Unlock()
		if preparing {
			return errors.New("DOWNLOAD_SHUTDOWN_BUSY")
		}
		return nil
	}
	b := i.broker
	resume := func() {}
	if b != nil && b.downloads != nil {
		var err error
		resume, err = b.downloads.quiesce()
		if err != nil {
			i.serial.Unlock()
			return err
		}
	}
	i.downloadResume = resume
	i.downloadPreparing = true
	i.serial.Unlock()
	// Progress and provider cancellation still need the instance serial owner.
	// Do not hold it across a user's question, Save dialog or transfer wait.
	cancel := false
	var err error
	if b != nil && b.downloads != nil {
		if b.downloads.busy() && r.config.DownloadDecision != nil {
			cancel, err = r.config.DownloadDecision(ctx)
		}
		if err == nil {
			err = b.downloads.drain(ctx, cancel)
		}
	}
	i.serial.Lock()
	defer i.serial.Unlock()
	if i.broker != b || i.downloadResume == nil {
		return ErrClosed
	}
	i.downloadPreparing = false
	if err != nil {
		resume()
		i.downloadResume = nil
	}
	return err
}

// CancelQuitAdmission returns a runner's temporary quit fence only while the
// current owners have not begun retirement. It never reopens a closed registry.
func (r *InstancePresenterRegistry) CancelQuitAdmission() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.quitAdmission = false
}

// ReopenAfterMaintenance reuses durable descriptors after a failed installer
// handoff. Only fully joined/finished owners may be discarded. A new admitted
// UI receives fresh leases and NeedsReload descriptors, never old documents.
func (r *InstancePresenterRegistry) ReopenAfterMaintenance() error {
	r.shutdown.Lock()
	defer r.shutdown.Unlock()
	if !r.CleanupComplete() {
		return errors.New("CEF_MAINTENANCE_OWNER_STILL_LIVE")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.instances = map[string]*instancePresenter{}
	r.surfaces = map[string]*instancePresenter{}
	r.processes = map[PresenterPeer]*instancePresenter{}
	r.closed = false
	r.quitAdmission = false
	return nil
}

// PrepareDownloadShutdown holds admission across every current broker and any
// future instance. The caller retains this lease until shutdown succeeds, or
// releases it on failure while the same UI owners are still available.
func (r *InstancePresenterRegistry) PrepareDownloadShutdown(ctx context.Context) (func(), error) {
	r.mu.Lock()
	if r.downloadPaused {
		r.mu.Unlock()
		return nil, errors.New("DOWNLOAD_SHUTDOWN_BUSY")
	}
	r.downloadPaused = true
	list := make([]*instancePresenter, 0, len(r.instances))
	for _, i := range r.instances {
		list = append(list, i)
	}
	r.mu.Unlock()
	var releases []func()
	var once sync.Once
	resume := func() {
		once.Do(func() {
			for _, release := range releases {
				release()
			}
			r.mu.Lock()
			r.downloadPaused = false
			r.mu.Unlock()
		})
	}
	var brokers []*Broker
	for _, i := range list {
		i.serial.Lock()
		b := i.broker
		if b != nil && b.downloads != nil {
			release, err := b.downloads.quiesce()
			if err != nil {
				i.serial.Unlock()
				resume()
				return nil, err
			}
			releases = append(releases, release)
			brokers = append(brokers, b)
		}
		i.serial.Unlock()
	}
	cancel, busy := false, false
	for _, b := range brokers {
		busy = busy || b.downloads.busy()
	}
	if busy && r.config.DownloadDecision != nil {
		var err error
		cancel, err = r.config.DownloadDecision(ctx)
		if err != nil {
			resume()
			return nil, err
		}
	}
	group, drainContext := errgroup.WithContext(ctx)
	for _, b := range brokers {
		group.Go(func() error { return b.downloads.drain(drainContext, cancel) })
	}
	if err := group.Wait(); err != nil {
		resume()
		return nil, err
	}
	return resume, nil
}
