package pet

import (
	"os"
	"path/filepath"
)

// CommunityHome resolves the local community package home from the process environment.
func CommunityHome() string {
	if home := os.Getenv("DSH_HOME"); home != "" {
		return home
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".dsh")
}
