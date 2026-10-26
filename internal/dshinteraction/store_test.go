package dshinteraction

import (
	"reflect"
	"testing"
	"time"
)

type callbackEvent struct {
	kind        string
	surface     string
	instance    string
	sessionID   string
	interaction Interaction
}

func TestReportTracksPageStateAndRejectsAnInstanceOwnedByAnotherSurface(t *testing.T) {
	store := New()
	if err := store.Report("workspace", Report{Instance: "page-1", SessionID: "", Current: "current-1", Visible: true}); err != nil {
		t.Fatal(err)
	}
	instance, current, visible, ok := store.Page("workspace")
	if !ok || instance != "page-1" || current != "current-1" || !visible {
		t.Fatalf("Page(workspace) = %q %q %t %t", instance, current, visible, ok)
	}
	if surface, ok := store.Owner("page-1"); !ok || surface != "workspace" {
		t.Fatalf("Owner(page-1) = %q %t", surface, ok)
	}
	if err := store.Report("workspace", Report{Instance: "page-1", SessionID: "session-1", Kind: "question", Key: "question:1", Current: "ignored", Visible: false}); err != nil {
		t.Fatal(err)
	}
	_, current, visible, _ = store.Page("workspace")
	if current != "current-1" || !visible {
		t.Fatalf("interaction report changed page context to %q %t", current, visible)
	}
	if err := store.Report("workspace-2", Report{Instance: "page-1", SessionID: "session-2", Kind: "question", Key: "question:1"}); err == nil {
		t.Fatal("accepted an instance already owned by another surface")
	}
	if instance, _, _, ok := store.Page("workspace-2"); ok {
		t.Fatalf("rejected report created page %q", instance)
	}
}

func TestPageReloadReplacesOldInstanceAndRemovesEveryInteractionFirst(t *testing.T) {
	store := New()
	var events []callbackEvent
	store.OnRemoved(func(instance, sessionID string, it Interaction) {
		events = append(events, callbackEvent{kind: "removed", instance: instance, sessionID: sessionID, interaction: it})
	})
	store.OnAdded(func(surface, instance, sessionID string, it Interaction) {
		events = append(events, callbackEvent{kind: "added", surface: surface, instance: instance, sessionID: sessionID, interaction: it})
	})
	for _, sessionID := range []string{"session-b", "session-a"} {
		if err := store.Report("workspace", Report{Instance: "page-old", SessionID: sessionID, Kind: "question", Key: sessionID + ":1"}); err != nil {
			t.Fatal(err)
		}
	}
	events = nil
	if err := store.Report("workspace", Report{Instance: "page-new", SessionID: "session-c", Kind: "approval", Key: "approval:3", ToolName: "shell.exec"}); err != nil {
		t.Fatal(err)
	}
	want := []callbackEvent{
		{kind: "removed", instance: "page-old", sessionID: "session-a", interaction: Interaction{Kind: "question", Key: "session-a:1"}},
		{kind: "removed", instance: "page-old", sessionID: "session-b", interaction: Interaction{Kind: "question", Key: "session-b:1"}},
		{kind: "added", surface: "workspace", instance: "page-new", sessionID: "session-c", interaction: Interaction{Kind: "approval", Key: "approval:3", ToolName: "shell.exec"}},
	}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("page-reload callbacks = %#v, want %#v", events, want)
	}
	if _, ok := store.Owner("page-old"); ok {
		t.Fatal("old instance still has an owner")
	}
	if surface, ok := store.Owner("page-new"); !ok || surface != "workspace" {
		t.Fatalf("new instance owner = %q %t", surface, ok)
	}
}

func TestNewKeyRemovesOldInteractionBeforeAddingReplacement(t *testing.T) {
	store := New()
	var events []callbackEvent
	store.OnRemoved(func(instance, sessionID string, it Interaction) {
		events = append(events, callbackEvent{kind: "removed", instance: instance, sessionID: sessionID, interaction: it})
	})
	store.OnAdded(func(surface, instance, sessionID string, it Interaction) {
		events = append(events, callbackEvent{kind: "added", surface: surface, instance: instance, sessionID: sessionID, interaction: it})
	})
	if err := store.Report("workspace", Report{Instance: "page-1", SessionID: "session-1", Kind: "question", Key: "question:1"}); err != nil {
		t.Fatal(err)
	}
	events = nil
	if err := store.Report("workspace", Report{Instance: "page-1", SessionID: "session-1", Kind: "plan-review", Key: "plan:2"}); err != nil {
		t.Fatal(err)
	}
	want := []callbackEvent{
		{kind: "removed", instance: "page-1", sessionID: "session-1", interaction: Interaction{Kind: "question", Key: "question:1"}},
		{kind: "added", surface: "workspace", instance: "page-1", sessionID: "session-1", interaction: Interaction{Kind: "plan-review", Key: "plan:2"}},
	}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("replacement callbacks = %#v, want %#v", events, want)
	}
}

func TestClearOperationsRemoveEachInteraction(t *testing.T) {
	for _, test := range []struct {
		name  string
		clear func(*Store)
	}{
		{name: "reset", clear: func(store *Store) { store.Reset("workspace", "page-1") }},
		{name: "window", clear: func(store *Store) { store.ClearWindow("workspace") }},
		{name: "all", clear: func(store *Store) { store.ClearAll() }},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := New()
			var removed []callbackEvent
			store.OnRemoved(func(instance, sessionID string, it Interaction) {
				removed = append(removed, callbackEvent{instance: instance, sessionID: sessionID, interaction: it})
			})
			for _, sessionID := range []string{"session-a", "session-b"} {
				if err := store.Report("workspace", Report{Instance: "page-1", SessionID: sessionID, Kind: "approval", Key: sessionID + ":1"}); err != nil {
					t.Fatal(err)
				}
			}
			test.clear(store)
			if len(removed) != 2 || removed[0].sessionID != "session-a" || removed[1].sessionID != "session-b" {
				t.Fatalf("removed interactions = %+v", removed)
			}
			if _, _, _, ok := store.Page("workspace"); ok {
				t.Fatal("clear left the page in the store")
			}
			if _, ok := store.Owner("page-1"); ok {
				t.Fatal("clear left the instance owner in the store")
			}
		})
	}
}

func TestCallbacksRunOutsideStoreLockAndCanReenter(t *testing.T) {
	store := New()
	done := make(chan struct{})
	var events []string
	store.OnAdded(func(surface, instance, sessionID string, it Interaction) {
		events = append(events, "added:"+it.Key)
		store.Reset(surface, instance)
	})
	store.OnRemoved(func(instance, sessionID string, it Interaction) {
		events = append(events, "removed:"+it.Key)
	})
	go func() {
		defer close(done)
		_ = store.Report("workspace", Report{Instance: "page-1", SessionID: "session-1", Kind: "question", Key: "question:1"})
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("reentrant store callback deadlocked")
	}
	if !reflect.DeepEqual(events, []string{"added:question:1", "removed:question:1"}) {
		t.Fatalf("callbacks = %v", events)
	}
	if _, _, _, ok := store.Page("workspace"); ok {
		t.Fatal("reentrant reset did not clear page")
	}
}
