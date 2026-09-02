package main

import (
	"strings"

	"github.com/local/work/internal/lifecycle"
	worksettings "github.com/local/work/internal/settings"
)

type nativeLocaleCopy struct {
	settings      string
	openWorkspace string
	help          string
	checkUpdates  string
	about         string
	restartDSH    string
	quit          string
	trayTooltip   string
	trayStatus    string
	updateTitle   string
	updateMessage string
	aboutTitle    string
	aboutMessage  string
	stateLabels   map[lifecycle.State]string
}

var nativeLocaleCopies = map[worksettings.Locale]nativeLocaleCopy{
	worksettings.LocaleEnglish: {
		settings:      "Settings",
		openWorkspace: "Open workspace",
		help:          "Help",
		checkUpdates:  "Check for Updates…",
		about:         "About Work",
		restartDSH:    "Restart DSH",
		quit:          "Quit DSH Work",
		trayTooltip:   "DSH Work",
		trayStatus:    "DSH: {state}",
		updateTitle:   "Check for Updates",
		updateMessage: "Update checking is unavailable.",
		aboutTitle:    "About Work",
		aboutMessage:  "Work\n\nA local desktop host for DeepSeek Harness.",
		stateLabels: map[lifecycle.State]string{
			lifecycle.StateStarting: "Starting",
			lifecycle.StateReady:    "Ready",
			lifecycle.StateStopping: "Stopping",
			lifecycle.StateStopped:  "Stopped",
			lifecycle.StateFailed:   "Needs attention",
		},
	},
	worksettings.LocaleChinese: {
		settings:      "设置",
		openWorkspace: "打开工作区",
		help:          "帮助",
		checkUpdates:  "检查更新…",
		about:         "关于 Work",
		restartDSH:    "重启 DSH",
		quit:          "退出 DSH Work",
		trayTooltip:   "DSH Work",
		trayStatus:    "DSH：{state}",
		updateTitle:   "检查更新",
		updateMessage: "暂不支持检查更新。",
		aboutTitle:    "关于 Work",
		aboutMessage:  "Work\n\nDeepSeek Harness 的本地主机。",
		stateLabels: map[lifecycle.State]string{
			lifecycle.StateStarting: "启动中",
			lifecycle.StateReady:    "已就绪",
			lifecycle.StateStopping: "停止中",
			lifecycle.StateStopped:  "已停止",
			lifecycle.StateFailed:   "需要处理",
		},
	},
	worksettings.LocaleJapanese: {
		settings:      "設定",
		openWorkspace: "ワークスペースを開く",
		help:          "ヘルプ",
		checkUpdates:  "更新を確認…",
		about:         "Work について",
		restartDSH:    "DSH を再起動",
		quit:          "DSH Work を終了",
		trayTooltip:   "DSH Work",
		trayStatus:    "DSH：{state}",
		updateTitle:   "更新を確認",
		updateMessage: "更新確認は利用できません。",
		aboutTitle:    "Work について",
		aboutMessage:  "Work\n\nDeepSeek Harness のローカルデスクトップホストです。",
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
