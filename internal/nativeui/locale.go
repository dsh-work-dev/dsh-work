package nativeui

import (
	"strings"

	"github.com/local/dsh-work/internal/lifecycle"
	"github.com/local/dsh-work/internal/settings"
)

// Labels contains user-facing copy owned by the native desktop shell.
type Labels struct {
	Actions                string
	Settings               string
	ShowPet                string
	OpenWorkspace          string
	Help                   string
	CheckUpdates           string
	About                  string
	RestartDSH             string
	Quit                   string
	TrayTooltip            string
	UpdateTitle            string
	UpdateMessage          string
	UpdateAvailableTitle   string
	UpdateAvailableMessage string
	UpdateNow              string
	UpdateCancel           string
	UpdateNoUpdateMessage  string
	UpdateBusyMessage      string
	UpdateFailureTitle     string
	UpdateFailureMessage   string
	PetMenuErrorTitle      string
	PetMenuErrorMessage    string

	trayStatus            string
	workspaceFailureTitle string
	workspaceFailureBody  string
	lifecycleTitles       map[lifecycle.State]string
	lifecycleBodies       map[lifecycle.State]string
	stateLabels           map[lifecycle.State]string
}

// NotificationCopy is localized native-notification content.
type NotificationCopy struct {
	Title string
	Body  string
}

var labelsByLocale = map[settings.Locale]Labels{
	settings.LocaleEnglish: {
		Actions:                "Actions",
		Settings:               "Settings",
		ShowPet:                "Show desktop pet",
		OpenWorkspace:          "Open workspace",
		Help:                   "Help",
		CheckUpdates:           "Check for Updates…",
		About:                  "About dsh-work",
		RestartDSH:             "Restart DSH",
		Quit:                   "Stop background and exit",
		TrayTooltip:            "dsh-work",
		trayStatus:             "DSH: {state}",
		UpdateTitle:            "Check for Updates",
		UpdateMessage:          "Update checking is unavailable.",
		UpdateAvailableTitle:   "Update available",
		UpdateAvailableMessage: "Version %s is downloading to a temporary location. Update now or cancel?",
		UpdateNow:              "Update",
		UpdateCancel:           "Cancel",
		UpdateNoUpdateMessage:  "dsh-work is up to date.",
		UpdateBusyMessage:      "An update is already in progress.",
		UpdateFailureTitle:     "Update failed",
		UpdateFailureMessage:   "The update could not be completed. Try again later.",
		PetMenuErrorTitle:      "Desktop pet unavailable",
		PetMenuErrorMessage:    "The desktop pet could not be updated. Review Pet settings and try again.",
		workspaceFailureTitle:  "Workspace needs attention",
		workspaceFailureBody:   "Open dsh-work to review the DSH workspace.",
		lifecycleTitles: map[lifecycle.State]string{
			lifecycle.StateStarting: "DSH is starting",
			lifecycle.StateReady:    "DSH is ready",
			lifecycle.StateStopping: "DSH is stopping",
			lifecycle.StateStopped:  "DSH stopped",
		},
		lifecycleBodies: map[lifecycle.State]string{
			lifecycle.StateStarting: "dsh-work is starting the DSH workspace.",
			lifecycle.StateReady:    "The DSH workspace is ready.",
			lifecycle.StateStopping: "dsh-work is stopping the DSH workspace.",
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
		Actions:                "操作",
		Settings:               "设置",
		ShowPet:                "显示桌面宠物",
		OpenWorkspace:          "打开工作区",
		Help:                   "帮助",
		CheckUpdates:           "检查更新…",
		About:                  "关于 dsh-work",
		RestartDSH:             "重启 DSH",
		Quit:                   "停止后台并退出",
		TrayTooltip:            "dsh-work",
		trayStatus:             "DSH：{state}",
		UpdateTitle:            "检查更新",
		UpdateMessage:          "暂不支持检查更新。",
		UpdateAvailableTitle:   "发现新版本",
		UpdateAvailableMessage: "版本 %s 正在下载到临时目录。现在更新还是取消？",
		UpdateNow:              "更新",
		UpdateCancel:           "取消",
		UpdateNoUpdateMessage:  "dsh-work 已是最新版本。",
		UpdateBusyMessage:      "已有更新正在进行。",
		UpdateFailureTitle:     "更新失败",
		UpdateFailureMessage:   "更新未完成，请稍后重试。",
		PetMenuErrorTitle:      "桌面宠物不可用",
		PetMenuErrorMessage:    "无法更新桌面宠物。请打开宠物设置再试。",
		workspaceFailureTitle:  "工作区需要处理",
		workspaceFailureBody:   "打开 dsh-work 查看 DSH 工作区。",
		lifecycleTitles: map[lifecycle.State]string{
			lifecycle.StateStarting: "DSH 正在启动",
			lifecycle.StateReady:    "DSH 已就绪",
			lifecycle.StateStopping: "DSH 正在停止",
			lifecycle.StateStopped:  "DSH 已停止",
		},
		lifecycleBodies: map[lifecycle.State]string{
			lifecycle.StateStarting: "dsh-work 正在启动 DSH 工作区。",
			lifecycle.StateReady:    "DSH 工作区已就绪。",
			lifecycle.StateStopping: "dsh-work 正在停止 DSH 工作区。",
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
		Actions:                "操作",
		Settings:               "設定",
		ShowPet:                "デスクトップペットを表示",
		OpenWorkspace:          "ワークスペースを開く",
		Help:                   "ヘルプ",
		CheckUpdates:           "更新を確認…",
		About:                  "dsh-work について",
		RestartDSH:             "DSH を再起動",
		Quit:                   "バックグラウンドを停止して終了",
		TrayTooltip:            "dsh-work",
		trayStatus:             "DSH：{state}",
		UpdateTitle:            "更新を確認",
		UpdateMessage:          "更新確認は利用できません。",
		UpdateAvailableTitle:   "更新があります",
		UpdateAvailableMessage: "バージョン %s を一時フォルダーにダウンロードしています。更新しますか、それともキャンセルしますか？",
		UpdateNow:              "更新",
		UpdateCancel:           "キャンセル",
		UpdateNoUpdateMessage:  "dsh-work は最新です。",
		UpdateBusyMessage:      "別の更新が進行中です。",
		UpdateFailureTitle:     "更新に失敗しました",
		UpdateFailureMessage:   "更新を完了できませんでした。しばらくしてからもう一度お試しください。",
		PetMenuErrorTitle:      "デスクトップペットを利用できません",
		PetMenuErrorMessage:    "デスクトップペットを更新できませんでした。ペット設定を確認して、もう一度お試しください。",
		workspaceFailureTitle:  "ワークスペースを確認してください",
		workspaceFailureBody:   "dsh-work を開いて DSH ワークスペースを確認してください。",
		lifecycleTitles: map[lifecycle.State]string{
			lifecycle.StateStarting: "DSH を起動しています",
			lifecycle.StateReady:    "DSH の準備が完了",
			lifecycle.StateStopping: "DSH を停止しています",
			lifecycle.StateStopped:  "DSH を停止しました",
		},
		lifecycleBodies: map[lifecycle.State]string{
			lifecycle.StateStarting: "dsh-work が DSH ワークスペースを起動しています。",
			lifecycle.StateReady:    "DSH ワークスペースの準備ができました。",
			lifecycle.StateStopping: "dsh-work が DSH ワークスペースを停止しています。",
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
