package storagepaths

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/adrg/xdg"
)

// Directories contains the stable location record and the platform defaults
// for persistent application data and regenerable local cache data.
type Directories struct {
	Root         string
	UserDataRoot string
	CacheRoot    string
	// LogRoot holds per-machine log files; it never moves with Root.
	LogRoot    string
	ConfigFile string
}

// DefaultDirectories resolves dsh-work's platform storage layout.
func DefaultDirectories() (Directories, error) {
	var paths Directories
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		if err == nil {
			err = fmt.Errorf("empty home directory")
		}
		return paths, fmt.Errorf("resolve user home directory: %w", err)
	}
	paths.UserDataRoot = filepath.Join(home, ".dsh-work")
	switch runtime.GOOS {
	case "windows":
		configRoot, err := os.UserConfigDir()
		if err != nil || configRoot == "" {
			if err == nil {
				err = fmt.Errorf("empty config directory")
			}
			return paths, fmt.Errorf("resolve per-user config directory: %w", err)
		}
		cacheRoot, err := os.UserCacheDir()
		if err != nil || cacheRoot == "" {
			if err == nil {
				err = fmt.Errorf("empty cache directory")
			}
			return paths, fmt.Errorf("resolve per-user cache directory: %w", err)
		}
		// Application data stays in Roaming AppData; WebView and other caches
		// use Local AppData. Keep config.toml in Local AppData.
		paths.Root = filepath.Join(configRoot, "dsh-work")
		paths.CacheRoot = filepath.Join(cacheRoot, "dsh-work")
		paths.LogRoot = filepath.Join(cacheRoot, "dsh-work", "logs")
		paths.ConfigFile = filepath.Join(cacheRoot, "dsh-work", "config.toml")
	case "darwin":
		configRoot, err := os.UserConfigDir()
		if err != nil || configRoot == "" {
			if err == nil {
				err = fmt.Errorf("empty config directory")
			}
			return paths, fmt.Errorf("resolve per-user config directory: %w", err)
		}
		cacheRoot, err := os.UserCacheDir()
		if err != nil || cacheRoot == "" {
			if err == nil {
				err = fmt.Errorf("empty cache directory")
			}
			return paths, fmt.Errorf("resolve per-user cache directory: %w", err)
		}
		// Application Support is the data root itself, without an extra storage
		// directory. Keep config.toml inside Application Support.
		paths.Root = filepath.Join(configRoot, "dsh-work")
		paths.CacheRoot = filepath.Join(cacheRoot, "dsh-work")
		paths.LogRoot = filepath.Join(home, "Library", "Logs", "dsh-work")
		paths.ConfigFile = filepath.Join(configRoot, "dsh-work", "config.toml")
	default:
		if xdg.ConfigHome == "" || xdg.DataHome == "" || xdg.CacheHome == "" || xdg.StateHome == "" {
			return Directories{}, fmt.Errorf("XDG config, data, cache or state directory is unavailable")
		}
		paths.Root = filepath.Join(xdg.DataHome, "dsh-work")
		paths.CacheRoot = filepath.Join(xdg.CacheHome, "dsh-work")
		paths.LogRoot = filepath.Join(xdg.StateHome, "dsh-work", "logs")
		paths.ConfigFile = filepath.Join(xdg.ConfigHome, "dsh-work", "config.toml")
	}
	return paths, nil
}
