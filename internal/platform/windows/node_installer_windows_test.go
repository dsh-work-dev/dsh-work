//go:build windows

package windows

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/local/dsh-work/internal/dshmanager"
)

func TestManagedNodeInstallerIsIdempotentAndDoesNotActivate(t *testing.T) {
	downloader := &nodeArchiveDownloaderFake{data: nodeArchive(t, false)}
	installer := NewManagedNodeInstaller(
		&managedNodeCommandFake{}, t.TempDir(),
		WithManagedNodeDownloader(downloader),
		WithManagedNodeSources("https://official.example/node", "https://mirror.example/node"),
	)
	architecture := runtime.GOARCH
	if architecture == "amd64" {
		architecture = "x64"
	}
	release := dshmanager.NodeReleaseInfo{
		Version: "v24.20.0", Platform: "windows", Architecture: architecture,
		Filename: "node-v24.20.0-win-" + architecture + ".zip", SHA256: strings.Repeat("a", 64),
		Source: dshmanager.RuntimeArtifactSourceOfficial,
	}

	first, err := installer.Install(context.Background(), release, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := installer.Install(context.Background(), release, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || first.Version != release.Version || !first.Verified || !first.Removable {
		t.Fatalf("installations = %#v / %#v", first, second)
	}
	if len(downloader.urls) != 1 {
		t.Fatalf("downloads = %#v, want one idempotent download", downloader.urls)
	}
	if downloader.urls[0] != "https://official.example/node/v24.20.0/"+release.Filename {
		t.Fatalf("download URL = %q", downloader.urls[0])
	}
	if filepath.Base(first.NodePath) != "node.exe" || filepath.Base(first.NPMPath) != "npm.cmd" {
		t.Fatalf("installed paths = %#v", first)
	}
	if err := installer.Remove(context.Background(), first); err != nil {
		t.Fatalf("remove installed Node: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(first.NodePath)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Node installation remains after removal: %v", err)
	}
}

func TestManagedNodeInstallerFallsBackOnlyAfterReachabilityFailure(t *testing.T) {
	downloader := &nodeArchiveDownloaderFake{
		data: nodeArchive(t, false), errors: []error{errors.New("network timeout"), nil},
	}
	installer := NewManagedNodeInstaller(
		&managedNodeCommandFake{}, t.TempDir(),
		WithManagedNodeDownloader(downloader),
		WithManagedNodeSources("https://official.example/node", "https://mirror.example/node"),
	)
	architecture := runtime.GOARCH
	if architecture == "amd64" {
		architecture = "x64"
	}
	release := dshmanager.NodeReleaseInfo{
		Version: "v24.20.0", Platform: "windows", Architecture: architecture,
		Filename: "node-v24.20.0-win-" + architecture + ".zip", SHA256: strings.Repeat("b", 64),
		Source: dshmanager.RuntimeArtifactSourceOfficial,
	}
	installed, err := installer.Install(context.Background(), release, nil)
	if err != nil {
		t.Fatal(err)
	}
	if installed.InstallSource != dshmanager.RuntimeArtifactSourceMirror || len(downloader.urls) != 2 {
		t.Fatalf("fallback result = %#v urls=%#v", installed, downloader.urls)
	}
}
