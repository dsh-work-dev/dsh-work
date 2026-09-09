package app

import (
	"context"
	"github.com/local/dsh-work/internal/settings"
	"github.com/wailsapp/wails/v3/pkg/application"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPetMediaCapabilityRangeAndRevocation(t *testing.T) {
	s := newTrustedPetSettingsService(newPetServiceManager(t, settings.DefaultValues()), &petServiceCatalog{}, nil)
	d := testPetDefinition("key", "Pet", "private-source.png", []byte("0123456789"))
	s.activeDefinition = &d
	s.activeKey = "key"
	p, err := s.GetPetPlayback(petTestContext(), "")
	if err != nil {
		t.Fatal(err)
	}
	h := PetMediaHandler(s, http.NotFoundHandler())
	url := p.Assets["spritesheet"]
	r := httptest.NewRequest("GET", url, nil)
	r.Header.Set("Range", "bytes=2-4")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 206 || w.Body.String() != "234" {
		t.Fatalf("range: %d %s", w.Code, w.Body.String())
	}
	other := d
	s.activeDefinition = &other
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", url, nil))
	if w.Code != 404 {
		t.Fatalf("stale capability status %d", w.Code)
	}
	untrusted := NewPetSettingsService(s.manager, s.catalog)
	if _, err := untrusted.GetPetPlayback(context.Background(), ""); err == nil {
		t.Fatal("untrusted asset capability")
	}
}

func TestPetHitRegionsRecoverAfterStall(t *testing.T) {
	s := newTrustedPetSettingsService(newPetServiceManager(t, settings.DefaultValues()), &petServiceCatalog{}, nil)
	if err := s.SetPetHitRegions(petTestContext(), []PetHitRegion{{X: .3, Y: .4, Width: .4, Height: .5}}); err != nil {
		t.Fatal(err)
	}
	if PetPointerHit(s, .1, .1) || !PetPointerHit(s, .5, .6) {
		t.Fatal("hit regions not respected")
	}
	s.hitUpdated = time.Now().Add(-3 * time.Second)
	if !PetPointerHit(s, .1, .1) {
		t.Fatal("stalled frontend kept click-through")
	}
	if err := s.SetPetHitRegions(petTestContext(), []PetHitRegion{{X: 1, Width: 1}}); err == nil {
		t.Fatal("out of bounds region accepted")
	}
}

type petTestWindow struct{ application.Window }

func (petTestWindow) Name() string { return "pet" }
func petTestContext() context.Context {
	return context.WithValue(context.Background(), application.WindowKey, petTestWindow{})
}
