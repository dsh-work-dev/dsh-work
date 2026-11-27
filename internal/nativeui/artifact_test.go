package nativeui

import (
	"github.com/local/dsh-work/internal/settings"
	"strings"
	"testing"
)

func TestArtifactPickerLocalePreservesFolderScope(t *testing.T) {
	for locale, folder := range map[settings.Locale]string{settings.LocaleEnglish: "folder", settings.LocaleChinese: "文件夹", settings.LocaleJapanese: "フォルダー"} {
		copy := ArtifactPickerFor(locale)
		if copy.Open == "" || !strings.Contains(copy.Title, folder) || !strings.Contains(copy.Title, copy.Open) {
			t.Fatalf("locale %s lost action or folder scope: %+v", locale, copy)
		}
	}
}
