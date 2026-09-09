package app

import (
	"bytes"
	"context"
	"github.com/local/dsh-work/internal/pet"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// GetPetPlayback fetches a selection's immutable media plan only when it changes.
func (s *PetSettingsService) GetPetPlayback(ctx context.Context, known string) (*pet.Playback, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, cancel, err := s.beginOperationForSurface(ctx, "pet")
	if err != nil {
		return nil, err
	}
	defer cancel()
	if s.activeDefinition == nil {
		return nil, nil
	}
	key := s.playbackKeyLocked()
	if known == key {
		return nil, nil
	}
	p := pet.ProjectPlayback(*s.activeDefinition, key)
	return &p, nil
}

func (s *PetSettingsService) playbackKeyLocked() string {
	if s.playbackDefinition != s.activeDefinition {
		s.playbackDefinition = s.activeDefinition
		s.playbackToken = newPetPreviewRef(s.activeKey, time.Now())
	}
	return s.playbackToken
}

// PetMediaHandler serves only the current validated, immutable selection. The
// unguessable capability is issued exclusively to the trusted pet surface.
func PetMediaHandler(s *PetSettingsService, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/__pet_media/") {
			next.ServeHTTP(w, r)
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/__pet_media/"), "/")
		if len(parts) != 2 {
			http.NotFound(w, r)
			return
		}
		if parts[1] == "preview" {
			s.mu.Lock()
			asset, ok := s.previewMedia[parts[0]]
			_, valid := s.previewKeyLocked(parts[0], time.Now())
			closed := s.closed
			s.mu.Unlock()
			if !ok || !valid || closed {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "video/webm")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("Cache-Control", "no-store")
			http.ServeContent(w, r, "preview.webm", time.Time{}, bytes.NewReader(asset.Data))
			return
		}
		index, err := strconv.Atoi(parts[1])
		if err != nil {
			http.NotFound(w, r)
			return
		}
		s.mu.Lock()
		if s.closed || s.activeDefinition == nil || s.playbackDefinition != s.activeDefinition || parts[0] != s.playbackToken || index < 0 || index >= len(s.activeDefinition.Assets) {
			s.mu.Unlock()
			http.NotFound(w, r)
			return
		}
		asset := s.activeDefinition.Assets[index]
		s.mu.Unlock()
		mime := "image/png"
		if asset.Format == "webp" {
			mime = "image/webp"
		}
		if asset.Format == "webm" {
			mime = "video/webm"
		}
		w.Header().Set("Content-Type", mime)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
		http.ServeContent(w, r, "asset", time.Time{}, bytes.NewReader(asset.Data))
	})
}

// PetGesture accepts a fixed gesture vocabulary, never task or window commands.
func (s *PetSettingsService) PetGesture(ctx context.Context, kind string, x, y float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, cancel, err := s.beginOperationForSurface(ctx, "pet")
	if err != nil {
		return err
	}
	defer cancel()
	if kind != "click" && kind != "pointer.move" && kind != "pointer.leave" {
		return petSettingsUnavailable()
	}
	if s.activeRuntime == nil {
		return nil
	}
	return s.activeRuntime.Dispatch(pet.PetInputEvent{Type: kind, Generation: s.generation, Payload: map[string]any{"x": x, "y": y}})
}
