package nativeui

import (
	"strings"

	"github.com/local/dsh-work/internal/lifecycle"
	"github.com/local/dsh-work/internal/settings"
)

// Labels contains user-facing copy owned by the native desktop shell.
type Labels struct {
	BackgroundNotice      string
	QuitQuestion          string
	QuitActiveTasks       string
	QuitScheduledTasks    string
	QuitBothTasks         string
	Actions               string
	Settings              string
	ShowPet               string
	OpenWorkspace         string
	Help                  string
	CheckUpdates          string
	About                 string
	RestartDSH            string
	Quit                  string
	TrayTooltip           string
	UpdateAvailableMenu   string
	UpdateInProgressMenu  string
	UpdateReadyTitle      string
	UpdateReadyMessage    string
	ApprovalRequiredTitle string
	ApprovalRequiredBody  string
	AnswerNeededTitle     string
	AnswerNeededBody      string
	ApprovalAllowOnce     string
	ApprovalReject        string
	PetMenuErrorTitle     string
	PetMenuErrorMessage   string

	trayStatus            string
	workspaceFailureTitle string
	workspaceFailureBody  string
	lifecycleTitles       map[lifecycle.State]string
	lifecycleBodies       map[lifecycle.State]string
	stateLabels           map[lifecycle.State]string
	genericToolName       string
}

// NotificationCopy is localized native-notification content.
type NotificationCopy struct {
	Title string
	Body  string
}

var labelsByLocale = map[settings.Locale]Labels{
	settings.LocaleEnglish: {
		BackgroundNotice:      "Running tasks will continue. You can reopen the window from the system tray.",
		QuitQuestion:          "Quit DSH Work?",
		QuitActiveTasks:       "Running tasks will be interrupted.",
		QuitScheduledTasks:    "Scheduled tasks will not run while the app is closed.",
		QuitBothTasks:         "Running tasks will be interrupted, and scheduled tasks will not run while the app is closed.",
		Actions:               "Actions",
		Settings:              "Settings",
		ShowPet:               "Show desktop pet",
		OpenWorkspace:         "Open workspace",
		Help:                  "Help",
		CheckUpdates:          "Check for Updates…",
		About:                 "About DSH Work",
		RestartDSH:            "Restart DSH",
		Quit:                  "Stop background and exit",
		TrayTooltip:           "DSH Work",
		trayStatus:            "DSH: {state}",
		UpdateAvailableMenu:   "Update ready to install…",
		UpdateInProgressMenu:  "Updating…",
		UpdateReadyTitle:      "Update ready to install",
		UpdateReadyMessage:    "Version %s is ready. Open About to install it.",
		ApprovalRequiredTitle: "Approval required",
		ApprovalRequiredBody:  "%s is waiting for a permission decision",
		AnswerNeededTitle:     "Answer needed",
		AnswerNeededBody:      "A conversation is waiting for your answer",
		ApprovalAllowOnce:     "Allow once",
		ApprovalReject:        "Reject",
		genericToolName:       "A tool",
		PetMenuErrorTitle:     "Desktop pet unavailable",
		PetMenuErrorMessage:   "The desktop pet could not be updated. Review Pet settings and try again.",
		workspaceFailureTitle: "Workspace needs attention",
		workspaceFailureBody:  "Open DSH Work to review the DSH workspace.",
		lifecycleTitles: map[lifecycle.State]string{
			lifecycle.StateStarting: "DSH is starting",
			lifecycle.StateReady:    "DSH is ready",
			lifecycle.StateStopping: "DSH is stopping",
			lifecycle.StateStopped:  "DSH stopped",
		},
		lifecycleBodies: map[lifecycle.State]string{
			lifecycle.StateStarting: "DSH Work is starting the DSH workspace.",
			lifecycle.StateReady:    "The DSH workspace is ready.",
			lifecycle.StateStopping: "DSH Work is stopping the DSH workspace.",
			lifecycle.StateStopped:  "The DSH workspace is stopped.",
		},
		stateLabels: map[lifecycle.State]string{
			lifecycle.StateStarting: "Starting",
			lifecycle.StateReady:    "Ready",
			lifecycle.StateStopping: "Stopping",
			lifecycle.StateStopped:  "Stopped",
			lifecycle.StateFailed:   "Needs attention",
		},
	},
	settings.LocaleChinese: {
		BackgroundNotice:      "正在运行的任务不会中断，可在系统托盘中重新打开窗口",
		QuitQuestion:          "退出 DSH Work？",
		QuitActiveTasks:       "当前正在运行的任务将会中断",
		QuitScheduledTasks:    "应用关闭期间，定时任务不会运行",
		QuitBothTasks:         "当前正在运行的任务将会中断，且应用关闭期间，定时任务不会运行",
		Actions:               "操作",
		Settings:              "设置",
		ShowPet:               "显示桌面宠物",
		OpenWorkspace:         "打开工作区",
		Help:                  "帮助",
		CheckUpdates:          "检查更新…",
		About:                 "关于 DSH Work",
		RestartDSH:            "重启 DSH",
		Quit:                  "停止后台并退出",
		TrayTooltip:           "DSH Work",
		trayStatus:            "DSH：{state}",
		UpdateAvailableMenu:   "有新版本可安装…",
		UpdateInProgressMenu:  "更新中…",
		UpdateReadyTitle:      "新版本可以安装",
		UpdateReadyMessage:    "版本 %s 已准备好。打开关于页面进行安装。",
		ApprovalRequiredTitle: "需要批准",
		ApprovalRequiredBody:  "%s 正在等待权限决定",
		AnswerNeededTitle:     "需要回答",
		AnswerNeededBody:      "一个会话正在等待你的回答",
		ApprovalAllowOnce:     "允许一次",
		ApprovalReject:        "拒绝",
		genericToolName:       "一个工具",
		PetMenuErrorTitle:     "桌面宠物不可用",
		PetMenuErrorMessage:   "无法更新桌面宠物。请打开宠物设置再试。",
		workspaceFailureTitle: "工作区需要处理",
		workspaceFailureBody:  "打开 DSH Work 查看 DSH 工作区。",
		lifecycleTitles: map[lifecycle.State]string{
			lifecycle.StateStarting: "DSH 正在启动",
			lifecycle.StateReady:    "DSH 已就绪",
			lifecycle.StateStopping: "DSH 正在停止",
			lifecycle.StateStopped:  "DSH 已停止",
		},
		lifecycleBodies: map[lifecycle.State]string{
			lifecycle.StateStarting: "DSH Work 正在启动 DSH 工作区。",
			lifecycle.StateReady:    "DSH 工作区已就绪。",
			lifecycle.StateStopping: "DSH Work 正在停止 DSH 工作区。",
			lifecycle.StateStopped:  "DSH 工作区已停止。",
		},
		stateLabels: map[lifecycle.State]string{
			lifecycle.StateStarting: "启动中",
			lifecycle.StateReady:    "已就绪",
			lifecycle.StateStopping: "停止中",
			lifecycle.StateStopped:  "已停止",
			lifecycle.StateFailed:   "需要处理",
		},
	},
	settings.LocaleJapanese: {
		BackgroundNotice:      "実行中のタスクは続行されます。システムトレイからウィンドウを再び開けます。",
		QuitQuestion:          "DSH Work を終了しますか？",
		QuitActiveTasks:       "実行中のタスクは中断されます。",
		QuitScheduledTasks:    "アプリを閉じている間、スケジュールされたタスクは実行されません。",
		QuitBothTasks:         "実行中のタスクは中断され、アプリを閉じている間、スケジュールされたタスクは実行されません。",
		Actions:               "操作",
		Settings:              "設定",
		ShowPet:               "デスクトップペットを表示",
		OpenWorkspace:         "ワークスペースを開く",
		Help:                  "ヘルプ",
		CheckUpdates:          "更新を確認…",
		About:                 "DSH Work について",
		RestartDSH:            "DSH を再起動",
		Quit:                  "バックグラウンドを停止して終了",
		TrayTooltip:           "DSH Work",
		trayStatus:            "DSH：{state}",
		UpdateAvailableMenu:   "インストールできる更新があります…",
		UpdateInProgressMenu:  "更新中…",
		UpdateReadyTitle:      "更新をインストールできます",
		UpdateReadyMessage:    "バージョン %s の準備ができました。概要を開いてインストールしてください。",
		ApprovalRequiredTitle: "承認が必要です",
		ApprovalRequiredBody:  "%s が権限の判断を待っています",
		AnswerNeededTitle:     "回答が必要です",
		AnswerNeededBody:      "会話が回答を待っています",
		ApprovalAllowOnce:     "一度だけ許可",
		ApprovalReject:        "拒否",
		genericToolName:       "ツール",
		PetMenuErrorTitle:     "デスクトップペットを利用できません",
		PetMenuErrorMessage:   "デスクトップペットを更新できませんでした。ペット設定を確認して、もう一度お試しください。",
		workspaceFailureTitle: "ワークスペースを確認してください",
		workspaceFailureBody:  "DSH Work を開いて DSH ワークスペースを確認してください。",
		lifecycleTitles: map[lifecycle.State]string{
			lifecycle.StateStarting: "DSH を起動しています",
			lifecycle.StateReady:    "DSH の準備が完了",
			lifecycle.StateStopping: "DSH を停止しています",
			lifecycle.StateStopped:  "DSH を停止しました",
		},
		lifecycleBodies: map[lifecycle.State]string{
			lifecycle.StateStarting: "DSH Work が DSH ワークスペースを起動しています。",
			lifecycle.StateReady:    "DSH ワークスペースの準備ができました。",
			lifecycle.StateStopping: "DSH Work が DSH ワークスペースを停止しています。",
			lifecycle.StateStopped:  "DSH ワークスペースは停止しています。",
		},
		stateLabels: map[lifecycle.State]string{
			lifecycle.StateStarting: "起動中",
			lifecycle.StateReady:    "準備完了",
			lifecycle.StateStopping: "停止中",
			lifecycle.StateStopped:  "停止済み",
			lifecycle.StateFailed:   "確認が必要",
		},
	},
}

func LabelsFor(locale settings.Locale) Labels {
	if value, ok := labelsByLocale[locale]; ok {
		return value
	}
	return labelsByLocale[settings.DefaultLocale]
}

func TrayStatus(locale settings.Locale, state lifecycle.State) string {
	labels := LabelsFor(locale)
	stateLabel, ok := labels.stateLabels[state]
	if !ok {
		stateLabel = string(state)
	}
	return strings.Replace(labels.trayStatus, "{state}", stateLabel, 1)
}

func FailureNotification(locale settings.Locale) NotificationCopy {
	labels := LabelsFor(locale)
	return NotificationCopy{Title: labels.workspaceFailureTitle, Body: labels.workspaceFailureBody}
}

func LifecycleNotification(locale settings.Locale, state lifecycle.State) NotificationCopy {
	labels := LabelsFor(locale)
	return NotificationCopy{Title: labels.lifecycleTitles[state], Body: labels.lifecycleBodies[state]}
}

func UpdateReadyNotification(locale settings.Locale, targetVersion string) NotificationCopy {
	labels := LabelsFor(locale)
	return NotificationCopy{Title: labels.UpdateReadyTitle, Body: strings.Replace(labels.UpdateReadyMessage, "%s", targetVersion, 1)}
}

func DSHApprovalNotification(locale settings.Locale, toolName string) NotificationCopy {
	labels := LabelsFor(locale)
	if toolName == "" {
		toolName = labels.genericToolName
	}
	return NotificationCopy{Title: labels.ApprovalRequiredTitle, Body: strings.Replace(labels.ApprovalRequiredBody, "%s", toolName, 1)}
}

func DSHAnswerNotification(locale settings.Locale) NotificationCopy {
	labels := LabelsFor(locale)
	return NotificationCopy{Title: labels.AnswerNeededTitle, Body: labels.AnswerNeededBody}
}

// QuitConfirmationMessage selects only the task consequences that apply.
func QuitConfirmationMessage(locale settings.Locale, active, scheduled bool) string {
	labels := LabelsFor(locale)
	detail := labels.QuitActiveTasks
	if active && scheduled {
		detail = labels.QuitBothTasks
	} else if scheduled {
		detail = labels.QuitScheduledTasks
	}
	return labels.QuitQuestion + "\n" + detail
}
