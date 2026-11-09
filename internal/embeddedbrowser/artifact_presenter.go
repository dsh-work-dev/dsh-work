package embeddedbrowser

import (
	"context"
	"crypto/rand"
	"errors"
	"github.com/local/dsh-work/internal/settings"
	"path/filepath"
	"strings"
)

// OpenArtifact presents only a Host-selected bundle. There is deliberately no
// directory, entry, root ID or resource URL argument on the UI-facing seam.
func (r *InstancePresenterRegistry) OpenArtifact(ctx context.Context, peer PresenterPeer, surface string, parent uintptr, lease, slot string) (ViewState, error) {
	if len(slot) == 0 || len(slot) > 1024 {
		return ViewState{}, errors.New("CEF_SLOT_REJECTED")
	}
	i, err := r.lockSurface(peer, surface)
	if err != nil {
		return ViewState{}, err
	}
	w, err := i.views.lockWindow(peer, surface, parent, lease)
	if err != nil {
		i.serial.Unlock()
		return ViewState{}, err
	}
	if w.artifactPick != nil {
		w.serial.Unlock()
		i.serial.Unlock()
		return ViewState{}, errors.New("CEF_ARTIFACT_SELECTION_PENDING")
	}
	life, cancel := context.WithCancel(ctx)
	pending := &artifactAdmission{cancel: cancel, done: make(chan struct{})}
	w.artifactPick = pending
	selectedWindow := w
	defer func() {
		cancel()
		selectedWindow.serial.Lock()
		if selectedWindow.artifactPick == pending {
			selectedWindow.artifactPick = nil
		}
		selectedWindow.serial.Unlock()
	}()
	w.serial.Unlock()
	i.serial.Unlock()
	// The system picker checks the actual process instance/window and drains its
	// STA on cancellation. No UI string is used as a filesystem location.
	locale := settings.DefaultLocale
	if r.config.ArtifactLocale != nil {
		locale = r.config.ArtifactLocale()
	}
	path, err := chooseArtifactFile(life, peer, parent, locale)
	close(pending.done)
	if err != nil {
		return ViewState{}, err
	}
	if path == "" {
		return ViewState{}, errors.New("CEF_ARTIFACT_SELECTION_CANCELED")
	}
	if !filepath.IsAbs(path) || !(strings.EqualFold(filepath.Ext(path), ".html") || strings.EqualFold(filepath.Ext(path), ".htm")) {
		return ViewState{}, errors.New("CEF_ARTIFACT_HTML_REQUIRED")
	}
	i, err = r.lockSurface(peer, surface)
	if err != nil {
		return ViewState{}, err
	}
	defer i.serial.Unlock()
	w, err = i.views.lockWindow(peer, surface, parent, lease)
	if err != nil {
		return ViewState{}, err
	}
	defer w.serial.Unlock()
	if w.artifactPick != pending {
		return ViewState{}, errors.New("CEF_PRESENTER_DOCUMENT_RETIRED")
	}
	if err = life.Err(); err != nil {
		return ViewState{}, err
	}
	if err = r.startLocked(ctx, i); err != nil {
		return ViewState{}, err
	}
	old := i.views.get(surface, slot)
	if old != nil {
		old.serial.Lock()
		err = i.views.loseLocked(old)
		old.serial.Unlock()
		if err != nil {
			return ViewState{}, err
		}
	}
	a, err := i.broker.openArtifact(ctx, filepath.Dir(path), filepath.Base(path), true)
	if err != nil {
		return ViewState{}, err
	}
	p := &presentation{peer: peer, surface: surface, parent: parent, target: a.OwnedTarget(), view: ViewState{Lease: rand.Text(), State: "empty", ArtifactName: filepath.Base(path)}}
	i.views.mu.Lock()
	i.views.views[presentationKey(surface, slot)] = p
	i.views.mu.Unlock()
	v, err := i.views.updateLocked(ctx, peer, surface, parent, slot, "", SlotBounds{}, 1, false)
	if err != nil {
		p.serial.Lock()
		err = errors.Join(err, i.views.loseLocked(p))
		p.serial.Unlock()
	}
	r.report(i, r.rememberLocked(i))
	return r.identityLocked(i, v), err
}
