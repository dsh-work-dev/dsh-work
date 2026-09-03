package main

import (
	"strings"

	"github.com/local/work/internal/lifecycle"
	worksettings "github.com/local/work/internal/settings"
)

type nativeLocaleCopy struct {
	settings              string
	openWorkspace         string
	help                  string
	checkUpdates          string
	about                 string
	restartDSH            string
	quit                  string
	trayTooltip           string
	trayStatus            string
	updateTitle           string
	updateMessage         string
	aboutTitle            string
	aboutMessage          string
	workspaceFailureTitle string
	workspaceFailureBody  string
	lifecycleTitles       map[lifecycle.State]string
	lifecycleBodies       map[lifecycle.State]string
	stateLabels           map[lifecycle.State]string
}

var nativeLocaleCopies = map[worksettings.Locale]nativeLocaleCopy{
	worksettings.LocaleEnglish: {
		settings:              "Settings",
		openWorkspace:         "Open workspace",
		help:                  "Help",
		checkUpdates:          "Check for Updates…",
		about:                 "About Work",
		restartDSH:            "Restart DSH",
		quit:                  "Quit DSH Work",
		trayTooltip:           "DSH Work",
		trayStatus:            "DSH: {state}",
		updateTitle:           "Check for Updates",
		updateMessage:         "Update checking is unavailable.",
		aboutTitle:            "About Work",
		aboutMessage:          "Work\n\nA local desktop host for DeepSeek Harness.",
		workspaceFailureTitle: "Workspace needs attention",
		workspaceFailureBody:  "Open Work to review the DSH workspace.",
		lifecycleTitles: map[lifecycle.State]string{
			lifecycle.StateStarting: "DSH is starting",
			lifecycle.StateReady:    "DSH is ready",
			lifecycle.StateStopping: "DSH is stopping",
			lifecycle.StateStopped:  "DSH stopped",
		},
		lifecycleBodies: map[lifecycle.State]string{
			lifecycle.StateStarting: "Work is starting the DSH workspace.",
			lifecycle.StateReady:    "The DSH workspace is ready.",
			lifecycle.StateStopping: "Work is stopping the DSH workspace.",
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
	worksettings.LocaleChinese: {
		settings:              "设置",
		openWorkspace:         "打开工作区",
		help:                  "帮助",
		checkUpdates:          "检查更新…",
		about:                 "关于 Work",
		restartDSH:            "重启 DSH",
		quit:                  "退出 DSH Work",
		trayTooltip:           "DSH Work",
		trayStatus:            "DSH：{state}",
		updateTitle:           "检查更新",
		updateMessage:         "暂不支持检查更新。",
		aboutTitle:            "关于 Work",
		aboutMessage:          "Work\n\nDeepSeek Harness 的本地主机。",
		workspaceFailureTitle: "工作区需要处理",
		workspaceFailureBody:  "打开 Work 查看 DSH 工作区。",
		lifecycleTitles: map[lifecycle.State]string{
			lifecycle.StateStarting: "DSH 正在启动",
			lifecycle.StateReady:    "DSH 已就绪",
			lifecycle.StateStopping: "DSH 正在停止",
			lifecycle.StateStopped:  "DSH 已停止",
		},
		lifecycleBodies: map[lifecycle.State]string{
			lifecycle.StateStarting: "Work 正在启动 DSH 工作区。",
			lifecycle.StateReady:    "DSH 工作区已就绪。",
			lifecycle.StateStopping: "Work 正在停止 DSH 工作区。",
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
	worksettings.LocaleJapanese: {
		settings:              "設定",
		openWorkspace:         "ワークスペースを開く",
		help:                  "ヘルプ",
		checkUpdates:          "更新を確認…",
		about:                 "Work について",
		restartDSH:            "DSH を再起動",
		quit:                  "DSH Work を終了",
		trayTooltip:           "DSH Work",
		trayStatus:            "DSH：{state}",
		updateTitle:           "更新を確認",
		updateMessage:         "更新確認は利用できません。",
		aboutTitle:            "Work について",
		aboutMessage:          "Work\n\nDeepSeek Harness のローカルデスクトップホストです。",
		workspaceFailureTitle: "ワークスペースを確認してください",
		workspaceFailureBody:  "Work を開いて DSH ワークスペースを確認してください。",
		lifecycleTitles: map[lifecycle.State]string{
			lifecycle.StateStarting: "DSH を起動しています",
			lifecycle.StateReady:    "DSH の準備が完了",
			lifecycle.StateStopping: "DSH を停止しています",
			lifecycle.StateStopped:  "DSH を停止しました",
		},
		lifecycleBodies: map[lifecycle.State]string{
			lifecycle.StateStarting: "Work が DSH ワークスペースを起動しています。",
			lifecycle.StateReady:    "DSH ワークスペースの準備ができました。",
			lifecycle.StateStopping: "Work が DSH ワークスペースを停止しています。",
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

func nativeLocaleCopyFor(locale worksettings.Locale) nativeLocaleCopy {
	if value, ok := nativeLocaleCopies[locale]; ok {
		return value
	}
	return nativeLocaleCopies[worksettings.DefaultLocale]
}

func nativeTrayStatus(locale worksettings.Locale, state lifecycle.State) string {
	copy := nativeLocaleCopyFor(locale)
	stateLabel, ok := copy.stateLabels[state]
	if !ok {
		stateLabel = string(state)
	}
	return strings.Replace(copy.trayStatus, "{state}", stateLabel, 1)
}
