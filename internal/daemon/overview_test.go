package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/app"
	"github.com/local/dsh-work/internal/dshmanager"
	"github.com/local/dsh-work/internal/lifecycle"
)

type overviewTransport func(*http.Request) (*http.Response, error)

type overviewTestReplacer struct{}

func (overviewTestReplacer) Replace(source, destination string) error {
	return os.Rename(source, destination)
}

type overviewFailedReplacer struct{}

func (overviewFailedReplacer) Replace(string, string) error { return errors.New("replacement failed") }

func (f overviewTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func overviewResponse(r *http.Request, body io.ReadCloser) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Body: body, Header: make(http.Header), Request: r}
}

func overviewCaller() context.Context {
	return app.LocalClientContext(context.Background(), "workspace")
}

func overviewStaticTransport(payload string) overviewTransport {
	return func(r *http.Request) (*http.Response, error) {
		body := ""
		if r.URL.Path == "/__dshwork/overview" {
			body = payload
		}
		return overviewResponse(r, io.NopCloser(strings.NewReader(body))), nil
	}
}

func TestOverviewOfflineCacheIsStaleAndProfileIsolated(t *testing.T) {
	root := t.TempDir()
	profile := "data-A/profile-one"
	profileKey := func(context.Context) (string, error) { return profile, nil }
	s := &OverviewService{Root: root, Replacer: overviewTestReplacer{}, ProfileKey: profileKey}
	run, stop := context.WithCancel(context.Background())
	defer stop()
	const payload = `{"identity":"profile-one","sessions":[]}`
	s.Start(run, "http://127.0.0.1:4321/auth", overviewStaticTransport(payload))
	live, err := s.GetSnapshot(overviewCaller(), "today", "zh-CN", 28800)
	if err != nil || live.DSHPayload != payload || live.StaleAt != "" || live.DSHError != "" {
		t.Fatalf("live snapshot = %+v, %v", live, err)
	}
	stop()
	offline, err := s.GetSnapshot(overviewCaller(), "today", "zh-CN", 28800)
	if err != nil || offline.DSHPayload != payload || offline.StaleAt == "" || offline.DSHError == "" {
		t.Fatalf("offline snapshot lost stale provenance: %+v, %v", offline, err)
	}
	profile = "data-A/profile-two"
	other, err := s.GetSnapshot(overviewCaller(), "today", "zh-CN", 28800)
	if err != nil || other.DSHPayload != "" || other.ProfileKey != profile {
		t.Fatalf("cross-profile cached data: %+v, %v", other, err)
	}
	profile = "data-A/profile-one"
	restarted := &OverviewService{Root: root, Replacer: overviewTestReplacer{}, ProfileKey: profileKey}
	restored, err := restarted.GetSnapshot(overviewCaller(), "today", "zh-CN", 28800)
	if err != nil || restored.DSHPayload != payload || restored.StaleAt == "" || restored.DSHError == "" {
		t.Fatalf("restart did not retain offline cache: %+v, %v", restored, err)
	}
}

func TestOverviewCancelsInFlightGenerationRequest(t *testing.T) {
	requestStarted := make(chan struct{})
	run, stop := context.WithCancel(context.Background())
	defer stop()
	s := &OverviewService{Root: t.TempDir(), ProfileKey: func(context.Context) (string, error) { return "profile", nil }}
	s.Start(run, "http://127.0.0.1:4321/auth", overviewTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/__dshwork/overview" {
			return overviewResponse(r, io.NopCloser(strings.NewReader(""))), nil
		}
		close(requestStarted)
		<-r.Context().Done()
		return nil, r.Context().Err()
	}))
	done := make(chan OverviewSnapshot, 1)
	go func() { snapshot, _ := s.GetSnapshot(overviewCaller(), "today", "en", 0); done <- snapshot }()
	select {
	case <-requestStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("overview request did not start")
	}
	stop()
	select {
	case snapshot := <-done:
		if snapshot.DSHError == "" || snapshot.DSHPayload != "" {
			t.Fatalf("cancelled request appeared live: %+v", snapshot)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("generation cancellation did not reach HTTP request")
	}
}

type overviewReadCallback struct {
	io.Reader
	once     sync.Once
	callback func()
}

func (r *overviewReadCallback) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.once.Do(r.callback)
	return n, err
}
func (*overviewReadCallback) Close() error { return nil }

func TestOverviewCancelledResponseCannotBecomeLiveOrCached(t *testing.T) {
	run, stop := context.WithCancel(context.Background())
	defer stop()
	s := &OverviewService{Root: t.TempDir(), ProfileKey: func(context.Context) (string, error) { return "profile", nil }}
	s.Start(run, "http://127.0.0.1:4321/auth", overviewTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/__dshwork/overview" {
			return overviewResponse(r, io.NopCloser(strings.NewReader(""))), nil
		}
		return overviewResponse(r, &overviewReadCallback{Reader: strings.NewReader(`{"identity":"old-generation"}`), callback: func() { stop(); <-r.Context().Done() }}), nil
	}))
	snapshot, err := s.GetSnapshot(overviewCaller(), "today", "en", 0)
	if err == nil && (snapshot.DSHPayload != "" || snapshot.DSHError == "") {
		t.Fatalf("cancelled generation published live data: %+v", snapshot)
	}
	offline, err := s.GetSnapshot(overviewCaller(), "today", "en", 0)
	if err != nil || offline.DSHPayload != "" {
		t.Fatalf("cancelled generation polluted cache: %+v, %v", offline, err)
	}
}

func TestOverviewProfileSwitchDuringResponseRejectsPayload(t *testing.T) {
	profile := "profile-A"
	s := &OverviewService{Root: t.TempDir(), ProfileKey: func(context.Context) (string, error) { return profile, nil }}
	s.Start(context.Background(), "http://127.0.0.1:4321/auth", overviewTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/__dshwork/overview" {
			return overviewResponse(r, io.NopCloser(strings.NewReader(""))), nil
		}
		return overviewResponse(r, &overviewReadCallback{Reader: strings.NewReader(`{"identity":"profile-A"}`), callback: func() { profile = "profile-B" }}), nil
	}))
	snapshot, err := s.GetSnapshot(overviewCaller(), "today", "en", 0)
	if err != nil || snapshot.DSHPayload != "" || snapshot.DSHError == "" {
		t.Fatalf("profile switch accepted foreign payload: %+v, %v", snapshot, err)
	}
}

func TestOverviewWithoutRunContextKeepsHostData(t *testing.T) {
	s := &OverviewService{ProfileKey: func(context.Context) (string, error) { return "", errors.New("manager unavailable") }}
	s.Initialize()
	s.Observe(lifecycle.Status{State: lifecycle.StateFailed, GenerationID: "g"})
	got, err := s.GetSnapshot(overviewCaller(), "today", "en", 0)
	if err != nil || got.DSHPayload != "" || got.DSHError == "" || len(got.Events) != 1 {
		t.Fatalf("unreadable Run context dropped Host data: %+v, %v", got, err)
	}
}

type overviewCountingReplacer struct{ writes int }

func (r *overviewCountingReplacer) Replace(source, destination string) error {
	if filepath.Base(destination) == "overview-cache.json" {
		r.writes++
	}
	return os.Rename(source, destination)
}

func TestOverviewCacheIsBoundedAndRewrittenOnlyOnChange(t *testing.T) {
	root := t.TempDir()
	replacer := &overviewCountingReplacer{}
	payload := `{"sessions":[]}`
	s := &OverviewService{Root: root, Replacer: replacer, ProfileKey: func(context.Context) (string, error) { return "profile", nil }}
	s.Start(context.Background(), "http://127.0.0.1:4321/auth", overviewTransport(func(r *http.Request) (*http.Response, error) {
		return overviewStaticTransport(payload)(r)
	}))
	for i := 0; i < 3; i++ {
		if _, err := s.GetSnapshot(overviewCaller(), "today", "en", 0); err != nil {
			t.Fatal(err)
		}
	}
	if replacer.writes != 1 {
		t.Fatalf("unchanged payload rewrote the cache %d times", replacer.writes)
	}
	for offset := 0; offset < 10; offset++ {
		payload = fmt.Sprintf(`{"offset":%d}`, offset)
		if _, err := s.GetSnapshot(overviewCaller(), "today", "en", offset*3600); err != nil {
			t.Fatal(err)
		}
	}
	if len(s.cache) > overviewCacheEntries {
		t.Fatalf("cache holds %d entries", len(s.cache))
	}
	restarted := &OverviewService{Root: root, Replacer: replacer, ProfileKey: s.ProfileKey}
	got, err := restarted.GetSnapshot(overviewCaller(), "today", "en", 9*3600)
	if err != nil || got.DSHPayload == "" || got.StaleAt == "" || len(got.PersistenceErrors) != 0 {
		t.Fatalf("restart could not read the bounded cache: %+v, %v", got, err)
	}
}

func TestOverviewProfileSwitchDuringFailureRejectsOldCache(t *testing.T) {
	profile := "profile-A"
	requests := 0
	s := &OverviewService{Root: t.TempDir(), ProfileKey: func(context.Context) (string, error) { return profile, nil }}
	s.Start(context.Background(), "http://127.0.0.1:4321/auth", overviewTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/__dshwork/overview" {
			return overviewResponse(r, io.NopCloser(strings.NewReader(""))), nil
		}
		requests++
		if requests == 1 {
			return overviewResponse(r, io.NopCloser(strings.NewReader(`{"identity":"profile-A"}`))), nil
		}
		profile = "profile-B"
		return nil, io.ErrUnexpectedEOF
	}))
	if _, err := s.GetSnapshot(overviewCaller(), "today", "en", 0); err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.GetSnapshot(overviewCaller(), "today", "en", 0)
	if err == nil && snapshot.DSHPayload != "" {
		t.Fatalf("failed request displayed another Profile's cache: %+v", snapshot)
	}
}

func TestOverviewNullDiskCacheDoesNotCrashLiveCollection(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "overview-cache.json"), []byte("null"), 0600); err != nil {
		t.Fatal(err)
	}
	s := &OverviewService{Root: root, ProfileKey: func(context.Context) (string, error) { return "profile", nil }}
	s.Start(context.Background(), "http://127.0.0.1:4321/auth", overviewStaticTransport(`{"sessions":[]}`))
	snapshot, err := s.GetSnapshot(overviewCaller(), "today", "en", 0)
	if err != nil || snapshot.DSHPayload == "" || snapshot.StaleAt != "" {
		t.Fatalf("live recovery from invalid cache = %+v, %v", snapshot, err)
	}
}

func TestOverviewJournalRestoresEventsAndDeduplicatesRecoveries(t *testing.T) {
	root := t.TempDir()
	key := func(context.Context) (string, error) { return "profile", nil }
	old := &dshmanager.SwitchAttempt{AutomaticRollback: true, Rollback: dshmanager.RollbackRestored, CompletedAt: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)}
	s := &OverviewService{Root: root, Replacer: overviewTestReplacer{}, ProfileKey: key, RecoverySource: func(context.Context) (*dshmanager.SwitchAttempt, error) { return old, nil }}
	s.Initialize()
	s.Observe(lifecycle.Status{State: lifecycle.StateReady, GenerationID: "generation-one"})
	completed := time.Now().Add(time.Second).UTC().Format(time.RFC3339)
	restored := &dshmanager.SwitchAttempt{AutomaticRollback: true, Rollback: dshmanager.RollbackRestored, CompletedAt: completed}
	s.ObserveRecovery(restored)
	s.ObserveRecovery(restored)
	manual := *restored
	manual.AutomaticRollback = false
	manual.CompletedAt = time.Now().Add(2 * time.Second).UTC().Format(time.RFC3339)
	s.ObserveRecovery(&manual)
	failed := manual
	failed.AutomaticRollback = true
	failed.Rollback = dshmanager.RollbackFailed
	s.ObserveRecovery(&failed)
	s.ObserveRecovery(&failed)
	live, err := s.GetSnapshot(overviewCaller(), "today", "en", 0)
	if err != nil || live.AutomaticRecoveries != 1 || live.UptimeSeconds == nil || len(live.Events) != 3 {
		t.Fatalf("observed lifecycle/recovery = %+v, %v", live, err)
	}
	restarted := &OverviewService{Root: root, Replacer: overviewTestReplacer{}, ProfileKey: key, RecoverySource: func(context.Context) (*dshmanager.SwitchAttempt, error) { return restored, nil }}
	restarted.Initialize()
	got, err := restarted.GetSnapshot(overviewCaller(), "today", "en", 0)
	if err != nil || len(got.Events) != 3 || got.Events[0].Generation != "generation-one" || got.Events[1].Kind != "automatic-recovery" || got.Events[1].State != "restored" || got.Events[2].State != "failed" || got.AutomaticRecoveries != 1 || got.RecoveryTrackingSince != live.RecoveryTrackingSince {
		t.Fatalf("restored journal = %+v, %v", got, err)
	}
	if got.UptimeSeconds != nil {
		t.Fatal("previous daemon's Ready uptime was reused")
	}
}

func TestOverviewOperationEventsPersistAndDeduplicate(t *testing.T) {
	root := t.TempDir()
	s := &OverviewService{Root: root, Replacer: overviewTestReplacer{}, ProfileKey: func(context.Context) (string, error) { return "profile", nil }}
	s.Initialize()
	event := OverviewEvent{ID: "plugin-operation", Kind: "plugin-update", State: "completed", At: time.Now().UTC().Format(time.RFC3339Nano)}
	s.RecordEvent(event)
	s.RecordEvent(event)
	restarted := &OverviewService{Root: root, Replacer: overviewTestReplacer{}, ProfileKey: s.ProfileKey}
	restarted.Initialize()
	restarted.RecordEvent(event)
	restarted.RecordEvent(OverviewEvent{ID: "restore-operation", Kind: "restore", State: "failed", At: event.At})
	got, err := restarted.GetSnapshot(overviewCaller(), "today", "en", 0)
	if err != nil || len(got.Events) != 2 || got.Events[0] != event || got.Events[1].Kind != "restore" || got.Events[1].State != "failed" {
		t.Fatalf("terminal events lost or duplicated: %+v, %v", got, err)
	}
}

func TestOverviewPersistenceFailureKeepsLiveDataAndPreviousJournal(t *testing.T) {
	root := t.TempDir()
	key := func(context.Context) (string, error) { return "profile", nil }
	s := &OverviewService{Root: root, Replacer: overviewTestReplacer{}, ProfileKey: key}
	s.Initialize()
	s.Observe(lifecycle.Status{State: lifecycle.StateReady, GenerationID: "g"})
	before, err := os.ReadFile(filepath.Join(root, "overview-journal.json"))
	if err != nil {
		t.Fatal(err)
	}
	s.Replacer = overviewFailedReplacer{}
	s.Observe(lifecycle.Status{State: lifecycle.StateStopped, GenerationID: "g"})
	s.Start(context.Background(), "http://127.0.0.1:4321/auth", overviewStaticTransport(`{"sessions":[]}`))
	got, err := s.GetSnapshot(overviewCaller(), "today", "en", 0)
	if err != nil || got.DSHPayload == "" || got.StaleAt != "" || got.PersistenceErrors["journalSave"] == "" || got.PersistenceErrors["cacheSave"] == "" {
		t.Fatalf("persistence failure blocked live result or was hidden: %+v, %v", got, err)
	}
	after, err := os.ReadFile(filepath.Join(root, "overview-journal.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("failed replacement changed previous journal")
	}
	leftovers, _ := filepath.Glob(filepath.Join(root, ".overview-*.tmp"))
	if len(leftovers) > 0 {
		t.Fatalf("temporary files leaked: %v", leftovers)
	}
	s.Replacer = overviewTestReplacer{}
	s.Observe(lifecycle.Status{State: lifecycle.StateStopped, GenerationID: "g"})
	got, err = s.GetSnapshot(overviewCaller(), "today", "en", 0)
	if err != nil || len(got.PersistenceErrors) != 0 {
		t.Fatalf("successful retry kept persistence error: %+v, %v", got, err)
	}
}

func TestOverviewEmptyRootIsMemoryOnly(t *testing.T) {
	s := &OverviewService{Replacer: overviewFailedReplacer{}, ProfileKey: func(context.Context) (string, error) { return "profile", nil }}
	s.Initialize()
	s.Observe(lifecycle.Status{State: lifecycle.StateReady, GenerationID: "g"})
	s.Start(context.Background(), "http://127.0.0.1:4321/auth", overviewStaticTransport(`{"sessions":[]}`))
	got, err := s.GetSnapshot(overviewCaller(), "today", "en", 0)
	if err != nil || got.DSHPayload == "" || len(got.PersistenceErrors) != 0 {
		t.Fatalf("memory-only service attempted persistence: %+v, %v", got, err)
	}
}
