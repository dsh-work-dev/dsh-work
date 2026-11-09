package nativeui

import "github.com/local/dsh-work/internal/settings"

// ArtifactPickerLabels is trusted Host copy; the document never supplies it.
type ArtifactPickerLabels struct{ Title, Open string }

func ArtifactPickerFor(locale settings.Locale) ArtifactPickerLabels {
	switch locale {
	case settings.LocaleEnglish:
		return ArtifactPickerLabels{"Open HTML (allow resources in its folder)", "Open HTML"}
	case settings.LocaleJapanese:
		return ArtifactPickerLabels{"HTML を開く（同じフォルダー内のリソースを許可）", "HTML を開く"}
	default:
		return ArtifactPickerLabels{"打开 HTML（允许读取所在文件夹内的资源）", "打开 HTML"}
	}
}
