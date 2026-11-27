//go:build darwin && cgo

package mac

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/local/dsh-work/internal/dshadapter"
	"github.com/local/dsh-work/internal/dshmanager"
	"github.com/local/dsh-work/internal/platform/automationpackages"
	"golang.org/x/sys/unix"
)

const defaultProfilePNPM = "11.25.0"
const profilePNPMManifest = ".dsh-work-pnpm.json"

type profilePNPMInstallation struct {
	Version      string
	EntrySHA256  string
	BundleSHA256 string
}

func profilePNPMVersion(recorded string) (string, error) {
	if recorded == "" || recorded == "pnpm" {
		return defaultProfilePNPM, nil
	}
	version, ok := strings.CutPrefix(recorded, "pnpm@")
	if !ok || !macDSHVersion.MatchString(version) || len(version) > 80 {
		return "", errors.New("invalid pnpm version in the DSH profile")
	}
	return version, nil
}

func (r *Runtime) PrepareProfilePackageManager(ctx context.Context, node dshmanager.ResolvedNode, recorded string) error {
	selected, err := r.nodeForEnvironment(node.ChildEnvironment)
	if err != nil {
		return err
	}
	if selected != node.NodePath {
		return errors.New("Mac profile package manager needs the selected Node")
	}
	entry, err := r.profilePNPM(ctx, selected, recorded)
	if err != nil {
		return err
	}
	version, err := profilePNPMVersion(recorded)
	if err != nil {
		return err
	}
	return probeProfilePNPM(ctx, selected, entry, version)
}

func (r *Runtime) profilePNPM(ctx context.Context, node, recorded string) (string, error) {
	version, err := profilePNPMVersion(recorded)
	if err != nil {
		return "", err
	}
	r.mu.RLock()
	store := r.dshStore
	r.mu.RUnlock()
	if !filepath.IsAbs(store) {
		return "", errors.New("managed Mac package manager store is unavailable")
	}
	if err = os.MkdirAll(store, 0700); err != nil {
		return "", err
	}
	store, err = filepath.EvalSymlinks(store)
	if err != nil {
		return "", err
	}
	root, err := macStoreDirectory(store, "profile-tools", true)
	if err != nil {
		return "", err
	}
	destination := filepath.Join(root, "pnpm-"+version)
	if entry, err := verifiedProfilePNPM(destination, version); err == nil {
		return entry, nil
	}
	if _, err = os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
		return "", errors.New("managed Mac pnpm installation is damaged")
	}
	npm := filepath.Join(filepath.Dir(node), "npm")
	if node == r.node {
		npm, err = exec.LookPath("npm")
		if err != nil {
			return "", err
		}
	}
	stage, err := os.MkdirTemp(root, ".staging-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(stage)
	result, err := runMacNPM(ctx, npm, []string{"install", "--ignore-scripts", "--no-audit", "--no-fund", "--no-save",
		"--package-lock=false", "--registry", "https://registry.npmjs.org/", "pnpm@" + version}, stage, filepath.Dir(node))
	if err != nil {
		return "", fmt.Errorf("install managed pnpm %s: %w", version, automationpackages.ClassifyPackageManagerFailure(result, err))
	}
	entry, manifest, err := readProfilePNPMPackage(stage, version)
	if err != nil {
		return "", err
	}
	if err = probeProfilePNPM(ctx, node, entry, version); err != nil {
		return "", err
	}
	body, err := json.Marshal(manifest)
	if err != nil {
		return "", err
	}
	if err = os.WriteFile(filepath.Join(stage, profilePNPMManifest), body, 0600); err != nil {
		return "", err
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	if err = unix.RenameatxNp(unix.AT_FDCWD, stage, unix.AT_FDCWD, destination, unix.RENAME_EXCL); err != nil && !errors.Is(err, unix.EEXIST) {
		return "", err
	}
	return verifiedProfilePNPM(destination, version)
}

func probeProfilePNPM(ctx context.Context, node, entry, version string) error {
	cmd := exec.CommandContext(ctx, node, entry, "--config.manage-package-manager-versions=false", "--config.pm-on-fail=ignore", "--version")
	cmd.Dir = filepath.Dir(entry)
	cmd.Env = []string{"PATH=" + filepath.Dir(node) + ":/usr/bin:/bin", "HOME=" + os.Getenv("HOME"), "TMPDIR=" + os.TempDir()}
	output, err := cmd.Output()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil || dshadapter.ParseVersion(string(output)) != version {
		return errors.New("managed Mac pnpm reported an unexpected version")
	}
	return nil
}

func verifiedProfilePNPM(directory, version string) (string, error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return "", err
	}
	defer root.Close()
	file, err := root.Open(profilePNPMManifest)
	if err != nil {
		return "", err
	}
	defer file.Close()
	var expected profilePNPMInstallation
	if err = json.NewDecoder(io.LimitReader(file, 4096)).Decode(&expected); err != nil {
		return "", err
	}
	entry, actual, err := readProfilePNPMPackage(directory, version)
	if err != nil {
		return "", err
	}
	if actual != expected {
		return "", errors.New("managed Mac pnpm failed integrity verification")
	}
	return entry, nil
}

func readProfilePNPMPackage(directory, version string) (string, profilePNPMInstallation, error) {
	var manifest profilePNPMInstallation
	packageDir := filepath.Join(directory, "node_modules", "pnpm")
	resolved, err := filepath.EvalSymlinks(packageDir)
	if err != nil || resolved != packageDir {
		return "", manifest, errors.New("managed Mac pnpm package directory is invalid")
	}
	root, err := os.OpenRoot(packageDir)
	if err != nil {
		return "", manifest, err
	}
	defer root.Close()
	file, err := root.Open("package.json")
	if err != nil {
		return "", manifest, err
	}
	defer file.Close()
	var pkg struct {
		Name, Version string
		Bin           map[string]string
	}
	if err = json.NewDecoder(io.LimitReader(file, 1<<20)).Decode(&pkg); err != nil || pkg.Name != "pnpm" || pkg.Version != version {
		return "", manifest, errors.New("managed Mac pnpm package identity is invalid")
	}
	var bundle string
	switch pkg.Bin["pnpm"] {
	case "bin/pnpm.mjs":
		bundle = "dist/pnpm.mjs"
	case "bin/pnpm.cjs":
		bundle = "dist/pnpm.cjs"
	default:
		return "", manifest, errors.New("managed Mac pnpm package has an unsupported entry point")
	}
	entry := filepath.Join(packageDir, pkg.Bin["pnpm"])
	for _, path := range []string{entry, filepath.Join(packageDir, bundle)} {
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil || resolved != path {
			return "", manifest, errors.New("managed Mac pnpm package contains an invalid executable entry")
		}
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			return "", manifest, errors.New("managed Mac pnpm package entry is unavailable")
		}
	}
	manifest.Version = version
	manifest.EntrySHA256, err = macExecutableSHA256(entry)
	if err != nil {
		return "", manifest, err
	}
	manifest.BundleSHA256, err = macExecutableSHA256(filepath.Join(packageDir, bundle))
	return entry, manifest, err
}
