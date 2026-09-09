package app

import (
	"context"
	"math"
	"time"
)

type PetHitRegion struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// SetPetHitRegions reports normalized regions in the trusted pet viewport.
func (s *PetSettingsService) SetPetHitRegions(ctx context.Context, regions []PetHitRegion) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, cancel, err := s.beginOperationForSurface(ctx, "pet")
	if err != nil {
		return err
	}
	defer cancel()
	if len(regions) > 8 {
		return petSettingsUnavailable()
	}
	for _, r := range regions {
		for _, v := range []float64{r.X, r.Y, r.Width, r.Height} {
			if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 1 {
				return petSettingsUnavailable()
			}
		}
		if r.X+r.Width > 1.001 || r.Y+r.Height > 1.001 {
			return petSettingsUnavailable()
		}
	}
	s.hitRegions = append([]PetHitRegion(nil), regions...)
	s.hitUpdated = time.Now()
	return nil
}

// PetPointerHit is a Host-only query. A stalled WebView restores ordinary input
// so a stale hit map cannot strand the pet in permanent click-through mode.
func PetPointerHit(s *PetSettingsService, x, y float64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if time.Since(s.hitUpdated) > 2*time.Second {
		return true
	}
	for _, r := range s.hitRegions {
		if x >= r.X && y >= r.Y && x <= r.X+r.Width && y <= r.Y+r.Height {
			return true
		}
	}
	return false
}
