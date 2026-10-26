package presence

import "testing"

func TestTrackerTracksFocusPerSurfaceAndLastFocused(t *testing.T) {
	tracker := New()
	if tracker.AnyFocused() || tracker.LastFocused() != "workspace" {
		t.Fatal("empty tracker must be unfocused and default to workspace")
	}
	tracker.SetFocused("workspace", true)
	tracker.SetFocused("workspace-2", true)
	if !tracker.AnyFocused() || !tracker.Focused("workspace") || !tracker.Focused("workspace-2") || tracker.LastFocused() != "workspace-2" {
		t.Fatal("both surfaces should be focused and the latest focus should be workspace-2")
	}
	tracker.SetFocused("workspace-2", false)
	if !tracker.AnyFocused() || tracker.Focused("workspace-2") || !tracker.Focused("workspace") {
		t.Fatal("focus loss should affect only its surface")
	}
	if tracker.LastFocused() != "workspace-2" {
		t.Fatal("focus loss should retain the last focused surface")
	}
	tracker.SetFocused("workspace-2", true)
	tracker.Clear("workspace")
	if !tracker.AnyFocused() || tracker.Focused("workspace") {
		t.Fatal("Clear should remove only the selected surface")
	}
	tracker.Clear("workspace-2")
	if tracker.LastFocused() != "workspace" {
		t.Fatal("clearing the last-focused surface should fall back to workspace")
	}
	tracker.SetFocused("workspace-3", true)
	tracker.ClearAll()
	if tracker.AnyFocused() || tracker.Focused("workspace-3") || tracker.LastFocused() != "workspace" {
		t.Fatal("ClearAll should reset focus and last-focused surface")
	}
}
