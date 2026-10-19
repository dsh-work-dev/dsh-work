package diagnostics

import (
	"path/filepath"
	"regexp"
	"strings"
)

func (d *Diagnostics) anonymize(text string) string {
	if home := d.config.HomeDir; home != "" {
		variants := []string{
			home,
			strings.ReplaceAll(home, `\`, `\\`),
			filepath.ToSlash(home),
		}
		for _, variant := range variants {
			if variant == "" {
				continue
			}
			pattern := regexp.MustCompile("(?i)" + regexp.QuoteMeta(variant))
			text = pattern.ReplaceAllString(text, "~")
		}
	}
	if host := d.config.HostName; len(host) >= 3 {
		pattern := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(host) + `\b`)
		text = pattern.ReplaceAllString(text, "<host>")
	}
	return text
}
