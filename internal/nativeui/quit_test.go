package nativeui

import (
	"github.com/local/dsh-work/internal/settings"
	"testing"
)

func TestQuitConfirmationMessage(t *testing.T) {
	for _, tc := range []struct {
		locale   settings.Locale
		question string
		details  [3]string
	}{
		{settings.LocaleChinese, "退出 DSH Work？", [3]string{"当前正在运行的任务将会中断", "应用关闭期间，定时任务不会运行", "当前正在运行的任务将会中断，且应用关闭期间，定时任务不会运行"}},
		{settings.LocaleEnglish, "Quit DSH Work?", [3]string{"Running tasks will be interrupted.", "Scheduled tasks will not run while the app is closed.", "Running tasks will be interrupted, and scheduled tasks will not run while the app is closed."}},
		{settings.LocaleJapanese, "DSH Work を終了しますか？", [3]string{"実行中のタスクは中断されます。", "アプリを閉じている間、スケジュールされたタスクは実行されません。", "実行中のタスクは中断され、アプリを閉じている間、スケジュールされたタスクは実行されません。"}},
	} {
		for i, flags := range [][2]bool{{true, false}, {false, true}, {true, true}} {
			got := QuitConfirmationMessage(tc.locale, flags[0], flags[1])
			if got != tc.question+"\n"+tc.details[i] {
				t.Fatalf("locale=%s flags=%v message=%q", tc.locale, flags, got)
			}
		}
	}
}
