package dshactivity

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestActivityPriorityAndDistinctOutcomes(t *testing.T) {
	s := Snapshot{Sessions: []Activity{
		{SessionID: "running", Seq: 100, Running: true},
		{SessionID: "ready", Seq: 5, Outcome: "completed", Unread: true},
		{SessionID: "failed", Seq: 9, Outcome: "error"},
		{SessionID: "question", Seq: 2, Running: true, Interaction: &Interaction{Key: "q1", Kind: "question", Text: "Choose a format"}},
		{SessionID: "cancelled", Seq: 4, Outcome: "aborted"},
	}}
	if !normalize(&s) {
		t.Fatal("valid activities rejected")
	}
	for i, id := range []string{"question", "failed", "ready", "running", "cancelled"} {
		if s.Sessions[i].SessionID != id {
			t.Fatalf("priority: %+v", s.Sessions)
		}
	}
	s.Connected = true
	kind, key := AnimationIntent(s)
	if kind != "task.waiting" || key == "" {
		t.Fatalf("question intent: %s %s", kind, key)
	}
	for _, outcome := range []string{"blocked", "max-tokens", "interrupted"} {
		if got := activityState(Activity{Outcome: outcome}); got != outcome {
			t.Fatalf("%s => %s", outcome, got)
		}
	}
	if got := activityState(Activity{Outcome: "completed"}); got != "idle" {
		t.Fatal("read completion remained ready")
	}
	if got := activityState(Activity{Running: true, Goal: &Goal{Phase: "blocked"}}); got != "blocked" {
		t.Fatal("blocked goal lost")
	}
	for outcome, intent := range map[string]string{"completed": "host.ready", "aborted": "task.cancelled", "error": "task.failed", "blocked": "task.blocked", "max-tokens": "task.limited", "interrupted": "task.interrupted"} {
		a := Activity{SessionID: "terminal", Outcome: outcome}
		a.State = activityState(a)
		got, _ := AnimationIntent(Snapshot{Connected: true, Sessions: []Activity{a}})
		if got != intent {
			t.Fatalf("%s intent = %s, want %s", outcome, got, intent)
		}
	}
}

func TestBridgeAuthenticatedSnapshotNavigationAndGenerationIsolation(t *testing.T) {
	b := New(t.TempDir())
	if _, err := b.Prepare("g1"); err != nil {
		t.Fatal(err)
	}
	token := b.token
	navigated := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.SetCookie(w, &http.Cookie{Name: "dsh-test", Value: "authenticated", Path: "/"})
			return
		}
		cookie, err := r.Cookie("dsh-test")
		if err != nil || cookie.Value != "authenticated" || r.Header.Get("X-DSH-Work-Token") != token {
			http.Error(w, "forbidden", 403)
			return
		}
		if r.Method == http.MethodPost {
			var input map[string]string
			_ = json.NewDecoder(r.Body).Decode(&input)
			navigated <- input["sessionId"]
			return
		}
		_ = json.NewEncoder(w).Encode(Snapshot{SchemaVersion: 1, Generation: "g1", Sessions: []Activity{{SessionID: "one", Seq: 7, Running: true}}})
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer b.Close()
	b.Start(ctx, server.URL)
	deadline := time.Now().Add(3 * time.Second)
	for !b.Snapshot().Connected && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !b.Snapshot().Connected {
		t.Fatal("bridge did not authenticate/fetch")
	}
	if err := b.Open(ctx, "one"); err != nil {
		t.Fatal(err)
	}
	if got := <-navigated; got != "one" {
		t.Fatalf("navigation = %q", got)
	}
	if err := b.Open(ctx, "arbitrary"); err == nil {
		t.Fatal("unknown navigation admitted")
	}
	patch, err := b.Prepare("g2")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(patch) != b.root {
		t.Fatal("patch escaped Host directory")
	}
	b.accept("g1", Snapshot{SchemaVersion: 1, Generation: "g1", Sessions: []Activity{{SessionID: "old"}}})
	if b.Snapshot().Connected || len(b.Snapshot().Sessions) != 0 {
		t.Fatal("old generation published")
	}
	content, err := os.ReadFile(patch)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(content) {
		t.Fatal("invalid JSON/YAML patch")
	}
}
