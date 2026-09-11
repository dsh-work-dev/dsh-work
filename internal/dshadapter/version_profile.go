package dshadapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/local/dsh-work/internal/dshmanager"
	"gopkg.in/yaml.v3"
)

const versionInputLimit = 4 << 20

var credentialURL = regexp.MustCompile(`(?i)://[^/\s]+@|[?&](?:token|key|secret|signature|password)=`)

func privateVersionInput(data []byte) bool {
	if credentialURL.Match(data) {
		return true
	}
	var node yaml.Node
	if yaml.Unmarshal(data, &node) != nil {
		return true
	}
	var visit func(*yaml.Node) bool
	visit = func(n *yaml.Node) bool {
		if n.Kind == yaml.MappingNode {
			for i := 0; i+1 < len(n.Content); i += 2 {
				key := strings.ToLower(n.Content[i].Value)
				if key == "auth" || key == "token" || key == "password" || key == "secret" || key == "npmauthtoken" || key == "npmauthident" || strings.Contains(key, "_auth") {
					return true
				}
				if visit(n.Content[i+1]) {
					return true
				}
			}
		} else {
			for _, child := range n.Content {
				if visit(child) {
					return true
				}
			}
		}
		return false
	}
	return visit(&node)
}

var dependencyFields = []string{"dependencies", "devDependencies", "optionalDependencies", "peerDependencies", "peerDependenciesMeta", "overrides", "resolutions", "pnpm"}

func openVersionProfile(path string) (*os.Root, error) {
	home, err := os.OpenRoot(filepath.Dir(filepath.Dir(path)))
	if err != nil {
		return nil, err
	}
	defer home.Close()
	return home.OpenRoot(filepath.Join("profiles", filepath.Base(path)))
}

// Only dependency inputs and the DSH bundle association list belong to a
// version restore point. Other profile settings remain owned by the user.
func versionManifest(raw []byte) (map[string]json.RawMessage, error) {
	var manifest map[string]json.RawMessage
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, err
	}
	if manifest == nil {
		return nil, errors.New("profile package.json is not an object")
	}
	return manifest, nil
}
func readVersionFile(root *os.Root, name string, optional bool) ([]byte, error) {
	f, err := openVersionFile(root, name, os.O_RDONLY)
	if optional && errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > versionInputLimit {
		return nil, fmt.Errorf("unsupported snapshot input: %s", name)
	}
	data, err := io.ReadAll(io.LimitReader(f, versionInputLimit+1))
	if len(data) > versionInputLimit {
		return nil, fmt.Errorf("snapshot input is too large: %s", name)
	}
	return data, err
}
func (PluginCommands) CaptureVersions(ctx context.Context, path string) (dshmanager.ProfileVersionInput, error) {
	var out dshmanager.ProfileVersionInput
	if err := ctx.Err(); err != nil {
		return out, err
	}
	root, err := openVersionProfile(path)
	if err != nil {
		return out, err
	}
	defer root.Close()
	raw, err := readVersionFile(root, "package.json", false)
	if err != nil {
		return out, err
	}
	manifest, err := versionManifest(raw)
	if err != nil {
		return out, err
	}
	var dsh struct {
		Profile struct {
			Bundles []string `json:"bundles"`
		} `json:"profile"`
	}
	if b := manifest["dsh"]; len(b) > 0 {
		if err = json.Unmarshal(b, &dsh); err != nil {
			return out, err
		}
	}
	out.Bundles = dsh.Profile.Bundles
	for _, field := range []string{"peerDependencies", "peerDependenciesMeta", "overrides", "resolutions", "pnpm"} {
		if b := manifest[field]; len(b) > 0 && string(b) != "{}" && string(b) != "null" {
			out.Unavailable = "This profile needs additional dependency configuration"
		}
	}
	out.Plugins = []dshmanager.VersionPlugin{}
	seen := map[string]bool{}
	for _, field := range []string{"dependencies", "devDependencies", "optionalDependencies"} {
		var deps map[string]string
		if b := manifest[field]; len(b) > 0 {
			if err = json.Unmarshal(b, &deps); err != nil {
				return out, err
			}
		}
		for name, spec := range deps {
			if strings.Contains(name, "..") || strings.ContainsAny(name, "\\:") || strings.HasPrefix(name, "/") {
				return out, errors.New("invalid dependency name")
			}
			if seen[name] {
				continue
			}
			seen[name] = true
			// The supported DSH hoisted layout stores packages inside the profile.
			// safeopen rejects external traversal (including Windows junctions).
			data, e := readVersionFile(root, filepath.ToSlash(filepath.Join("node_modules", name, "package.json")), false)
			if e != nil {
				if field == "optionalDependencies" {
					continue
				}
				return out, fmt.Errorf("installed version of %s is unavailable", name)
			}
			var pkg struct {
				Version string `json:"version"`
			}
			if json.Unmarshal(data, &pkg) != nil || pkg.Version == "" {
				return out, fmt.Errorf("invalid installed version of %s", name)
			}
			out.Plugins = append(out.Plugins, dshmanager.VersionPlugin{Name: name, Version: pkg.Version, Source: spec, Group: field})
			if strings.HasPrefix(spec, "file:") || strings.HasPrefix(spec, "link:") || strings.Contains(spec, "git") || strings.HasPrefix(spec, ".") || strings.Contains(spec, "://") || strings.HasPrefix(spec, "workspace:") {
				out.Unavailable = "This snapshot needs the original local or Git plugin source"
			}
		}
	}
	sort.Slice(out.Plugins, func(i, j int) bool { return out.Plugins[i].Name < out.Plugins[j].Name })
	lock, err := readVersionFile(root, "pnpm-lock.yaml", true)
	if err != nil {
		return out, err
	}
	out.Lock = string(lock)
	if out.Lock == "" && len(out.Plugins) > 0 {
		out.Unavailable = "The profile has no pnpm lockfile; install its dependencies before recording"
	}
	workspace, err := readVersionFile(root, "pnpm-workspace.yaml", true)
	if err != nil {
		return out, err
	}
	out.Workspace = string(workspace)
	if privateVersionInput(lock) || privateVersionInput(workspace) {
		return out, errors.New("keep dependency credentials outside snapshot inputs")
	}
	list, _ := json.Marshal([]any{out.Plugins, out.Bundles})
	if privateVersionInput(list) {
		return out, errors.New("keep dependency credentials outside snapshot inputs")
	}
	lower := strings.ToLower(out.Workspace + string(list) + out.Lock)
	for _, secret := range []string{"_auth", "password:", "token:", "token=", "://"} {
		// Registry tarball URLs in a lock are normal; embedded URL credentials are not.
		if secret != "://" && strings.Contains(lower, secret) {
			return out, errors.New("dependency inputs contain credentials; keep credentials outside snapshot inputs")
		}
	}
	if strings.Contains(lower, "patcheddependencies") || strings.Contains(lower, "configdependencies") || strings.Contains(lower, "pnpmfile") {
		out.Unavailable = "This profile needs additional dependency configuration files"
	}
	modules, err := readVersionFile(root, "node_modules/.modules.yaml", true)
	if err != nil {
		return out, err
	}
	var meta struct {
		PackageManager string `yaml:"packageManager"`
	}
	_ = yaml.Unmarshal(modules, &meta)
	out.PackageManager = meta.PackageManager
	if len(out.Plugins) == 0 {
		out.PackageManager = "pnpm"
	}
	if out.PackageManager == "" {
		out.PackageManager = "pnpm"
	}
	return out, nil
}

func (PluginCommands) CheckVersionProfile(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	root, err := openVersionProfile(path)
	if err != nil {
		return err
	}
	defer root.Close()
	raw, err := readVersionFile(root, "package.json", false)
	if err != nil {
		return err
	}
	_, err = versionManifest(raw)
	return err
}

func writeVersionFile(root *os.Root, name string, data []byte) error {
	// os.Root confines both temporary publication and replacement to the selected
	// profile. Replacing the directory entry does not follow an existing symlink.
	temp := ".dsh-work-version-" + strings.ReplaceAll(name, ".", "-")
	f, err := openVersionFile(root, temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if errors.Is(err, os.ErrExist) {
		if err = root.Remove(temp); err != nil {
			return err
		}
		f, err = openVersionFile(root, temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	}
	if err != nil {
		return err
	}
	defer root.Remove(temp)
	_, writeErr := f.Write(data)
	syncErr := f.Sync()
	closeErr := f.Close()
	if err = errors.Join(writeErr, syncErr, closeErr); err != nil {
		return err
	}
	return root.Rename(temp, name)
}
func (PluginCommands) ApplyVersions(ctx context.Context, path string, input dshmanager.ProfileVersionInput) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	list, _ := json.Marshal([]any{input.Plugins, input.Bundles})
	for _, data := range [][]byte{list, []byte(input.Lock), []byte(input.Workspace)} {
		if len(data) > versionInputLimit || privateVersionInput(data) {
			return errors.New("snapshot dependency inputs are invalid or contain credentials")
		}
	}
	root, err := openVersionProfile(path)
	if err != nil {
		return err
	}
	defer root.Close()
	raw, err := readVersionFile(root, "package.json", false)
	if err != nil {
		return err
	}
	current, err := versionManifest(raw)
	if err != nil {
		return err
	}
	groups := map[string]map[string]string{}
	for _, plugin := range input.Plugins {
		if !validPackageSpec(plugin.Name) || plugin.Version == "" || plugin.Source == "" {
			return errors.New("invalid recorded plugin")
		}
		switch plugin.Group {
		case "dependencies", "devDependencies", "optionalDependencies":
		default:
			return errors.New("invalid recorded dependency group")
		}
		if groups[plugin.Group] == nil {
			groups[plugin.Group] = map[string]string{}
		}
		groups[plugin.Group][plugin.Name] = plugin.Source
	}
	for _, key := range dependencyFields {
		delete(current, key)
	}
	// Keep original specifiers for frozen-lockfile matching; the lock, checked
	// against installed exact versions after installation, controls resolution.
	for key, dependencies := range groups {
		current[key], _ = json.Marshal(dependencies)
	}
	if len(groups) == 0 {
		current["dependencies"] = json.RawMessage(`{}`)
	}
	var dsh map[string]json.RawMessage
	_ = json.Unmarshal(current["dsh"], &dsh)
	if dsh == nil {
		dsh = map[string]json.RawMessage{}
	}
	var profile map[string]json.RawMessage
	_ = json.Unmarshal(dsh["profile"], &profile)
	if profile == nil {
		profile = map[string]json.RawMessage{}
	}
	profile["bundles"], _ = json.Marshal(input.Bundles)
	dsh["profile"], _ = json.Marshal(profile)
	current["dsh"], _ = json.Marshal(dsh)
	data, err := json.MarshalIndent(current, "", "  ")
	if err != nil {
		return err
	}
	if err = writeVersionFile(root, "package.json", data); err != nil {
		return err
	}
	for name, value := range map[string]string{"pnpm-lock.yaml": input.Lock, "pnpm-workspace.yaml": input.Workspace} {
		if value == "" {
			if err = root.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		} else if err = writeVersionFile(root, name, []byte(value)); err != nil {
			return err
		}
	}
	return nil
}
func (PluginCommands) ResetVersions(ctx context.Context, path string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := openVersionProfile(path)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	raw, err := readVersionFile(root, "package.json", false)
	if err != nil {
		return nil, err
	}
	manifest, err := versionManifest(raw)
	if err != nil {
		return nil, err
	}
	names := map[string]bool{}
	for _, key := range []string{"dependencies", "devDependencies", "optionalDependencies"} {
		var deps map[string]json.RawMessage
		if len(manifest[key]) > 0 {
			if err = json.Unmarshal(manifest[key], &deps); err != nil {
				return nil, err
			}
		}
		for name := range deps {
			names[name] = true
		}
	}
	out := []string{}
	for name := range names {
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}
func (PluginCommands) ForceInstall(profile string, frozen bool) ([]string, error) {
	if !validProfileName(profile) {
		return nil, errors.New("invalid profile")
	}
	args := []string{"plugin", "--profile", profile, "install", "--force", "--config.optimistic-repeat-install=false"}
	if frozen {
		args = append(args, "--frozen-lockfile")
	}
	return args, nil
}
func (PluginCommands) RemoveVersions(profile string, names []string) ([]string, error) {
	if !validProfileName(profile) {
		return nil, errors.New("invalid profile")
	}
	args := []string{"plugin", "--profile", profile, "remove"}
	for _, name := range names {
		if !validPackageSpec(name) {
			return nil, errors.New("invalid package")
		}
		args = append(args, name)
	}
	return append(args, "--config.ignore-scripts=true", "--config.optimistic-repeat-install=false"), nil
}
