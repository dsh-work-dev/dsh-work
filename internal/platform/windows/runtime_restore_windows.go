//go:build windows

package windows

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/local/dsh-work/internal/acquisition"
	"github.com/local/dsh-work/internal/dshmanager"
)

// ForceInstall repairs a managed version in place. Only the package manager
// changes node_modules; a version check never short-circuits this operation.
func (i *RuntimeInstaller) ForceInstall(ctx context.Context, version string, node dshmanager.ResolvedNode) (dshmanager.RuntimeInfo, error) {
	if !runtimeVersionPattern.MatchString(version) {
		return dshmanager.RuntimeInfo{}, errors.New("invalid DSH version")
	}
	i.initializeDefaults()
	root, err := managedStoreRoot(i.storeRoot)
	if err != nil {
		return dshmanager.RuntimeInfo{}, err
	}
	destination := filepath.Join(root, "dsh-"+version)
	if !isDirectManagedChild(root, destination) {
		return dshmanager.RuntimeInfo{}, errors.New("invalid managed runtime directory")
	}
	if info, e := os.Lstat(destination); e == nil && info.Mode()&os.ModeSymlink != 0 {
		return dshmanager.RuntimeInfo{}, errors.New("managed runtime directory is linked")
	}
	if err = os.MkdirAll(destination, 0700); err != nil {
		return dshmanager.RuntimeInfo{}, err
	}
	if err = validateRestoreRuntimePaths(destination); err != nil {
		return dshmanager.RuntimeInfo{}, err
	}
	toolchain := runtimeToolchain{kind: dshmanager.RuntimeToolchainSystemNPM, nodePath: node.NodePath, version: node.Version, env: node.ChildEnvironment, packageManagerPath: node.NPMPath}
	if node.PNPMPath != "" {
		toolchain.kind = dshmanager.RuntimeToolchainSystemPNPM
		toolchain.packageManagerPath = node.PNPMPath
	}
	if toolchain.packageManagerPath == "" {
		toolchain.packageManagerPath, err = i.lookup("pnpm")
		toolchain.kind = dshmanager.RuntimeToolchainSystemPNPM
		if err != nil {
			return dshmanager.RuntimeInfo{}, errors.New("package manager is unavailable")
		}
	}
	// Materialize a package manifest for existing npm --no-save installations.
	// A package-manager remove followed by exact forced add also repairs broken
	// files when a hoisted linker skips same-version packages under --force.
	manifest := filepath.Join(destination, "package.json")
	if _, e := os.Stat(manifest); errors.Is(e, os.ErrNotExist) {
		if e = os.WriteFile(manifest, []byte(`{"private":true}`), 0600); e != nil {
			return dshmanager.RuntimeInfo{}, e
		}
	}
	if _, e := os.Stat(filepath.Join(destination, "node_modules", "@deepseek-ai", "dsh")); e == nil {
		args := []string{"uninstall", "--prefix", destination, "--ignore-scripts", "--no-audit", "--fund=false", "@deepseek-ai/dsh"}
		if toolchain.kind == dshmanager.RuntimeToolchainSystemPNPM {
			// pnpm remove requires a declaration; add it without touching the package tree.
			if e = ensureRestoreRuntimeManifest(manifest, version); e != nil {
				return dshmanager.RuntimeInfo{}, e
			}
			// Acquisition publishes a staged package tree. pnpm may retain the
			// staging virtual-store path in its own metadata, so remove refuses
			// to run until pnpm reconciles that tree at its final location.
			// Let pnpm repair its metadata; never rewrite its store paths ourselves.
			reconcile := []string{"install", "--dir", destination, "--force", "--config.ignore-scripts=true", "--config.optimistic-repeat-install=false"}
			if _, e = i.executor.Run(ctx, toolchain.packageManagerPath, reconcile, toolchain.env, destination); e != nil {
				return dshmanager.RuntimeInfo{}, e
			}
			args = []string{"remove", "--dir", destination, "@deepseek-ai/dsh", "--config.ignore-scripts=true", "--config.optimistic-repeat-install=false"}
		}
		if _, e = i.executor.Run(ctx, toolchain.packageManagerPath, args, toolchain.env, destination); e != nil {
			return dshmanager.RuntimeInfo{}, e
		}
	}
	args := packageInstallArgs(toolchain.kind, destination, version, i.officialRegistry)
	args = append(args, "--force")
	if toolchain.kind == dshmanager.RuntimeToolchainSystemPNPM {
		args = append(args, "--config.optimistic-repeat-install=false")
	}
	installSource := dshmanager.RuntimeArtifactSourceOfficial
	result, err := i.executor.Run(ctx, toolchain.packageManagerPath, args, toolchain.env, destination)
	if err != nil && i.mirrorRegistry != "" && classifyPackageManagerFailure(result, err).Kind == acquisition.FailureReachability {
		installSource = dshmanager.RuntimeArtifactSourceMirror
		args = packageInstallArgs(toolchain.kind, destination, version, i.mirrorRegistry)
		args = append(args, "--force")
		if toolchain.kind == dshmanager.RuntimeToolchainSystemPNPM {
			args = append(args, "--config.optimistic-repeat-install=false")
		}
		_, err = i.executor.Run(ctx, toolchain.packageManagerPath, args, toolchain.env, destination)
	}
	if err != nil {
		return dshmanager.RuntimeInfo{}, err
	}
	executable := runtimeExecutablePath(destination)
	if err = validateRestoreRuntimePaths(destination); err != nil {
		return dshmanager.RuntimeInfo{}, err
	}
	actual, err := i.verifyDSH(ctx, executable, version, toolchain, nil)
	if err != nil {
		return dshmanager.RuntimeInfo{}, err
	}
	if actual != version {
		return dshmanager.RuntimeInfo{}, errors.New("restored DSH version mismatch")
	}
	return dshmanager.RuntimeInfo{ID: "dsh-" + version, Version: version, Path: executable, Source: dshmanager.RuntimeSourceManaged, Toolchain: toolchain.kind, ToolchainPath: filepath.Dir(node.NodePath), InstallSource: installSource, Installed: true, Removable: true}, nil
}

func validateRestoreRuntimePaths(destination string) error {
	root, err := filepath.EvalSymlinks(destination)
	if err != nil {
		return err
	}
	// Check control files and package-manager roots before any in-place writes.
	// Internal pnpm package links are allowed when they resolve inside this runtime.
	for _, name := range []string{"package.json", "package-lock.json", "pnpm-lock.yaml", "pnpm-workspace.yaml", "node_modules", "node_modules/.pnpm", "node_modules/.modules.yaml", "node_modules/.bin", "node_modules/.bin/dsh", "node_modules/.bin/dsh.cmd", "node_modules/.bin/dsh.exe", "node_modules/.bin/dsh.ps1", "node_modules/@deepseek-ai", "node_modules/@deepseek-ai/dsh"} {
		path := filepath.Join(destination, filepath.FromSlash(name))
		if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return err
		}
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil || !isWithinDirectory(root, resolved) || resolved == root {
			return errors.New("runtime dependency path resolves outside its managed directory")
		}
	}
	return nil
}

func ensureRestoreRuntimeManifest(path, version string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var manifest map[string]json.RawMessage
	if err = json.Unmarshal(data, &manifest); err != nil {
		return err
	}
	if manifest == nil {
		return errors.New("invalid runtime manifest")
	}
	deps := map[string]string{"@deepseek-ai/dsh": version}
	manifest["dependencies"], _ = json.Marshal(deps)
	data, err = json.Marshal(manifest)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}
