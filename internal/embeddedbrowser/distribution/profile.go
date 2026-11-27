package distribution

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const profileMarker = "dsh-cef-engine.json"
const profileMarkerStagePrefix = ".dsh-cef-engine-"
const appMacKeychain = "dsh-work"

type profileIdentity struct {
	CEFVersion  string `json:"cefVersion"`
	MacKeychain string `json:"macKeychain,omitempty"`
}

func readProfileIdentity(directory string) (profileIdentity, error) {
	var identity profileIdentity
	data, err := os.ReadFile(filepath.Join(directory, profileMarker))
	if err != nil {
		return identity, err
	}
	if json.Unmarshal(data, &identity) != nil || identity.CEFVersion != CEFVersion {
		return identity, errors.New("CEF_PROFILE_ENGINE_INCOMPATIBLE")
	}
	if identity.MacKeychain != "" && identity.MacKeychain != appMacKeychain {
		return identity, errors.New("CEF_PROFILE_KEYCHAIN_INCOMPATIBLE")
	}
	return identity, nil
}

// UsesAppMacKeychain preserves the default keychain for legacy profiles and
// private development fixtures that do not use the installed profile marker.
func UsesAppMacKeychain(directory string) (bool, error) {
	identity, err := readProfileIdentity(directory)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return identity.MacKeychain == appMacKeychain, err
}

// AdmitProfile is called under the registry's exclusive profile lease before
// CEF opens it. A profile without a marker is admitted only while empty; older
// or unknown data is preserved rather than opened by an incompatible engine.
func AdmitProfile(directory string) error {
	return admitProfile(directory, runtime.GOOS == "darwin")
}

func admitProfile(directory string, mac bool) error {
	identity, err := readProfileIdentity(directory)
	if err == nil {
		if !mac && identity.MacKeychain != "" {
			return errors.New("CEF_PROFILE_KEYCHAIN_INCOMPATIBLE")
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
	for _, entry := range entries {
		suffix, staged := strings.CutPrefix(entry.Name(), profileMarkerStagePrefix)
		// CreateTemp in the pinned Go toolchain appends a decimal uint32.
		// An interrupted first write can leave this file before CEF has run.
		number, parseErr := strconv.ParseUint(suffix, 10, 32)
		if !staged || !entry.Type().IsRegular() || parseErr != nil || strconv.FormatUint(number, 10) != suffix {
			return errors.New("CEF_PROFILE_ENGINE_UNKNOWN")
		}
	}
	identity = profileIdentity{CEFVersion: CEFVersion}
	if mac {
		identity.MacKeychain = appMacKeychain
	}
	data, _ := json.Marshal(identity)
	file, err := os.CreateTemp(directory, profileMarkerStagePrefix+"*")
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
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), filepath.Join(directory, profileMarker))
}
