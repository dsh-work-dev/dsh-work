package desktopapp

import "github.com/local/dsh-work/internal/settings"

func browserStopLabel(locale settings.Locale) string {
	switch locale {
	case settings.LocaleEnglish:
		return "Stop browser use"
	case settings.LocaleJapanese:
		return "ブラウザー操作を停止"
	default:
		return "停止使用浏览器"
	}
}

func browserAttentionLabel(locale settings.Locale) string {
	switch locale {
	case settings.LocaleEnglish:
		return "Needs your attention"
	case settings.LocaleJapanese:
		return "対応が必要です"
	default:
		return "需要你处理"
	}
}
