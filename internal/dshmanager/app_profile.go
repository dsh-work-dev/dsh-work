package dshmanager

import (
	"path/filepath"

	"github.com/local/dsh-work/internal/lifecycle"
)

func isAppProfile(dataDirectory DataDirectoryInfo, name string) bool {
	return dataDirectory.Ownership == DataDirectoryOwnershipDSHWork && name == AppProfileName
}

func ensureAppProfile(config Config, dataDirectory DataDirectoryInfo, name string) error {
	if !isAppProfile(dataDirectory, name) {
		return nil
	}
	if err := prepareProfileWorkingDirectory(dataDirectory); err != nil {
		return failureWithMeta(lifecycle.ErrorProfilePreparationFailed, "the DSH data directory could not be prepared", err.Error(), true, false)
	}
	if config.AppProfile == nil {
		return failureWithMeta(lifecycle.ErrorProfilePreparationFailed, "the DSH profile needs preparation", "the app profile provider is unavailable", true, false)
	}
	profilePath := filepath.Join(dataDirectory.Path, "profiles", AppProfileName)
	if err := config.AppProfile.InitializeAppProfile(profilePath); err != nil {
		return failureWithMeta(lifecycle.ErrorProfilePreparationFailed, "the DSH profile could not be prepared", err.Error(), true, false)
	}
	return nil
}
