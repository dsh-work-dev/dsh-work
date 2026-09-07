package dshmanager

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/local/dsh-work/internal/lifecycle"
)

// State is the manager persistence contract. Current and known-good state are
// intentionally absent because they are only true while a Host Worker is
// alive. Configured is the single persisted Run context.
type State struct {
	DataDirectories  []DataDirectoryInfo      `json:"dataDirectories,omitempty"`
	Runtimes         []RuntimeInfo            `json:"runtimes,omitempty"`
	Nodes            []NodeInstallationInfo   `json:"nodes,omitempty"`
	LatestNode       *NodeReleaseInfo         `json:"latestNode,omitempty"`
	DSHReleases      []DSHReleaseInfo         `json:"dshReleases,omitempty"`
	PluginProvenance []PluginProvenanceRecord `json:"pluginProvenance,omitempty"`
	Configured       *RunContext              `json:"configured,omitempty"`
}

// StateStore isolates persistence from manager policy. A future platform or
// encrypted store can implement this contract without changing selection or
// plugin behavior.
type StateStore interface {
	Load(context.Context, string) (*State, error)
	Save(context.Context, string, State) error
}

// FileStateStore is the default local application-data store. It writes a
// private temporary file and replaces the state atomically.
type FileStateStore struct{}

func (FileStateStore) Load(ctx context.Context, path string) (*State, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, failure(lifecycle.ErrorManagerStateInvalid, "manager state could not be read", "the persisted selection is unavailable")
	}
	state, err := decodeState(data)
	if err != nil {
		return nil, failure(lifecycle.ErrorManagerStateInvalid, "manager state is invalid", "the persisted selection is malformed or missing required fields")
	}
	return &state, nil
}

// persistedState is the read contract for the manager file. JSON unknown fields
// are intentionally ignored so a newer build can add optional catalog metadata
// without making an older build lose the user's selection.
type persistedState struct {
	DataDirectories  []DataDirectoryInfo      `json:"dataDirectories,omitempty"`
	Runtimes         []RuntimeInfo            `json:"runtimes,omitempty"`
	Nodes            []NodeInstallationInfo   `json:"nodes,omitempty"`
	LatestNode       *NodeReleaseInfo         `json:"latestNode,omitempty"`
	DSHReleases      []DSHReleaseInfo         `json:"dshReleases,omitempty"`
	PluginProvenance []PluginProvenanceRecord `json:"pluginProvenance,omitempty"`
	Configured       *RunContext              `json:"configured,omitempty"`
}

func decodeState(data []byte) (State, error) {
	var document map[string]json.RawMessage
	if err := json.Unmarshal(data, &document); err != nil {
		return State{}, err
	}
	if document == nil {
		return State{}, errors.New("manager state must be a JSON object")
	}
	var persisted persistedState
	if err := json.Unmarshal(data, &persisted); err != nil {
		return State{}, err
	}
	state := State{
		DataDirectories:  persisted.DataDirectories,
		Runtimes:         persisted.Runtimes,
		Nodes:            persisted.Nodes,
		LatestNode:       persisted.LatestNode,
		DSHReleases:      persisted.DSHReleases,
		PluginProvenance: persisted.PluginProvenance,
		Configured:       persisted.Configured,
	}
	for index := range state.Runtimes {
		normalized, err := normalizeRuntime(state.Runtimes[index])
		if err != nil {
			return State{}, err
		}
		state.Runtimes[index] = normalized
	}
	if state.Configured != nil {
		selection, err := normalizeNodeSelection(state.Configured.Node)
		if err != nil {
			return State{}, err
		}
		state.Configured.Node = selection
	}
	if err := validateState(state); err != nil {
		return State{}, err
	}
	return state, nil
}

func validateState(state State) error {
	seenDataDirectories := make(map[string]struct{}, len(state.DataDirectories))
	for _, dataDirectory := range state.DataDirectories {
		if err := validateDataDirectory(dataDirectory); err != nil {
			return err
		}
		if _, exists := seenDataDirectories[dataDirectory.ID]; exists {
			return errors.New("duplicate DSH data-directory identity")
		}
		seenDataDirectories[dataDirectory.ID] = struct{}{}
	}
	seenNodes := make(map[string]struct{}, len(state.Nodes))
	for _, node := range state.Nodes {
		if err := validateNodeInstallation(node); err != nil {
			return err
		}
		if _, exists := seenNodes[node.ID]; exists {
			return errors.New("duplicate Node installation identity")
		}
		seenNodes[node.ID] = struct{}{}
	}
	if state.LatestNode != nil {
		if err := validateNodeRelease(*state.LatestNode); err != nil {
			return err
		}
	}
	seenDSHReleases := make(map[string]struct{}, len(state.DSHReleases))
	for _, release := range state.DSHReleases {
		if err := validateDSHRelease(release); err != nil {
			return err
		}
		if _, exists := seenDSHReleases[release.Version]; exists {
			return errors.New("duplicate DSH release identity")
		}
		seenDSHReleases[release.Version] = struct{}{}
	}
	if len(state.PluginProvenance) > 256 {
		return errors.New("plugin provenance exceeds bounded capacity")
	}
	for _, record := range state.PluginProvenance {
		if validateProfileRef(record.Profile) != nil || !validPackageName(record.Package) || record.SourceKind == "" || strings.TrimSpace(record.RecordedAt) == "" {
			return errors.New("plugin provenance record is invalid")
		}
		if record.SuccessfulRoute != "" && record.SuccessfulRoute != RuntimeArtifactSourceNone && record.SuccessfulRoute != RuntimeArtifactSourceOfficial && record.SuccessfulRoute != RuntimeArtifactSourceMirror && record.SuccessfulRoute != RuntimeArtifactSourceLocal {
			return errors.New("plugin provenance route is invalid")
		}
	}
	if state.Configured != nil {
		if err := validateRunContext(*state.Configured); err != nil {
			return err
		}
		selection, _ := normalizeNodeSelection(state.Configured.Node)
		if selection.Kind == NodeSelectionManaged {
			if _, exists := seenNodes[selection.InstallationID]; !exists {
				return errors.New("configured managed Node installation is missing")
			}
		}
	}
	return nil
}

func validateRunContext(target RunContext) error {
	if target.RuntimeID == "" {
		return errors.New("Run context runtime identity is required")
	}
	if _, err := normalizeNodeSelection(target.Node); err != nil {
		return err
	}
	return validateProfileRef(target.Profile)
}

func (FileStateStore) Save(ctx context.Context, path string, state State) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return failure(lifecycle.ErrorManagerStateInvalid, "manager state could not be encoded", "the configured Run context could not be persisted")
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return failure(lifecycle.ErrorManagerStateInvalid, "manager state directory could not be created", "the configured Run context could not be persisted")
	}
	temporary, err := os.CreateTemp(directory, ".manager-state-*.tmp")
	if err != nil {
		return failure(lifecycle.ErrorManagerStateInvalid, "manager state could not be written", "the configured Run context could not be persisted")
	}
	temporaryPath := temporary.Name()
	keepTemporary := false
	defer func() {
		_ = temporary.Close()
		if !keepTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return failure(lifecycle.ErrorManagerStateInvalid, "manager state could not be secured", "the configured Run context could not be persisted")
	}
	if _, err := temporary.Write(data); err != nil {
		return failure(lifecycle.ErrorManagerStateInvalid, "manager state could not be written", "the configured Run context could not be persisted")
	}
	if err := temporary.Sync(); err != nil {
		return failure(lifecycle.ErrorManagerStateInvalid, "manager state could not be flushed", "the configured Run context could not be persisted")
	}
	if err := temporary.Close(); err != nil {
		return failure(lifecycle.ErrorManagerStateInvalid, "manager state could not be closed", "the configured Run context could not be persisted")
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return failure(lifecycle.ErrorManagerStateInvalid, "manager state could not be replaced", "the configured Run context could not be persisted")
	}
	keepTemporary = true
	return nil
}

// ProfileCatalog supplies the built-in profiles understood by one DSH
// adapter. Custom profiles are still discovered from the selected data
// directory.
type ProfileCatalog interface {
	BuiltInProfiles() []ProfileDefinition
}

// ProfileReader is a read-only projection seam. DSH remains responsible for
// reconciliation; the default reader only inspects direct dependency data.
type ProfileReader interface {
	Read(context.Context, string) ([]PluginInfo, error)
}

// FileProfileReader reads a profile package manifest without mutating it.
type FileProfileReader struct{}

func (FileProfileReader) Read(ctx context.Context, profilePath string) ([]PluginInfo, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(profilePath, "package.json"))
	if errors.Is(err, os.ErrNotExist) {
		return []PluginInfo{}, nil
	}
	if err != nil {
		return nil, err
	}
	var manifest struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, err
	}
	versions := make(map[string]string, len(manifest.Dependencies)+len(manifest.DevDependencies))
	for name, spec := range manifest.Dependencies {
		if validPackageName(name) {
			versions[name] = spec
		}
	}
	for name, spec := range manifest.DevDependencies {
		if validPackageName(name) {
			if _, exists := versions[name]; !exists {
				versions[name] = spec
			}
		}
	}
	names := make([]string, 0, len(versions))
	for name := range versions {
		names = append(names, name)
	}
	sort.Strings(names)
	plugins := make([]PluginInfo, 0, len(names))
	for _, name := range names {
		resolvedVersion := installedPackageVersion(profilePath, name)
		plugins = append(plugins, PluginInfo{
			Name: name, Package: name, Spec: versions[name], Version: resolvedVersion, CurrentVersion: resolvedVersion,
			Installed: true, SourceKind: classifyPluginSource(versions[name]), UpdateCheck: PluginUpdateUnknown,
		})
	}
	return plugins, nil
}

func installedPackageVersion(profilePath, packageName string) string {
	parts := strings.Split(filepath.ToSlash(packageName), "/")
	pathParts := append([]string{profilePath, "node_modules"}, parts...)
	data, err := os.ReadFile(filepath.Join(append(pathParts, "package.json")...))
	if err != nil {
		return ""
	}
	var manifest struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(data, &manifest) != nil || !validRuntimeVersion(manifest.Version) {
		return ""
	}
	return manifest.Version
}
