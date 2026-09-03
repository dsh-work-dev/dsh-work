package dshmanager

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// ThemeReader is a read-only seam into the selected DSH data directory. DSH remains the
// owner of the setting and Work only projects it to its trusted surfaces.
type ThemeReader interface {
	Read(context.Context, string) (ThemePreference, error)
}

// FileThemeReader reads the ui-theme preference written by DSH's file-backed
// settings provider. YAML v3 also accepts the JSON form supported by DSH.
type FileThemeReader struct{}

func (FileThemeReader) Read(ctx context.Context, dataDirectoryPath string) (ThemePreference, error) {
	if err := contextError(ctx); err != nil {
		return ThemePreferenceSystem, err
	}
	if dataDirectoryPath == "" {
		return ThemePreferenceSystem, nil
	}

	data, err := readThemeSettings(dataDirectoryPath)
	if errors.Is(err, os.ErrNotExist) {
		return ThemePreferenceSystem, nil
	}
	if err != nil {
		return ThemePreferenceSystem, err
	}
	if len(data) == 0 {
		return ThemePreferenceSystem, nil
	}

	var document struct {
		UITheme struct {
			Preference string `yaml:"preference"`
		} `yaml:"ui-theme"`
	}
	if err := yaml.Unmarshal(data, &document); err != nil {
		return ThemePreferenceSystem, err
	}
	preference := ThemePreference(document.UITheme.Preference)
	if !preference.Valid() {
		return ThemePreferenceSystem, nil
	}
	return preference, nil
}

func readThemeSettings(dataDirectoryPath string) ([]byte, error) {
	for _, name := range []string{"settings.yaml", "settings.yml", "settings.json"} {
		data, err := os.ReadFile(filepath.Join(dataDirectoryPath, name))
		if err == nil {
			return data, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	return nil, os.ErrNotExist
}

func selectedTheme(ctx context.Context, dataDirectories []DataDirectoryInfo, active, desired *LaunchTarget, reader ThemeReader) ThemePreference {
	if err := contextError(ctx); err != nil || reader == nil {
		return ThemePreferenceSystem
	}
	selection := active
	if selection == nil {
		selection = desired
	}
	if selection == nil {
		return ThemePreferenceSystem
	}
	dataDirectory, ok := findDataDirectory(dataDirectories, selection.Profile.DataDirectoryID)
	if !ok {
		return ThemePreferenceSystem
	}
	preference, err := reader.Read(ctx, dataDirectory.Path)
	if err != nil || !preference.Valid() {
		return ThemePreferenceSystem
	}
	return preference
}
