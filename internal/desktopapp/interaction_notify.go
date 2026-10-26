package desktopapp

import (
	"context"
	"time"

	"github.com/local/dsh-work/internal/dshinteraction"
	"github.com/local/dsh-work/internal/logging"
	"github.com/local/dsh-work/internal/nativeui"
	"github.com/local/dsh-work/internal/notifications"
	"github.com/local/dsh-work/internal/settings"
	wailsnotifications "github.com/wailsapp/wails/v3/pkg/services/notifications"
)

const (
	dshInteractionNotificationKind = "dsh-interaction"
	dshApprovalCategoryID          = "dsh-approval"
	dshAnswerApprovalCommand       = "dsh-answer-approval"
)

type interactionNotifier struct {
	page          func(surface string) (instance, current string, visible bool, ok bool)
	focused       func(surface string) bool
	preferences   func() notifications.Preferences
	publish       func(context.Context, notifications.Event) error
	remove        func(id string) error
	locale        func() settings.Locale
	owner         func(instance string) (surface string, ok bool)
	sendCommand   func(window, name string, ttl time.Duration, data any)
	open          func(context.Context, string, string) error
	showWorkspace func()
}

func (n *interactionNotifier) added(surface, instance, sessionID string, it dshinteraction.Interaction) {
	if n == nil || n.page == nil || n.focused == nil || n.preferences == nil || n.publish == nil || n.locale == nil {
		return
	}
	pageInstance, current, visible, ok := n.page(surface)
	if !ok || pageInstance != instance || !n.preferences().Allows(notifications.ClassDSHInteraction) {
		return
	}
	if n.focused(surface) && visible && current == sessionID {
		return
	}

	var copy nativeui.NotificationCopy
	categoryID := ""
	switch it.Kind {
	case "approval":
		copy = nativeui.DSHApprovalNotification(n.locale(), it.ToolName)
		categoryID = dshApprovalCategoryID
	case "question", "plan-review":
		copy = nativeui.DSHAnswerNotification(n.locale())
	default:
		return
	}
	event := notifications.Event{
		ID:                        "dsh-interaction:" + instance + ":" + it.Key,
		Class:                     notifications.ClassDSHInteraction,
		Title:                     copy.Title,
		Body:                      copy.Body,
		CategoryID:                categoryID,
		Data:                      map[string]string{"kind": dshInteractionNotificationKind, "instance": instance, "sessionId": sessionID, "key": it.Key},
		AllowWhenWorkspaceFocused: true,
	}
	if err := n.publish(context.Background(), event); err != nil {
		logging.Module(logging.ModNotify).Warn("interaction notification failed", logging.ErrAttrs(err, "")...)
	}
}

func (n *interactionNotifier) removed(instance, sessionID string, it dshinteraction.Interaction) {
	if n == nil || n.remove == nil {
		return
	}
	_ = n.remove("dsh-interaction:" + instance + ":" + it.Key)
}

func (n *interactionNotifier) respond(result wailsnotifications.NotificationResult) bool {
	info := result.Response.UserInfo
	kind := ""
	if info != nil {
		kind, _ = info["kind"].(string)
	}
	logging.Module(logging.ModNotify).Debug("interaction notification response", "action", result.Response.ActionIdentifier, "kind", kind)
	if info == nil || info["kind"] != dshInteractionNotificationKind {
		return false
	}
	instance, instanceOK := notificationString(info, "instance")
	sessionID, sessionOK := notificationString(info, "sessionId")
	key, keyOK := notificationString(info, "key")
	if !instanceOK || !sessionOK || !keyOK {
		n.displayWorkspace()
		return true
	}
	if n.owner == nil {
		n.displayWorkspace()
		return true
	}
	window, ok := n.owner(instance)
	if !ok {
		n.displayWorkspace()
		return true
	}
	switch result.Response.ActionIdentifier {
	case "allow-once", "reject":
		if n.sendCommand != nil {
			outcome := "allowed-once"
			if result.Response.ActionIdentifier == "reject" {
				outcome = "rejected"
			}
			n.sendCommand(window, dshAnswerApprovalCommand, 30*time.Second, map[string]string{
				"instance": instance, "sessionId": sessionID, "key": key, "outcome": outcome,
			})
		}
	default:
		if n.open != nil {
			if err := n.open(context.Background(), sessionID, window); err != nil {
				logging.Module(logging.ModNotify).Warn("interaction conversation could not be opened", logging.ErrAttrs(err, "")...)
			}
		}
	}
	return true
}

func (n *interactionNotifier) displayWorkspace() {
	if n != nil && n.showWorkspace != nil {
		n.showWorkspace()
	}
}

func notificationString(info map[string]interface{}, key string) (string, bool) {
	value, ok := info[key].(string)
	return value, ok && value != ""
}

func dshApprovalNotificationCategory(locale settings.Locale) wailsnotifications.NotificationCategory {
	labels := nativeui.LabelsFor(locale)
	return wailsnotifications.NotificationCategory{
		ID: dshApprovalCategoryID,
		Actions: []wailsnotifications.NotificationAction{
			{ID: "allow-once", Title: labels.ApprovalAllowOnce},
			{ID: "reject", Title: labels.ApprovalReject},
		},
	}
}
