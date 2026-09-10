package dshadapter

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Use DSH's supported plugin configuration to keep durable user data outside
// the profile and its generated dependency tree.
func prepareUserDataPatch(environment, root string) (string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", err
	}
	rows := []any{
		map[string]any{"id": "session-persistence-jsonl", "config": map[string]string{"root": filepath.Join(root, "sessions")}},
		map[string]any{"id": "storage-json", "config": map[string]string{"root": filepath.Join(root, "storages")}},
		map[string]any{"id": "attachment-local", "config": map[string]string{"dshHome": root}},
		map[string]any{"id": "settings", "config": map[string]string{"path": filepath.Join(root, "settings.yaml")}},
		map[string]any{"id": "credentials", "config": map[string]string{"path": filepath.Join(root, ".credentials.yaml")}},
	}
	data, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(environment, 0700); err != nil {
		return "", err
	}
	patch := filepath.Join(environment, "user-data.patch.json")
	if err := os.WriteFile(patch, data, 0600); err != nil {
		return "", err
	}
	return patch, nil
}
