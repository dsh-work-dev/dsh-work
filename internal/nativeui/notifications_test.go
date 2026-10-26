package nativeui

import (
	"context"
	"errors"
	"testing"

	"github.com/local/dsh-work/internal/lifecycle"
	"github.com/local/dsh-work/internal/notifications"
	"github.com/local/dsh-work/internal/settings"
	wailsnotifications "github.com/wailsapp/wails/v3/pkg/services/notifications"
)

func TestNotificationDeliveryUsesStableFailure(t *testing.T) {
	err := NewNotificationDelivery(nil, nil).Send(context.Background(), notifications.Event{
		ID:    "event-1",
		Class: notifications.ClassError,
		Title: "Error",
	})
	var failure lifecycle.Failure
	if !errors.As(err, &failure) {
		t.Fatalf("Send() error = %v, want lifecycle failure", err)
	}
	if failure.Code != lifecycle.ErrorNotificationDeliveryFailed || failure.Detail == "" {
		t.Fatalf("failure = %#v, want stable code and actionable detail", failure)
	}
}

type recordingNotificationSender struct {
	basic   []wailsnotifications.NotificationOptions
	actions []wailsnotifications.NotificationOptions
	err     error
}

func (s *recordingNotificationSender) SendNotification(options wailsnotifications.NotificationOptions) error {
	s.basic = append(s.basic, options)
	return s.err
}

func (s *recordingNotificationSender) SendNotificationWithActions(options wailsnotifications.NotificationOptions) error {
	s.actions = append(s.actions, options)
	return s.err
}

func TestNotificationDeliverySelectsActionsAndMergesData(t *testing.T) {
	sender := &recordingNotificationSender{}
	delivery := notificationDelivery{service: sender, host: &NotificationService{}}
	withActions := notifications.Event{
		ID: "approval", Title: "Approval required", Body: "A tool is waiting", Target: "workspace",
		CategoryID: "dsh-approval", Data: map[string]string{"kind": "dsh-interaction", "instance": "page-1"},
	}
	if err := delivery.Send(context.Background(), withActions); err != nil {
		t.Fatalf("Send(with actions) error = %v", err)
	}
	if len(sender.actions) != 1 || len(sender.basic) != 0 {
		t.Fatalf("notification sends = basic %d, actions %d; want actions only", len(sender.basic), len(sender.actions))
	}
	got := sender.actions[0]
	if got.CategoryID != "dsh-approval" || got.Data["target"] != "workspace" || got.Data["kind"] != "dsh-interaction" || got.Data["instance"] != "page-1" {
		t.Fatalf("action notification = %#v, want category and merged user data", got)
	}

	withoutActions := notifications.Event{ID: "question", Title: "Answer needed", Target: "workspace-2", Data: map[string]string{"kind": "dsh-interaction"}}
	if err := delivery.Send(context.Background(), withoutActions); err != nil {
		t.Fatalf("Send(without actions) error = %v", err)
	}
	if len(sender.basic) != 1 || sender.basic[0].CategoryID != "" || sender.basic[0].Data["target"] != "workspace-2" || sender.basic[0].Data["kind"] != "dsh-interaction" {
		t.Fatalf("basic notification = %#v, want merged data without a category", sender.basic)
	}
}

func TestDSHInteractionNotificationCopy(t *testing.T) {
	tests := []struct {
		locale    settings.Locale
		approval  NotificationCopy
		answer    NotificationCopy
		allowOnce string
		reject    string
	}{
		{settings.LocaleChinese, NotificationCopy{Title: "需要批准", Body: "一个工具 正在等待权限决定"}, NotificationCopy{Title: "需要回答", Body: "一个会话正在等待你的回答"}, "允许一次", "拒绝"},
		{settings.LocaleEnglish, NotificationCopy{Title: "Approval required", Body: "A tool is waiting for a permission decision"}, NotificationCopy{Title: "Answer needed", Body: "A conversation is waiting for your answer"}, "Allow once", "Reject"},
		{settings.LocaleJapanese, NotificationCopy{Title: "承認が必要です", Body: "ツール が権限の判断を待っています"}, NotificationCopy{Title: "回答が必要です", Body: "会話が回答を待っています"}, "一度だけ許可", "拒否"},
	}
	for _, test := range tests {
		t.Run(string(test.locale), func(t *testing.T) {
			if got := DSHApprovalNotification(test.locale, ""); got != test.approval {
				t.Fatalf("DSHApprovalNotification(empty tool) = %#v, want %#v", got, test.approval)
			}
			if got := DSHAnswerNotification(test.locale); got != test.answer {
				t.Fatalf("DSHAnswerNotification() = %#v, want %#v", got, test.answer)
			}
			labels := LabelsFor(test.locale)
			if labels.ApprovalAllowOnce != test.allowOnce || labels.ApprovalReject != test.reject {
				t.Fatalf("approval actions = %q / %q, want %q / %q", labels.ApprovalAllowOnce, labels.ApprovalReject, test.allowOnce, test.reject)
			}
		})
	}
	if got := DSHApprovalNotification(settings.LocaleEnglish, "shell.exec"); got.Body != "shell.exec is waiting for a permission decision" {
		t.Fatalf("DSHApprovalNotification(tool) body = %q", got.Body)
	}
}
