package dshmanager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tailscale/hujson"

	"github.com/local/dsh-work/internal/lifecycle"
)

// A disabled plugin stays installed but leaves the profile's layer stack: its
// package name is removed from `dsh.profile.bundles` in the profile manifest, so
// DSH loads neither its code nor its patches. dsh-work records the disable in
// its own state because every `dsh plugin` command reconciles that list against
// the installed packages and would silently re-add the plugin; the record is
// re-applied before each launch.

const maxPluginDisableRecords = 256

// corePackagePrefix names the DSH distribution. Its packages are never disabled.
const corePackagePrefix = "@deepseek-ai/"

// IsCorePluginPackage reports whether a package belongs to the DSH distribution.
func IsCorePluginPackage(name string) bool {
	return strings.HasPrefix(name, corePackagePrefix)
}

// SetPluginDisabled disables or re-enables one installed plugin in the current
// profile. The Host wraps this in its stopped-Worker transaction; without a
// Host the manager's own current-profile gate applies.
func (m *Manager) SetPluginDisabled(ctx context.Context, request PluginDisableRequest) (PluginResult, error) {
	release, err := m.acquireOperation(ctx)
	if err != nil {
		return PluginResult{}, err
	}
	defer release()
	dataDirectory, _, _, err := m.resolvePluginTarget(ctx, request.Target, false, true)
	if err != nil {
		return PluginResult{}, err
	}
	return m.setPluginDisabledLocked(ctx, dataDirectory, request.Target.Profile, request.Package, request.Disabled)
}

// ApplyPluginDisabled is SetPluginDisabled for a profile no Worker is using:
// either inside the Host's stopped-Worker transaction or after the profile
// failed to start. The already-resolved launch names the profile directory.
func (m *Manager) ApplyPluginDisabled(ctx context.Context, launch ResolvedLaunch, packageName string, disabled bool) (PluginResult, error) {
	if err := m.requireNoCurrentRunContext(); err != nil {
		return PluginResult{}, err
	}
	release, err := m.acquireOperation(ctx)
	if err != nil {
		return PluginResult{}, err
	}
	defer release()
	return m.setPluginDisabledLocked(ctx, launch.DataDirectory, launch.Target.Profile, packageName, disabled)
}

// EnforcePluginDisables removes every plugin recorded as disabled for the
// launch's profile from its layer list. It runs before each launch, which is
// the only point where the list is read.
func (m *Manager) EnforcePluginDisables(ctx context.Context, launch ResolvedLaunch) error {
	records := m.pluginDisablesFor(launch.Target.Profile)
	if len(records) == 0 {
		return contextError(ctx)
	}
	release, err := m.acquireOperation(ctx)
	if err != nil {
		return err
	}
	defer release()
	packages := make([]string, 0, len(records))
	for _, record := range records {
		packages = append(packages, record.Package)
	}
	profilePath := filepath.Join(launch.DataDirectory.Path, "profiles", launch.Target.Profile.Name)
	if _, err := removeProfileBundles(profilePath, packages); err != nil {
		// A profile without a manifest has no layer list to keep a plugin out of.
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return failureWithMeta(lifecycle.ErrorPluginDisableFailed, "Disabled plugins could not be kept out of the profile", err.Error(), true, false)
	}
	return nil
}

func (m *Manager) setPluginDisabledLocked(ctx context.Context, dataDirectory DataDirectoryInfo, profile ProfileRef, packageName string, disabled bool) (PluginResult, error) {
	if err := contextError(ctx); err != nil {
		return PluginResult{}, err
	}
	packageName = strings.TrimSpace(packageName)
	if !validPackageName(packageName) || validatePluginSpec(packageName) != nil {
		return PluginResult{}, failure(lifecycle.ErrorPluginSpecInvalid, "plugin package name is invalid", "use one installed package name")
	}
	if IsCorePluginPackage(packageName) {
		return PluginResult{}, failure(lifecycle.ErrorPluginProtected, "DSH distribution packages cannot be disabled", "only third-party plugins can be disabled")
	}
	profilePath := filepath.Join(dataDirectory.Path, "profiles", profile.Name)
	manifest, err := readProfileBundleManifest(profilePath)
	if err != nil {
		return PluginResult{}, failure(lifecycle.ErrorProfileInvalid, "DSH profile could not be read", "the profile package manifest is unavailable or invalid")
	}
	if _, installed := manifest.Dependencies[packageName]; !installed {
		return PluginResult{}, failure(lifecycle.ErrorPluginNotInstalled, "the plugin is not installed in this profile", "refresh the plugin list and try again")
	}
	existing, recorded := m.pluginDisable(profile, packageName)
	if disabled {
		if recorded {
			// The record may have been re-applied already; enforce it anyway.
			if _, err := removeProfileBundles(profilePath, []string{packageName}); err != nil {
				return PluginResult{}, failureWithMeta(lifecycle.ErrorPluginDisableFailed, "The plugin could not be disabled", err.Error(), true, false)
			}
			return m.pluginResult(ctx, profilePath, profile)
		}
		index := indexOf(manifest.Bundles(), packageName)
		if index < 0 {
			return PluginResult{}, failure(lifecycle.ErrorPluginDisableFailed, "this package is not a profile plugin layer", "the package is installed as a plain dependency and is not loaded as a plugin")
		}
		record := PluginDisableRecord{Profile: profile, Package: packageName, BundleIndex: index, DisabledAt: time.Now().UTC().Format(time.RFC3339)}
		// Record first so a failed manifest write can never leave a plugin out of
		// the layer list without the record that restores it.
		if err := m.savePluginDisable(ctx, record, false); err != nil {
			return PluginResult{}, err
		}
		if _, err := removeProfileBundles(profilePath, []string{packageName}); err != nil {
			_ = m.savePluginDisable(context.Background(), record, true)
			return PluginResult{}, failureWithMeta(lifecycle.ErrorPluginDisableFailed, "The plugin could not be disabled", err.Error(), true, false)
		}
		return m.pluginResult(ctx, profilePath, profile)
	}
	if !recorded {
		return m.pluginResult(ctx, profilePath, profile)
	}
	if err := insertProfileBundle(profilePath, packageName, existing.BundleIndex); err != nil {
		return PluginResult{}, failureWithMeta(lifecycle.ErrorPluginDisableFailed, "The plugin could not be enabled", err.Error(), true, false)
	}
	if err := m.savePluginDisable(ctx, existing, true); err != nil {
		// Keep the files consistent with the record that still says disabled.
		_, _ = removeProfileBundles(profilePath, []string{packageName})
		return PluginResult{}, err
	}
	return m.pluginResult(ctx, profilePath, profile)
}

func (m *Manager) pluginResult(ctx context.Context, profilePath string, profile ProfileRef) (PluginResult, error) {
	plugins, err := m.profilePlugins(ctx, profilePath)
	if err != nil {
		return PluginResult{}, err
	}
	return PluginResult{Profile: profile, Plugins: m.markDisabledPlugins(profile, plugins)}, nil
}

// requireNoCurrentRunContext keeps a direct profile write away from a profile a
// Ready Run context is using. The Host additionally refuses while a Worker runs.
func (m *Manager) requireNoCurrentRunContext() error {
	m.mu.RLock()
	current := m.current
	m.mu.RUnlock()
	if current != nil {
		return errors.New("plugin changes require a stopped Run context")
	}
	return nil
}

// ProfilePluginPackages lists the third-party plugin packages installed in the
// launch's profile. Failure attribution only names packages from this list.
func (m *Manager) ProfilePluginPackages(ctx context.Context, launch ResolvedLaunch) ([]string, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	manifest, err := readProfileBundleManifest(filepath.Join(launch.DataDirectory.Path, "profiles", launch.Target.Profile.Name))
	if err != nil {
		return nil, err
	}
	packages := make([]string, 0, len(manifest.Dependencies))
	for name := range manifest.Dependencies {
		if validPackageName(name) && !IsCorePluginPackage(name) {
			packages = append(packages, name)
		}
	}
	return packages, nil
}

func (m *Manager) pluginDisablesFor(profile ProfileRef) []PluginDisableRecord {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var records []PluginDisableRecord
	for _, record := range m.pluginDisables {
		if record.Profile == profile {
			records = append(records, record)
		}
	}
	return records
}

func (m *Manager) pluginDisable(profile ProfileRef, packageName string) (PluginDisableRecord, bool) {
	for _, record := range m.pluginDisablesFor(profile) {
		if record.Package == packageName {
			return record, true
		}
	}
	return PluginDisableRecord{}, false
}

// markDisabledPlugins projects the disable records onto a plugin listing.
func (m *Manager) markDisabledPlugins(profile ProfileRef, plugins []PluginInfo) []PluginInfo {
	records := m.pluginDisablesFor(profile)
	for index := range plugins {
		plugins[index].Disabled = false
		for _, record := range records {
			if record.Package == plugins[index].Package {
				plugins[index].Disabled = true
				break
			}
		}
	}
	return plugins
}

// savePluginDisable adds (or, with remove, drops) one record and persists the
// manager state before publishing it in memory.
func (m *Manager) savePluginDisable(ctx context.Context, record PluginDisableRecord, remove bool) error {
	m.mu.RLock()
	records := append([]PluginDisableRecord(nil), m.pluginDisables...)
	state := m.stateLocked()
	statePath := m.config.StatePath
	m.mu.RUnlock()
	filtered := records[:0]
	for _, existing := range records {
		if existing.Profile != record.Profile || existing.Package != record.Package {
			filtered = append(filtered, existing)
		}
	}
	records = filtered
	if !remove {
		if len(records) >= maxPluginDisableRecords {
			return failure(lifecycle.ErrorPluginDisableFailed, "too many plugins are disabled", "enable or remove a disabled plugin first")
		}
		records = append(records, record)
	}
	state.PluginDisables = append([]PluginDisableRecord(nil), records...)
	if err := m.store.Save(ctx, statePath, state); err != nil {
		return err
	}
	m.mu.Lock()
	m.pluginDisables = records
	m.mu.Unlock()
	return nil
}

// forgetPluginDisable drops the record of a plugin that is no longer installed.
func (m *Manager) forgetPluginDisable(ctx context.Context, profile ProfileRef, packageName string) error {
	record, ok := m.pluginDisable(profile, packageName)
	if !ok {
		return nil
	}
	return m.savePluginDisable(ctx, record, true)
}

func validatePluginDisableRecords(records []PluginDisableRecord) error {
	if len(records) > maxPluginDisableRecords {
		return errors.New("plugin disable records exceed bounded capacity")
	}
	seen := map[string]bool{}
	for _, record := range records {
		if validateProfileRef(record.Profile) != nil || !validPackageName(record.Package) || IsCorePluginPackage(record.Package) || record.BundleIndex < 0 || strings.TrimSpace(record.DisabledAt) == "" {
			return errors.New("plugin disable record is invalid")
		}
		key := record.Profile.DataDirectoryID + "\x00" + record.Profile.Name + "\x00" + record.Package
		if seen[key] {
			return errors.New("duplicate plugin disable record")
		}
		seen[key] = true
	}
	return nil
}

// profileBundleManifest is the part of a DSH profile manifest the disable
// mechanism reads. The file itself is edited in place through JSON Patch so
// every other byte DSH or the user wrote is preserved.
type profileBundleManifest struct {
	Dependencies map[string]string `json:"dependencies"`
	DSH          struct {
		Profile struct {
			Bundles *[]string `json:"bundles"`
		} `json:"profile"`
	} `json:"dsh"`
}

func (manifest profileBundleManifest) Bundles() []string {
	if manifest.DSH.Profile.Bundles == nil {
		return nil
	}
	return *manifest.DSH.Profile.Bundles
}

func readProfileBundleManifest(profilePath string) (profileBundleManifest, error) {
	data, err := readProfileManifestBytes(profilePath)
	if err != nil {
		return profileBundleManifest{}, err
	}
	var manifest profileBundleManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return profileBundleManifest{}, err
	}
	return manifest, nil
}

func readProfileManifestBytes(profilePath string) ([]byte, error) {
	data, err := os.ReadFile(filepath.Join(profilePath, "package.json"))
	if err != nil {
		return nil, err
	}
	if len(data) > maxProfileManifestBytes {
		return nil, errors.New("the profile manifest exceeds its size limit")
	}
	return data, nil
}

// removeProfileBundles drops the named packages from `dsh.profile.bundles` and
// reports the index each one had. Packages already absent are ignored.
func removeProfileBundles(profilePath string, packages []string) (map[string]int, error) {
	data, err := readProfileManifestBytes(profilePath)
	if err != nil {
		return nil, err
	}
	var manifest profileBundleManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, err
	}
	bundles := manifest.Bundles()
	removed := map[string]int{}
	var operations []map[string]any
	// Remove from the end so earlier indexes stay valid while patching.
	for index := len(bundles) - 1; index >= 0; index-- {
		if indexOf(packages, bundles[index]) >= 0 {
			removed[bundles[index]] = index
			operations = append(operations, map[string]any{"op": "remove", "path": fmt.Sprintf("/dsh/profile/bundles/%d", index)})
		}
	}
	if len(operations) == 0 {
		return removed, nil
	}
	return removed, patchProfileManifest(profilePath, data, operations)
}

// insertProfileBundle puts a package back into `dsh.profile.bundles` at the
// position it had, or at the end when the list has since become shorter.
func insertProfileBundle(profilePath, packageName string, index int) error {
	data, err := readProfileManifestBytes(profilePath)
	if err != nil {
		return err
	}
	var manifest profileBundleManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return err
	}
	if manifest.DSH.Profile.Bundles == nil {
		return errors.New("the profile manifest has no bundle list")
	}
	bundles := manifest.Bundles()
	if indexOf(bundles, packageName) >= 0 {
		return nil
	}
	if index < 0 || index > len(bundles) {
		index = len(bundles)
	}
	path := fmt.Sprintf("/dsh/profile/bundles/%d", index)
	if index == len(bundles) {
		path = "/dsh/profile/bundles/-"
	}
	return patchProfileManifest(profilePath, data, []map[string]any{{"op": "add", "path": path, "value": packageName}}, func(value *hujson.Value) {
		matchNeighbourIndentation(value.Find("/dsh/profile/bundles"), index)
	})
}

// matchNeighbourIndentation gives an inserted array element the leading
// whitespace of an adjacent element, so a multi-line list stays multi-line.
func matchNeighbourIndentation(list *hujson.Value, index int) {
	if list == nil {
		return
	}
	array, ok := list.Value.(*hujson.Array)
	if !ok || index >= len(array.Elements) || len(array.Elements) < 2 {
		return
	}
	neighbour := index + 1
	if neighbour >= len(array.Elements) {
		neighbour = index - 1
	}
	array.Elements[index].BeforeExtra = append(hujson.Extra(nil), array.Elements[neighbour].BeforeExtra...)
}

func patchProfileManifest(profilePath string, data []byte, operations []map[string]any, adjust ...func(*hujson.Value)) error {
	value, err := hujson.Parse(data)
	if err != nil {
		return err
	}
	patch, err := json.Marshal(operations)
	if err != nil {
		return err
	}
	if err := value.Patch(patch); err != nil {
		return err
	}
	for _, apply := range adjust {
		apply(&value)
	}
	next := value.Pack()
	if !json.Valid(next) {
		return errors.New("the patched profile manifest is not valid JSON")
	}
	return writeFileAtomic(filepath.Join(profilePath, "package.json"), next)
}

// writeFileAtomic replaces a file through a synced temporary sibling so a
// crash leaves either the old or the new manifest, never a partial one.
func writeFileAtomic(path string, data []byte) error {
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if _, err := temporary.Write(data); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Chmod(temporaryPath, mode); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	committed = true
	return nil
}

func indexOf(values []string, value string) int {
	for index, candidate := range values {
		if candidate == value {
			return index
		}
	}
	return -1
}
