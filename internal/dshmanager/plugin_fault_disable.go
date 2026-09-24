package dshmanager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
)

// DisableFaultPlugin disables one attributed plugin after its Run context has
// failed. A live Worker uses PluginManager.setBundleEnabled; this recovery
// path persists the same selected-bundle state while no Worker can answer.
func (m *Manager) DisableFaultPlugin(ctx context.Context, launch ResolvedLaunch, packageName string) error {
	release, err := m.acquireOperation(ctx)
	if err != nil {
		return err
	}
	defer release()
	if err := contextError(ctx); err != nil {
		return err
	}
	if !validPackageName(packageName) || IsCorePluginPackage(packageName) {
		return errors.New("only an installed third-party plugin can be disabled here")
	}
	if err := m.requireNoCurrentRunContext(); err != nil {
		return err
	}

	ctx = context.WithValue(ctx, pluginLaunchKey{}, launch)
	dataDirectory, _, _, err := m.resolvePluginTarget(ctx, PluginTarget{Profile: launch.Target.Profile}, false, true)
	if err != nil {
		return err
	}
	if filepath.Clean(dataDirectory.Path) != filepath.Clean(launch.DataDirectory.Path) {
		return errors.New("the startup failure no longer points at this DSH data directory")
	}
	profilePath := filepath.Join(dataDirectory.Path, "profiles", launch.Target.Profile.Name)
	if !installedPackageHasBundle(profilePath, packageName) {
		return fmt.Errorf("%s is not an installed DSH bundle", packageName)
	}
	return removeSelectedProfileBundle(filepath.Join(profilePath, "package.json"), packageName)
}

func removeSelectedProfileBundle(manifestPath, packageName string) error {
	profileInfo, err := os.Lstat(filepath.Dir(manifestPath))
	if err != nil {
		return err
	}
	if !profileInfo.IsDir() || profileInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("the DSH profile must be a regular directory")
	}
	manifestInfo, err := os.Lstat(manifestPath)
	if err != nil {
		return err
	}
	if !manifestInfo.Mode().IsRegular() {
		return errors.New("the DSH profile manifest must be a regular file")
	}
	file, err := os.Open(manifestPath)
	if err != nil {
		return err
	}
	data, readErr := io.ReadAll(io.LimitReader(file, maxProfileManifestBytes+1))
	closeErr := file.Close()
	if readErr != nil {
		return readErr
	}
	if closeErr != nil {
		return closeErr
	}
	if len(data) > maxProfileManifestBytes {
		return errors.New("the DSH profile manifest exceeds its size limit")
	}

	var document map[string]json.RawMessage
	if err := json.Unmarshal(data, &document); err != nil || document == nil {
		return errors.New("the DSH profile manifest is invalid")
	}
	var dependencies map[string]json.RawMessage
	if err := json.Unmarshal(document["dependencies"], &dependencies); err != nil {
		return errors.New("the DSH profile dependencies are invalid")
	}
	if _, installed := dependencies[packageName]; !installed {
		return fmt.Errorf("%s is no longer a direct dependency of this DSH profile", packageName)
	}

	var dsh map[string]json.RawMessage
	if err := json.Unmarshal(document["dsh"], &dsh); err != nil || dsh == nil {
		return errors.New("the DSH profile settings are invalid")
	}
	var profile map[string]json.RawMessage
	if err := json.Unmarshal(dsh["profile"], &profile); err != nil || profile == nil {
		return errors.New("the DSH profile bundle settings are invalid")
	}
	var bundles []string
	if err := json.Unmarshal(profile["bundles"], &bundles); err != nil {
		return errors.New("the DSH profile bundle list is invalid")
	}
	if !slices.Contains(bundles, packageName) {
		return nil
	}
	updated := slices.DeleteFunc(slices.Clone(bundles), func(bundle string) bool { return bundle == packageName })
	encoded, err := json.Marshal(updated)
	if err != nil {
		return err
	}
	profile["bundles"] = encoded
	encoded, err = json.Marshal(profile)
	if err != nil {
		return err
	}
	dsh["profile"] = encoded
	encoded, err = json.Marshal(dsh)
	if err != nil {
		return err
	}
	document["dsh"] = encoded
	encoded, err = json.MarshalIndent(document, "", "  ")
	if err != nil {
		return err
	}
	return replaceProfileManifest(manifestPath, append(encoded, '\n'), manifestInfo.Mode().Perm())
}

func replaceProfileManifest(path string, data []byte, mode os.FileMode) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".package-json-plugin-disable-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	keepTemporary := false
	defer func() {
		_ = temporary.Close()
		if !keepTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(mode); err != nil {
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	keepTemporary = true
	return nil
}
