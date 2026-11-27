//go:build darwin && cgo

package mac

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/local/dsh-work/internal/acquisition"
	"github.com/local/dsh-work/internal/dshadapter"
	"github.com/local/dsh-work/internal/dshmanager"
	"github.com/local/dsh-work/internal/lifecycle"
	"golang.org/x/sys/unix"
)

const macNodeManifestFile = ".dsh-work-node.json"

var macNodeVersion = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)

type ManagedNodeInstaller struct {
	root    string
	client  acquisition.VerifiedHTTPDownloader
	sources []acquisition.SourceCandidate
}

func NewManagedNodeInstaller(root string) ManagedNodeInstaller {
	return ManagedNodeInstaller{root: root, sources: []acquisition.SourceCandidate{
		{Route: acquisition.RouteOfficial, Location: "https://nodejs.org/dist/"},
		{Route: acquisition.RouteMirror, Location: "https://npmmirror.com/mirrors/node/"},
	}}
}

type macNodeManifest struct {
	Version       string            `json:"version"`
	ArchiveSHA256 string            `json:"sha256"`
	NodeSHA256    string            `json:"nodeSHA256"`
	Route         acquisition.Route `json:"route"`
}

func macNodeArch() string {
	if runtime.GOARCH == "amd64" {
		return "x64"
	}
	return runtime.GOARCH
}

func validSHA256(value string) bool {
	b, err := hex.DecodeString(value)
	return err == nil && len(b) == sha256.Size
}

func (i ManagedNodeInstaller) Install(ctx context.Context, release dshmanager.NodeReleaseInfo, observer dshmanager.RuntimeInstallObserver) (dshmanager.NodeInstallationInfo, error) {
	arch := macNodeArch()
	if release.Platform != "darwin" || release.Architecture != arch || !macNodeVersion.MatchString(release.Version) ||
		release.Filename != "node-"+release.Version+"-darwin-"+arch+".tar.gz" || !validSHA256(release.SHA256) {
		return dshmanager.NodeInstallationInfo{}, errors.New("Mac Node release identity is invalid")
	}
	root, directory, err := i.nodeDirectory(release.Version, true)
	if err != nil {
		return dshmanager.NodeInstallationInfo{}, err
	}
	existing, err := readMacNode(directory)
	if err == nil && strings.EqualFold(existing.SHA256, release.SHA256) {
		return existing, nil
	}
	if _, statErr := os.Lstat(directory); !errors.Is(statErr, os.ErrNotExist) {
		return dshmanager.NodeInstallationInfo{}, errors.New("managed Mac Node destination already exists or cannot be inspected")
	}
	stage, err := macStoreDirectory(root, ".staging", true)
	if err != nil {
		return dshmanager.NodeInstallationInfo{}, err
	}
	sources := append([]acquisition.SourceCandidate(nil), i.sources...)
	for index := range sources {
		sources[index].Location += release.Version + "/" + release.Filename
	}
	result, err := acquisition.Acquire(ctx, acquisition.Request{
		OperationID: "install-node-" + release.Version, StagingRoot: stage, StagePrefix: "node-",
		Identity:   acquisition.ArtifactIdentity{Kind: acquisition.ArtifactNode, Name: "node", Version: release.Version, Platform: "darwin", Architecture: arch, Filename: release.Filename},
		Candidates: sources,
	}, acquisition.VerifiedArchive{Downloader: i.client, SHA256: release.SHA256, Report: func(route acquisition.Route, received, total int64, known bool) {
		if observer != nil {
			source := lifecycle.RuntimePreparationSourceOfficial
			if route == acquisition.RouteMirror {
				source = lifecycle.RuntimePreparationSourceMirror
			}
			observer(lifecycle.RuntimePreparation{State: lifecycle.RuntimePreparationAcquiringNode, Operation: lifecycle.RuntimePreparationOperationDownloadNode,
				TargetVersion: release.Version, Toolchain: string(dshmanager.RuntimeToolchainManagedNodeNPM), Source: source,
				ReceivedBytes: received, TotalBytes: total, HasTotal: known, CanCancel: true})
		}
	}}, nil)
	if err != nil {
		return dshmanager.NodeInstallationInfo{}, err
	}
	defer os.RemoveAll(result.StagingPath)
	extracted := filepath.Join(result.StagingPath, "extracted")
	if err = os.Mkdir(extracted, 0700); err != nil {
		return dshmanager.NodeInstallationInfo{}, err
	}
	// Darwin's bsdtar rejects traversal and writes through symlinks by default.
	// The file is already SHA-256 verified against the selected Node release.
	if err = exec.CommandContext(ctx, "/usr/bin/tar", "-xzf", result.PayloadPath, "--strip-components", "1", "--no-same-owner", "--no-same-permissions", "-C", extracted).Run(); err != nil {
		return dshmanager.NodeInstallationInfo{}, fmt.Errorf("extract Mac Node release: %w", err)
	}
	node, npm, err := macNodeExecutables(extracted)
	if err != nil {
		return dshmanager.NodeInstallationInfo{}, err
	}
	if err = verifyMacNode(ctx, node, npm, release.Version); err != nil {
		return dshmanager.NodeInstallationInfo{}, err
	}
	digest, err := macExecutableSHA256(node)
	if err != nil {
		return dshmanager.NodeInstallationInfo{}, err
	}
	manifest := macNodeManifest{Version: release.Version, ArchiveSHA256: strings.ToLower(release.SHA256), NodeSHA256: digest, Route: result.Route}
	body, err := json.Marshal(manifest)
	if err != nil {
		return dshmanager.NodeInstallationInfo{}, err
	}
	if err = os.WriteFile(filepath.Join(extracted, macNodeManifestFile), body, 0600); err != nil {
		return dshmanager.NodeInstallationInfo{}, err
	}
	if err = ctx.Err(); err != nil {
		return dshmanager.NodeInstallationInfo{}, err
	}
	if err = unix.RenameatxNp(unix.AT_FDCWD, extracted, unix.AT_FDCWD, directory, unix.RENAME_EXCL); err != nil {
		if existing, e := readMacNode(directory); e == nil && existing.SHA256 == manifest.ArchiveSHA256 {
			return existing, nil
		}
		return dshmanager.NodeInstallationInfo{}, fmt.Errorf("publish Mac Node installation: %w", err)
	}
	return macNodeInstallation(directory, manifest), nil
}

func (i ManagedNodeInstaller) nodeDirectory(version string, create bool) (string, string, error) {
	if !macNodeVersion.MatchString(version) || !filepath.IsAbs(i.root) {
		return "", "", errors.New("invalid managed Mac Node path")
	}
	if create {
		if err := os.MkdirAll(i.root, 0700); err != nil {
			return "", "", err
		}
	}
	root, err := filepath.EvalSymlinks(i.root)
	if err != nil {
		return "", "", err
	}
	parent, err := macStoreDirectory(root, filepath.Join("toolchains", "node", version), create)
	if err != nil {
		return "", "", err
	}
	return root, filepath.Join(parent, "darwin-"+macNodeArch()), nil
}

func macStoreDirectory(root, relative string, create bool) (string, error) {
	// Open each directory by its expected physical name before touching the
	// next component. In particular staging and the store may not be symlinks.
	path := root
	for _, part := range strings.Split(relative, string(os.PathSeparator)) {
		path = filepath.Join(path, part)
		if create {
			if err := os.Mkdir(path, 0700); err != nil && !errors.Is(err, os.ErrExist) {
				return "", err
			}
		}
		if info, err := os.Lstat(path); err != nil || !info.IsDir() {
			return "", errors.New("managed Mac Node directory is unavailable")
		}
	}
	return path, nil
}

func macNodeExecutables(directory string) (node, npm string, err error) {
	node = filepath.Join(directory, "bin", "node")
	npm = filepath.Join(directory, "bin", "npm")
	nodeResolved, err := filepath.EvalSymlinks(node)
	if err != nil || nodeResolved != node {
		return "", "", errors.New("invalid Mac Node executable")
	}
	npmResolved, err := filepath.EvalSymlinks(npm)
	if err != nil || npmResolved != filepath.Join(directory, "lib", "node_modules", "npm", "bin", "npm-cli.js") {
		return "", "", errors.New("invalid Mac npm executable")
	}
	for _, file := range []string{node, npmResolved} {
		if info, e := os.Stat(file); e != nil || !info.Mode().IsRegular() {
			return "", "", errors.New("Mac runtime executable is unavailable")
		}
	}
	return node, npm, nil
}

func verifyMacNode(ctx context.Context, node, npm, version string) error {
	// The package-manager probe loads only the npm entry from this verified archive.
	for _, executable := range []string{node, npm} {
		cmd := exec.CommandContext(ctx, executable, "--version")
		cmd.Env = []string{"PATH=" + filepath.Dir(node) + ":/usr/bin:/bin", "HOME=" + os.Getenv("HOME"), "TMPDIR=" + os.TempDir()}
		out, err := cmd.Output()
		actual := dshadapter.ParseVersion(string(out))
		if err != nil || actual == "" || (executable == node && actual != strings.TrimPrefix(version, "v")) {
			return errors.New("Mac Node archive failed its Node/npm version check")
		}
	}
	return nil
}

func macExecutableSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err = io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func macNodeInstallation(directory string, manifest macNodeManifest) dshmanager.NodeInstallationInfo {
	arch := macNodeArch()
	source := dshmanager.RuntimeArtifactSourceOfficial
	if manifest.Route == acquisition.RouteMirror {
		source = dshmanager.RuntimeArtifactSourceMirror
	}
	return dshmanager.NodeInstallationInfo{ID: "node-" + manifest.Version + "-darwin-" + arch, Version: manifest.Version,
		Platform: "darwin", Architecture: arch, NodePath: filepath.Join(directory, "bin", "node"), NPMPath: filepath.Join(directory, "bin", "npm"),
		Ownership: dshmanager.NodeOwnershipManaged, InstallSource: source, SHA256: manifest.ArchiveSHA256, Installed: true, Verified: true, Removable: true}
}

func readMacNode(directory string) (dshmanager.NodeInstallationInfo, error) {
	var manifest macNodeManifest
	root, err := os.OpenRoot(directory)
	if err != nil {
		return dshmanager.NodeInstallationInfo{}, err
	}
	defer root.Close()
	file, err := root.Open(macNodeManifestFile)
	if err != nil {
		return dshmanager.NodeInstallationInfo{}, err
	}
	defer file.Close()
	if err = json.NewDecoder(io.LimitReader(file, 4096)).Decode(&manifest); err != nil || manifest.Version != filepath.Base(filepath.Dir(directory)) ||
		!validSHA256(manifest.ArchiveSHA256) || !validSHA256(manifest.NodeSHA256) || (manifest.Route != acquisition.RouteOfficial && manifest.Route != acquisition.RouteMirror) {
		return dshmanager.NodeInstallationInfo{}, errors.New("invalid managed Mac Node manifest")
	}
	node, _, err := macNodeExecutables(directory)
	if err != nil {
		return dshmanager.NodeInstallationInfo{}, err
	}
	digest, err := macExecutableSHA256(node)
	if err != nil || digest != manifest.NodeSHA256 {
		return dshmanager.NodeInstallationInfo{}, errors.New("managed Mac Node executable failed integrity verification")
	}
	return macNodeInstallation(directory, manifest), nil
}

func (i ManagedNodeInstaller) Remove(ctx context.Context, installation dshmanager.NodeInstallationInfo) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, directory, err := i.nodeDirectory(installation.Version, false)
	if err != nil {
		return err
	}
	actual, err := readMacNode(directory)
	if err != nil || actual != installation {
		return errors.New("managed Mac Node installation identity changed")
	}
	parent, err := os.OpenRoot(filepath.Dir(directory))
	if err != nil {
		return err
	}
	defer parent.Close()
	return parent.RemoveAll(filepath.Base(directory))
}
