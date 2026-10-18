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
	LogRoot       string
	LocationsFile string
}

// DefaultDirectories resolves dsh-work's platform storage layout.
func DefaultDirectories() (Directories, error) {
	var paths Directories
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
		// use Local AppData. Keep the location record as a file beside the cache.
		paths.Root = filepath.Join(configRoot, "dsh-work")
		paths.CacheRoot = filepath.Join(cacheRoot, "dsh-work")
		paths.LogRoot = filepath.Join(cacheRoot, "dsh-work", "logs")
		paths.LocationsFile = filepath.Join(cacheRoot, "dsh-work-paths.json")
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
		// directory. Keep its location record as a sibling file.
		paths.Root = filepath.Join(configRoot, "dsh-work")
		paths.CacheRoot = filepath.Join(cacheRoot, "dsh-work")
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			if err == nil {
				err = fmt.Errorf("empty home directory")
			}
			return Directories{}, fmt.Errorf("resolve user home directory: %w", err)
		}
		paths.LogRoot = filepath.Join(home, "Library", "Logs", "dsh-work")
		paths.LocationsFile = filepath.Join(configRoot, "dsh-work-paths.json")
	default:
		if xdg.ConfigHome == "" || xdg.DataHome == "" || xdg.CacheHome == "" || xdg.StateHome == "" {
			return Directories{}, fmt.Errorf("XDG config, data, cache or state directory is unavailable")
		}
		home, homeErr := os.UserHomeDir()
		if homeErr != nil || home == "" {
			if homeErr == nil {
				homeErr = fmt.Errorf("empty home directory")
			}
			return Directories{}, fmt.Errorf("resolve user home directory: %w", homeErr)
		}
		paths.Root = filepath.Join(home, ".dsh-work")
		paths.UserDataRoot = filepath.Join(xdg.DataHome, "dsh-work")
		paths.CacheRoot = filepath.Join(xdg.CacheHome, "dsh-work")
		paths.LogRoot = filepath.Join(xdg.StateHome, "dsh-work", "logs")
		paths.LocationsFile = filepath.Join(xdg.ConfigHome, "dsh-work.json")
	}
	return paths, nil
}
