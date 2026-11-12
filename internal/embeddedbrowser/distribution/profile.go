package distribution

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

const profileMarker = "dsh-cef-engine.json"

// AdmitProfile is called under the registry's exclusive profile lease before
// CEF opens it. A profile without a marker is admitted only while empty; older
// or unknown data is preserved rather than opened by an incompatible engine.
func AdmitProfile(directory string) error {
	marker := filepath.Join(directory, profileMarker)
	data, err := os.ReadFile(marker)
	if err == nil {
		var recorded struct {
			CEFVersion string `json:"cefVersion"`
		}
		if json.Unmarshal(data, &recorded) != nil || recorded.CEFVersion != CEFVersion {
			return errors.New("CEF_PROFILE_ENGINE_INCOMPATIBLE")
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(directory, 0700); err != nil {
			return err
		}
		entries = nil
	} else if err != nil {
		return err
	}
	if len(entries) != 0 {
		return errors.New("CEF_PROFILE_ENGINE_UNKNOWN")
	}
	data, _ = json.Marshal(struct {
		CEFVersion string `json:"cefVersion"`
	}{CEFVersion})
	file, err := os.CreateTemp(directory, ".dsh-cef-engine-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err := file.Chmod(0600); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), marker)
}
