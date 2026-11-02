//go:build windows

package browserhost

import (
	"golang.org/x/sys/windows/registry"
	"os"
	"path/filepath"
	"strings"
)

func discover() Discovery {
	installed := func(executable string) bool {
		for _, root := range []registry.Key{registry.CURRENT_USER, registry.LOCAL_MACHINE} {
			for _, view := range []uint32{registry.WOW64_64KEY, registry.WOW64_32KEY} {
				key, e := registry.OpenKey(root, `Software\Microsoft\Windows\CurrentVersion\App Paths\`+executable, registry.QUERY_VALUE|view)
				if e != nil {
					continue
				}
				path, _, e := key.GetStringValue("")
				key.Close()
				path = strings.Trim(strings.TrimSpace(path), `"`)
				if e != nil || !localAbsolute(path) || !strings.EqualFold(filepath.Base(path), executable) {
					continue
				}
				if info, e := os.Stat(path); e == nil && info.Mode().IsRegular() {
					return true
				}
			}
		}
		return false
	}
	result := Discovery{Chrome: installed("chrome.exe"), Edge: installed("msedge.exe"), Default: "unknown"}
	key, e := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\Shell\Associations\UrlAssociations\https\UserChoice`, registry.QUERY_VALUE)
	if e == nil {
		defer key.Close()
		id, _, e := key.GetStringValue("ProgId")
		if e == nil && id != "" {
			switch {
			case strings.HasPrefix(id, "ChromeHTML"):
				result.Default = "chrome"
			case strings.HasPrefix(id, "MSEdgeHTM"):
				result.Default = "edge"
			default:
				result.Default = "other"
			}
		}
	}
	return result
}
