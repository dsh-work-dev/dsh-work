package dshmanager

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/local/work/internal/lifecycle"
)

// State is the versioned manager persistence contract. Active state is
// intentionally absent because it is only true while a Host Worker is alive.
type State struct {
	Version         int                 `json:"version"`
	DataDirectories []DataDirectoryInfo `json:"dataDirectories,omitempty"`
	Runtimes        []RuntimeInfo       `json:"runtimes,omitempty"`
	Desired         *LaunchTarget       `json:"desired,omitempty"`
}

// StateStore isolates persistence and version policy from manager policy. A future
// platform or encrypted store can implement this contract without changing
// selection or plugin behavior.
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
		return nil, failure(lifecycle.ErrorManagerStateInvalid, "manager state is invalid", "the persisted selection has an unsupported format")
	}
	return &state, nil
}

func decodeState(data []byte) (State, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var state State
	if err := decoder.Decode(&state); err != nil {
		return State{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return State{}, errors.New("manager state contains multiple documents")
		}
		return State{}, err
	}
	if err := validateState(state); err != nil {
		return State{}, err
	}
	return state, nil
}

func validateState(state State) error {
	if state.Version != stateVersion {
		return errors.New("unsupported manager state version")
	}
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
	if state.Desired != nil {
		if err := validateLaunchTarget(*state.Desired); err != nil {
			return err
		}
	}
	return nil
}

func validateLaunchTarget(target LaunchTarget) error {
	if target.RuntimeID == "" {
		return errors.New("launch target runtime identity is required")
	}
	return validateProfileRef(target.Profile)
}

func (FileStateStore) Save(ctx context.Context, path string, state State) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return failure(lifecycle.ErrorManagerStateInvalid, "manager state could not be encoded", "the desired selection could not be persisted")
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return failure(lifecycle.ErrorManagerStateInvalid, "manager state directory could not be created", "the desired selection could not be persisted")
	}
	temporary, err := os.CreateTemp(directory, ".manager-state-*.tmp")
	if err != nil {
		return failure(lifecycle.ErrorManagerStateInvalid, "manager state could not be written", "the desired selection could not be persisted")
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
		return failure(lifecycle.ErrorManagerStateInvalid, "manager state could not be secured", "the desired selection could not be persisted")
	}
	if _, err := temporary.Write(data); err != nil {
		return failure(lifecycle.ErrorManagerStateInvalid, "manager state could not be written", "the desired selection could not be persisted")
	}
	if err := temporary.Sync(); err != nil {
		return failure(lifecycle.ErrorManagerStateInvalid, "manager state could not be flushed", "the desired selection could not be persisted")
	}
	if err := temporary.Close(); err != nil {
		return failure(lifecycle.ErrorManagerStateInvalid, "manager state could not be closed", "the desired selection could not be persisted")
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return failure(lifecycle.ErrorManagerStateInvalid, "manager state could not be replaced", "the desired selection could not be persisted")
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
		return []PluginInfo{}, nil
	}
	var manifest struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return []PluginInfo{}, nil
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
		plugins = append(plugins, PluginInfo{
			Name: name, Package: name, Spec: versions[name], Installed: true,
		})
	}
	return plugins, nil
}
