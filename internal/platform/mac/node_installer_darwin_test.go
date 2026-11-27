//go:build darwin && cgo

package mac

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/acquisition"
	"github.com/local/dsh-work/internal/dshmanager"
)

func TestManagedMacNodeInstallResolveAndRemove(t *testing.T) {
	const version = "v26.1.2"
	var payload bytes.Buffer
	gzipWriter := gzip.NewWriter(&payload)
	archive := tar.NewWriter(gzipWriter)
	root := "node-" + version + "-darwin-" + macNodeArch()
	for _, entry := range []struct{ name, content, link string }{
		{"bin/node", "#!/bin/sh\nprintf 'v26.1.2\\n'\n", ""},
		{"bin/npm", "", "../lib/node_modules/npm/bin/npm-cli.js"},
		{"lib/node_modules/npm/bin/npm-cli.js", "#!/bin/sh\nprintf '11.0.0\\n'\n", ""},
	} {
		header := &tar.Header{Name: root + "/" + entry.name, Mode: 0755, Size: int64(len(entry.content)), Typeflag: tar.TypeReg}
		if entry.link != "" {
			header.Typeflag, header.Linkname = tar.TypeSymlink, entry.link
		}
		if err := archive.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := archive.Write([]byte(entry.content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	filename := root + ".tar.gz"
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/"+version+"/"+filename {
			http.NotFound(w, r)
			return
		}
		calls.Add(1)
		_, _ = w.Write(payload.Bytes())
	}))
	defer server.Close()
	store := t.TempDir()
	installer := NewManagedNodeInstaller(store)
	installer.client.Client = server.Client()
	installer.sources = []acquisition.SourceCandidate{{Route: acquisition.RouteOfficial, Location: server.URL + "/"}}
	release := dshmanager.NodeReleaseInfo{Version: version, Platform: "darwin", Architecture: macNodeArch(), Filename: filename, SHA256: fmt.Sprintf("%x", sha256.Sum256(payload.Bytes()))}
	ctx := context.Background()
	installation, err := installer.Install(ctx, release, nil)
	if err != nil {
		t.Fatal(err)
	}
	if again, err := installer.Install(ctx, release, nil); err != nil || again != installation || calls.Load() != 1 {
		t.Fatalf("existing installation was not reused: %#v (%v), downloads %d", again, err, calls.Load())
	}
	runtime := &Runtime{node: "/system/node", managed: make(map[string]dshmanager.NodeInstallationInfo)}
	selection := dshmanager.NodeSelection{Kind: dshmanager.NodeSelectionManaged, InstallationID: installation.ID}
	resolved, err := runtime.NodeResolver(store).Resolve(ctx, selection, []dshmanager.NodeInstallationInfo{installation})
	if err != nil || resolved.NodePath != installation.NodePath {
		t.Fatalf("resolution: %#v, %v", resolved, err)
	}
	if executable, err := runtime.nodeForEnvironment(resolved.ChildEnvironment); err != nil || executable != installation.NodePath {
		t.Fatalf("launch node: %q, %v", executable, err)
	}
	if err = installer.Remove(ctx, installation); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(installation.NodePath)); !os.IsNotExist(err) {
		t.Fatalf("installation still exists: %v", err)
	}
	if _, err := runtime.nodeForEnvironment(resolved.ChildEnvironment); err == nil {
		t.Fatal("removed Node still admitted")
	}
}

func TestLiveManagedMacNodeInstall(t *testing.T) {
	if os.Getenv("DSH_WORK_TEST_LIVE_MAC_NODE") != "1" {
		t.Skip("requires an explicit official Node archive download")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	store := t.TempDir()
	catalog := acquisition.NodeCatalogClient{OfficialBaseURL: "https://nodejs.org/dist/", Platform: "darwin", Architecture: macNodeArch(), StagingRoot: store}
	metadata, err := catalog.RefreshLatest(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("selected %s (%s)", metadata.Version, metadata.Filename)
	installer := NewManagedNodeInstaller(store)
	installer.sources = installer.sources[:1]
	if archive := os.Getenv("DSH_WORK_TEST_LOCAL_NODE_ARCHIVE"); archive != "" {
		if !filepath.IsAbs(archive) {
			t.Fatal("expected an absolute Node archive path")
		}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/"+metadata.Version+"/"+metadata.Filename {
				http.NotFound(w, r)
				return
			}
			http.ServeFile(w, r, archive)
		}))
		defer server.Close()
		installer.client.Client = server.Client()
		installer.sources[0].Location = server.URL + "/"
	}
	release := dshmanager.NodeReleaseInfo{Version: metadata.Version, Platform: metadata.Platform, Architecture: metadata.Architecture, Filename: metadata.Filename, SHA256: metadata.SHA256}
	installation, err := installer.Install(ctx, release, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("verified official %s on %s/%s", installation.Version, installation.Platform, installation.Architecture)
	if err = installer.Remove(ctx, installation); err != nil {
		t.Fatal(err)
	}
}
