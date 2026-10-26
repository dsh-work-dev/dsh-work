package desktopapp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/dshinteraction"
	"github.com/local/dsh-work/internal/nativeui"
	"github.com/local/dsh-work/internal/notifications"
	"github.com/local/dsh-work/internal/settings"
	wailsnotifications "github.com/wailsapp/wails/v3/pkg/services/notifications"
)

func TestInteractionNotifierAddedTruthTable(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		for _, interactionRequired := range []bool{false, true} {
			for _, focused := range []bool{false, true} {
				for _, visible := range []bool{false, true} {
					for _, current := range []bool{false, true} {
						name := fmt.Sprintf("enabled=%t interaction=%t focused=%t visible=%t current=%t", enabled, interactionRequired, focused, visible, current)
						t.Run(name, func(t *testing.T) {
							currentSession := "other-session"
							if current {
								currentSession = "session-1"
							}
							var published []notifications.Event
							notifier := &interactionNotifier{
								page: func(surface string) (string, string, bool, bool) {
									return "page-1", currentSession, visible, surface == "workspace"
								},
								focused: func(surface string) bool { return focused && surface == "workspace" },
								preferences: func() notifications.Preferences {
									return notifications.Preferences{Enabled: enabled, InteractionRequired: interactionRequired}
								},
								publish: func(_ context.Context, event notifications.Event) error {
									published = append(published, event)
									return nil
								},
								locale: func() settings.Locale { return settings.LocaleEnglish },
							}
							notifier.added("workspace", "page-1", "session-1", dshinteraction.Interaction{Kind: "approval", Key: "approval:1", ToolName: "shell.exec"})
							wantSend := enabled && interactionRequired && !(focused && visible && current)
							if gotSend := len(published) == 1; gotSend != wantSend {
								t.Fatalf("published = %d, want send=%t", len(published), wantSend)
							}
						})
					}
				}
			}
		}
	}
}

func TestInteractionNotifierIgnoresFocusInAnotherWindow(t *testing.T) {
	var published int
	notifier := &interactionNotifier{
		page: func(surface string) (string, string, bool, bool) {
			return "page-1", "session-1", true, surface == "workspace"
		},
		focused: func(surface string) bool { return surface == "workspace-2" },
		preferences: func() notifications.Preferences {
			return notifications.Preferences{Enabled: true, InteractionRequired: true}
		},
		publish: func(context.Context, notifications.Event) error { published++; return nil },
		locale:  func() settings.Locale { return settings.LocaleEnglish },
	}
	notifier.added("workspace", "page-1", "session-1", dshinteraction.Interaction{Kind: "approval", Key: "approval:1"})
	if published != 1 {
		t.Fatalf("published = %d, want 1 while workspace-2 is focused", published)
	}
}

func TestInteractionNotifierBuildsPrivateApprovalAndAnswerNotifications(t *testing.T) {
	for _, test := range []struct {
		kind       string
		categoryID string
		title      string
		body       string
	}{
		{kind: "approval", categoryID: dshApprovalCategoryID, title: "Approval required", body: "shell.exec is waiting for a permission decision"},
		{kind: "question", title: "Answer needed", body: "A conversation is waiting for your answer"},
		{kind: "plan-review", title: "Answer needed", body: "A conversation is waiting for your answer"},
	} {
		t.Run(test.kind, func(t *testing.T) {
			var published []notifications.Event
			notifier := &interactionNotifier{
				page:    func(string) (string, string, bool, bool) { return "page-1", "elsewhere", true, true },
				focused: func(string) bool { return true },
				preferences: func() notifications.Preferences {
					return notifications.Preferences{Enabled: true, InteractionRequired: true}
				},
				publish: func(_ context.Context, event notifications.Event) error {
					published = append(published, event)
					return nil
				},
				locale: func() settings.Locale { return settings.LocaleEnglish },
			}
			notifier.added("workspace", "page-1", "session-1", dshinteraction.Interaction{Kind: test.kind, Key: "opaque-key", ToolName: "shell.exec"})
			if len(published) != 1 {
				t.Fatalf("published events = %d, want 1", len(published))
			}
			event := published[0]
			if event.ID != "dsh-interaction:page-1:opaque-key" || event.Class != notifications.ClassDSHInteraction || event.Title != test.title || event.Body != test.body || event.CategoryID != test.categoryID || !event.AllowWhenWorkspaceFocused {
				t.Fatalf("event = %#v", event)
			}
			wantData := map[string]string{"kind": "dsh-interaction", "instance": "page-1", "sessionId": "session-1", "key": "opaque-key"}
			if len(event.Data) != len(wantData) {
				t.Fatalf("Data = %#v, want %#v", event.Data, wantData)
			}
			for key, want := range wantData {
				if event.Data[key] != want {
					t.Fatalf("Data[%q] = %q, want %q", key, event.Data[key], want)
				}
			}
			for _, forbidden := range []string{"reason", "command", "parameter", "path", "session title", "private-session-title"} {
				if strings.Contains(event.Body, forbidden) {
					t.Fatalf("Body contains private content %q: %q", forbidden, event.Body)
				}
			}
			if _, exists := event.Data["toolName"]; exists {
				t.Fatalf("Data contains toolName: %#v", event.Data)
			}
		})
	}
}

func TestInteractionNotifierUsesEmptyToolFallbackAndCurrentPage(t *testing.T) {
	var event notifications.Event
	notifier := &interactionNotifier{
		page:    func(string) (string, string, bool, bool) { return "replacement", "other", false, true },
		focused: func(string) bool { return false },
		preferences: func() notifications.Preferences {
			return notifications.Preferences{Enabled: true, InteractionRequired: true}
		},
		publish: func(_ context.Context, value notifications.Event) error { event = value; return nil },
		locale:  func() settings.Locale { return settings.LocaleChinese },
	}
	notifier.added("workspace", "stale-page", "session-1", dshinteraction.Interaction{Kind: "approval", Key: "key"})
	if event.ID != "" {
		t.Fatalf("stale page published event %#v", event)
	}
	notifier.page = func(string) (string, string, bool, bool) { return "page-1", "other", false, true }
	notifier.added("workspace", "page-1", "session-1", dshinteraction.Interaction{Kind: "approval", Key: "key"})
	if event.Body != "一个工具 正在等待权限决定" {
		t.Fatalf("empty tool body = %q", event.Body)
	}
}

func TestInteractionNotifierRemovesDeliveredNotification(t *testing.T) {
	var removed string
	notifier := &interactionNotifier{remove: func(id string) error { removed = id; return errors.New("ignored") }}
	notifier.removed("page-1", "session-1", dshinteraction.Interaction{Kind: "approval", Key: "approval:3"})
	if removed != "dsh-interaction:page-1:approval:3" {
		t.Fatalf("removed ID = %q", removed)
	}
}

func TestInteractionNotifierRespondRoutesByAction(t *testing.T) {
	baseInfo := map[string]interface{}{"kind": dshInteractionNotificationKind, "instance": "page-1", "sessionId": "session-1", "key": "approval:1"}
	for _, action := range []struct{ identifier, outcome string }{{"allow-once", "allowed-once"}, {"reject", "rejected"}} {
		t.Run(action.identifier, func(t *testing.T) {
			show, opens, sent := 0, 0, 0
			var gotWindow, gotName string
			var gotTTL time.Duration
			var gotData map[string]string
			notifier := &interactionNotifier{
				owner:         func(instance string) (string, bool) { return "workspace-2", instance == "page-1" },
				showWorkspace: func() { show++ },
				open:          func(context.Context, string, string) error { opens++; return nil },
				sendCommand: func(window, name string, ttl time.Duration, data any) {
					sent++
					gotWindow, gotName, gotTTL = window, name, ttl
					gotData = data.(map[string]string)
				},
			}
			if !notifier.respond(wailsnotifications.NotificationResult{Response: wailsnotifications.NotificationResponse{ActionIdentifier: action.identifier, UserInfo: baseInfo}}) {
				t.Fatal("respond() = false, want handled")
			}
			if show != 0 || opens != 0 || sent != 1 || gotWindow != "workspace-2" || gotName != "dsh-answer-approval" || gotTTL != 30*time.Second {
				t.Fatalf("show=%d opens=%d sent=%d window=%q name=%q ttl=%s", show, opens, sent, gotWindow, gotName, gotTTL)
			}
			want := map[string]string{"instance": "page-1", "sessionId": "session-1", "key": "approval:1", "outcome": action.outcome}
			if len(gotData) != len(want) {
				t.Fatalf("command data = %#v, want %#v", gotData, want)
			}
			for key, value := range want {
				if gotData[key] != value {
					t.Fatalf("command data[%q] = %q, want %q", key, gotData[key], value)
				}
			}
		})
	}
}

func TestInteractionNotifierRespondShowsOrOpensOnlyAsSpecified(t *testing.T) {
	interactionInfo := map[string]interface{}{"kind": dshInteractionNotificationKind, "instance": "page-1", "sessionId": "session-1", "key": "key"}
	t.Run("non-interaction", func(t *testing.T) {
		notifier := &interactionNotifier{}
		if notifier.respond(wailsnotifications.NotificationResult{Response: wailsnotifications.NotificationResponse{UserInfo: map[string]interface{}{"kind": "other"}}}) {
			t.Fatal("respond() = true for another notification")
		}
	})
	for _, test := range []struct {
		name string
		info map[string]interface{}
	}{
		{name: "missing instance", info: map[string]interface{}{"kind": dshInteractionNotificationKind, "sessionId": "session-1", "key": "key"}},
		{name: "non-string key", info: map[string]interface{}{"kind": dshInteractionNotificationKind, "instance": "page-1", "sessionId": "session-1", "key": 1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			show, opens, sends := 0, 0, 0
			notifier := &interactionNotifier{
				showWorkspace: func() { show++ },
				owner:         func(string) (string, bool) { return "workspace", true },
				open:          func(context.Context, string, string) error { opens++; return nil },
				sendCommand:   func(string, string, time.Duration, any) { sends++ },
			}
			if !notifier.respond(wailsnotifications.NotificationResult{Response: wailsnotifications.NotificationResponse{UserInfo: test.info}}) || show != 1 || opens != 0 || sends != 0 {
				t.Fatalf("respond effects: show=%d opens=%d sends=%d", show, opens, sends)
			}
		})
	}
	t.Run("unknown owner", func(t *testing.T) {
		show, opens := 0, 0
		notifier := &interactionNotifier{
			owner:         func(string) (string, bool) { return "", false },
			showWorkspace: func() { show++ },
			open:          func(context.Context, string, string) error { opens++; return nil },
		}
		if !notifier.respond(wailsnotifications.NotificationResult{Response: wailsnotifications.NotificationResponse{UserInfo: interactionInfo}}) || show != 1 || opens != 0 {
			t.Fatalf("unknown owner effects: show=%d opens=%d", show, opens)
		}
	})
	t.Run("body click", func(t *testing.T) {
		show, opens := 0, 0
		var gotSession, gotWindow string
		notifier := &interactionNotifier{
			owner:         func(string) (string, bool) { return "workspace-2", true },
			showWorkspace: func() { show++ },
			open: func(_ context.Context, sessionID, window string) error {
				opens++
				gotSession, gotWindow = sessionID, window
				return nil
			},
		}
		result := wailsnotifications.NotificationResult{Response: wailsnotifications.NotificationResponse{ActionIdentifier: wailsnotifications.DefaultActionIdentifier, UserInfo: interactionInfo}}
		if !notifier.respond(result) || show != 0 || opens != 1 || gotSession != "session-1" || gotWindow != "workspace-2" {
			t.Fatalf("body click effects: show=%d opens=%d session=%q window=%q", show, opens, gotSession, gotWindow)
		}
	})
}

func TestDSHApprovalNotificationCategoryUsesLocalizedActions(t *testing.T) {
	for _, locale := range []settings.Locale{settings.LocaleChinese, settings.LocaleEnglish, settings.LocaleJapanese} {
		category := dshApprovalNotificationCategory(locale)
		labels := nativeui.LabelsFor(locale)
		if category.ID != "dsh-approval" || len(category.Actions) != 2 || category.Actions[0].ID != "allow-once" || category.Actions[0].Title != labels.ApprovalAllowOnce || category.Actions[1].ID != "reject" || category.Actions[1].Title != labels.ApprovalReject {
			t.Fatalf("category(%s) = %#v", locale, category)
		}
	}
}
