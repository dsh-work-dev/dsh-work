package browserhost

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/local/dsh-work/internal/settings"
)

// ProfileLauncher handles only user-confirmed launch locations. It does not
// identify an extension instance or authorize a browser action.
type ProfileLauncher interface {
	Resolve(browser, executable, directory string) (settings.BrowserLaunchHint, error)
	Open(context.Context, string, settings.BrowserLaunchHint) error
}

func NewProfileLauncher() ProfileLauncher { return profileLauncher{} }

// ResolveProfileDirectory reads metadata only in the directory explicitly
// chosen by the user. No default-profile scan, cookies or account data is used.
func ResolveProfileDirectory(executable, directory string) (settings.BrowserLaunchHint, error) {
	h := settings.BrowserLaunchHint{Executable: executable, UserDataDirectory: filepath.Dir(filepath.Clean(directory)), ProfileDirectory: filepath.Base(filepath.Clean(directory))}
	if !h.Valid() {
		return h, errors.New("BROWSER_PROFILE_DIRECTORY_INVALID")
	}
	for _, p := range []string{directory, filepath.Join(directory, "Preferences")} {
		info, err := os.Stat(p)
		if err != nil {
			return h, errors.New("BROWSER_PROFILE_DIRECTORY_MISSING")
		}
		if p == directory && !info.IsDir() || p != directory && !info.Mode().IsRegular() {
			return h, errors.New("BROWSER_PROFILE_DIRECTORY_INVALID")
		}
	}
	f, err := os.Open(filepath.Join(h.UserDataDirectory, "Local State"))
	if err != nil {
		return h, errors.New("BROWSER_PROFILE_DIRECTORY_INVALID")
	}
	defer f.Close()
	var metadata struct {
		Profile struct {
			InfoCache map[string]struct{} `json:"info_cache"`
		} `json:"profile"`
	}
	if json.NewDecoder(io.LimitReader(f, 16<<20)).Decode(&metadata) != nil {
		return h, errors.New("BROWSER_PROFILE_DIRECTORY_INVALID")
	}
	if _, ok := metadata.Profile.InfoCache[h.ProfileDirectory]; !ok {
		return h, errors.New("BROWSER_PROFILE_DIRECTORY_INVALID")
	}
	return h, nil
}
