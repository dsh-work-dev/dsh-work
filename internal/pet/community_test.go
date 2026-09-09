package pet

import (
	"bytes"
	"context"
	"github.com/at-wat/ebml-go"
	"github.com/at-wat/ebml-go/webm"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func videoFixture(t *testing.T, width uint64) []byte {
	t.Helper()
	var b bytes.Buffer
	doc := struct {
		EBML    webm.EBMLHeader
		Segment struct {
			Info   webm.Info
			Tracks webm.Tracks
		}
	}{}
	doc.EBML.DocType = "webm"
	doc.Segment.Info = webm.Info{TimecodeScale: 1000000, Duration: 1000}
	doc.Segment.Tracks.TrackEntry = []webm.TrackEntry{{TrackNumber: 1, TrackUID: 1, TrackType: 1, CodecID: "V_VP9", Video: &webm.Video{PixelWidth: width, PixelHeight: 36}}}
	if err := ebml.Marshal(&doc, &b); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestCommunityCatalogLoadsJSONCAndBoundsWebM(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "pets", "whale")
	if err := os.MkdirAll(filepath.Join(root, "webm"), 0700); err != nil {
		t.Fatal(err)
	}
	config := `{ // community package
 "pets":[{"id":"main","name":"Whale"}],
 "animations":{"idle":["breath"],"clicks":["wave"],"categories":[{"weight":10,"actions":["wave"]}],"events":{"workStatus":["breath","wave","breath","wave","wave","wave"]}},
 "animationWeights":{"idle":80,"turn":0,"move":0},
 }`
	if err := os.WriteFile(filepath.Join(root, "config.jsonc"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"breath", "wave"} {
		if err := os.WriteFile(filepath.Join(root, "webm", n+".webm"), videoFixture(t, 64), 0600); err != nil {
			t.Fatal(err)
		}
	}
	catalog, err := NewPetCatalog(CatalogConfig{CommunityHome: home, CacheRoot: filepath.Join(home, "cache"), HomeDir: func() (string, error) { return home, nil }, Env: func(string) string { return "" }})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := catalog.Refresh(context.Background())
	if err != nil || len(snapshot.Items) != 1 {
		t.Fatalf("catalog: %+v %v", snapshot, err)
	}
	d, err := catalog.Resolve(context.Background(), snapshot.Items[0].StableSourceKey)
	if err != nil {
		t.Fatal(err)
	}
	if d.Source.Profile != RendererWebM || d.Behavior == nil || d.States["working"].Track != "wave" || !d.States["working"].Loop {
		t.Fatalf("adaptation: %+v", d)
	}
	p := ProjectPlayback(d, "opaque")
	if !p.Video || p.Tracks["wave"][0].Width != 64 {
		t.Fatalf("playback: %+v", p)
	}
	if _, _, _, err := webmMetadata(videoFixture(t, MaxImageWidth+1)); err == nil {
		t.Fatal("oversized video accepted")
	}
	if err := os.Remove(filepath.Join(root, "webm", "wave.webm")); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.Revalidate(context.Background(), snapshot.Items[0].StableSourceKey); err == nil {
		t.Fatal("missing declared media accepted")
	}
}

func TestClickRestoresWorkAndIdleRespectsVisibility(t *testing.T) {
	now := time.Unix(100, 0)
	d := runtimeTestDefinition()
	d.Actions["wave"] = ActionSpec{Track: "attention", Fallback: "idle", Interruptible: true}
	r := NewRuntime(RuntimeConfig{Clock: func() time.Time { return now }, Random: func() float64 { return .99 }})
	if err := r.Start(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	_ = r.Dispatch(PetInputEvent{Type: "task.working"})
	_ = r.Dispatch(PetInputEvent{Type: "click"})
	if s := r.Snapshot(now); s.BaseState != BaseWorking || s.Action != "wave" {
		t.Fatalf("click: %+v", s)
	}
	now = now.Add(200 * time.Millisecond)
	if s := r.Snapshot(now); s.TrackID != "working" || s.Action != "" {
		t.Fatalf("did not resume work: %+v", s)
	}
	_ = r.Dispatch(PetInputEvent{Type: "host.ready"})
	r.Snapshot(now)
	now = now.Add(30 * time.Second)
	if s := r.Snapshot(now); s.Action != "wave" {
		t.Fatalf("idle did not vary: %+v", s)
	}
	r.SetHidden(true)
	_ = r.Dispatch(PetInputEvent{Type: "click"})
	r.SetHidden(false)
	r.SetReducedMotion(true)
	now = now.Add(time.Hour)
	_ = r.Dispatch(PetInputEvent{Type: "click"})
	if s := r.Snapshot(now); s.Action != "" || !s.ReducedMotion {
		t.Fatalf("reduced motion: %+v", s)
	}
}
