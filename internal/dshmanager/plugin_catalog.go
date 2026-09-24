package dshmanager

import "strings"

// IsCorePluginPackage reports whether a package belongs to the DSH distribution.
func IsCorePluginPackage(name string) bool {
	return strings.HasPrefix(name, "@deepseek-ai/")
}
