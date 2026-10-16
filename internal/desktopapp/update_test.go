package desktopapp

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/daemon"
	"github.com/local/dsh-work/internal/selfupdate"
	"github.com/local/dsh-work/internal/settings"
)

// pendingFor builds the feed identity the runner sees after a check. The
// verification block is not exercised here: the backend fake stands in for the
// whole authenticated download, and internal/selfupdate owns those checks.
func pendingFor(version string, size int64) *pendingRelease {
	return &pendingRelease{feed: &selfupdate.FeedRelease{
		Version: version,
		Channel: "stable",
		Artifact: selfupdate.FeedArtifact{
			Filename: "dsh-work-" + version + "-windows-amd64-installer.exe",
			Filetype: "exe",
			Size:     size,
			Platform: "windows",
			Arch:     "amd64",
		},
	}}
}

type updateBackendFake struct {
	release  *pendingRelease
	checkErr error
	stageErr error

	started chan struct{}
	finish  chan struct{}

	mu            sync.Mutex
	downloadCalls int
	installCalls  int
	recheckCalls  int
	recheckErr    error
	checkCalls    int
	discarded     bool
	switches      []string
}

func (f *updateBackendFake) WithChannel(channel string) (updateBackend, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.switches = append(f.switches, channel)
	return f, nil
}

func (f *updateBackendFake) Discard() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.discarded = true
	return nil
}

func (f *updateBackendFake) Check(context.Context) (*pendingRelease, error) {
	f.mu.Lock()
	f.checkCalls++
	f.mu.Unlock()
	return f.release, f.checkErr
}

func (f *updateBackendFake) Stage(ctx context.Context, release *pendingRelease, progress func(written, total int64)) error {
	f.mu.Lock()
	f.downloadCalls++
	f.mu.Unlock()
	if f.started != nil {
		select {
		case <-f.started:
		default:
			close(f.started)
		}
	}
	if f.stageErr != nil {
		return f.stageErr
	}
	if progress != nil && release != nil {
		progress(release.feed.Artifact.Size, release.feed.Artifact.Size)
	}
	if f.finish == nil {
		return nil
	}
	select {
	case <-f.finish:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (f *updateBackendFake) Recheck() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recheckCalls++
	return f.recheckErr
}

func (f *updateBackendFake) Install(context.Context) error {
	f.mu.Lock()
	f.installCalls++
	f.mu.Unlock()
	return nil
}

func (f *updateBackendFake) installs() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.installCalls
}

func TestUpdateRunnerManualCheckStopsAtAvailable(t *testing.T) {
	backend := &updateBackendFake{release: pendingFor("2.0.0", 100)}
	runner := &updateRunner{backend: backend, current: "1.0.0", state: daemon.UpdateSnapshot{Phase: daemon.UpdateIdle, CurrentVersion: "1.0.0"}}
	if err := runner.Action(context.Background(), daemon.UpdateActionCheck); err != nil {
		t.Fatal(err)
	}
	waitForUpdate(t, func() bool { return runner.Snapshot().Phase == daemon.UpdateAvailable })
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if backend.downloadCalls != 0 {
		t.Fatalf("manual check started %d downloads", backend.downloadCalls)
	}
	if got := runner.Snapshot().TargetVersion; got != "2.0.0" {
		t.Fatalf("target version = %q, want 2.0.0", got)
	}
}

func TestUpdateRunnerAutomaticCheckDownloadsWithoutInstalling(t *testing.T) {
	backend := &updateBackendFake{
		release: pendingFor("2.0.0", 100),
		started: make(chan struct{}),
		finish:  make(chan struct{}),
	}
	runner := &updateRunner{backend: backend, current: "1.0.0", state: daemon.UpdateSnapshot{Phase: daemon.UpdateIdle, CurrentVersion: "1.0.0"}}
	runner.Trigger(context.Background(), true)
	select {
	case <-backend.started:
	case <-time.After(time.Second):
		t.Fatal("automatic update did not start downloading")
	}
	if backend.installs() != 0 {
		t.Fatal("automatic download started the installer")
	}
	close(backend.finish)
	waitForUpdate(t, func() bool { return runner.Snapshot().Phase == daemon.UpdateReady })
	if got := runner.Snapshot().InstallMode; got != daemon.UpdateInstallInstaller {
		t.Fatalf("install mode = %q, want installer", got)
	}
	if backend.installs() != 0 {
		t.Fatal("automatic download ran the installer")
	}
}

func TestUpdateRunnerManualDownloadThenExplicitInstall(t *testing.T) {
	backend := &updateBackendFake{release: pendingFor("2.0.0", 100)}
	var stopUICalled bool
	runner := &updateRunner{
		backend: backend,
		current: "1.0.0",
		state:   daemon.UpdateSnapshot{Phase: daemon.UpdateAvailable, CurrentVersion: "1.0.0", TargetVersion: "2.0.0"},
		pending: backend.release,
		stopUI:  func() error { stopUICalled = true; return nil },
	}
	if err := runner.Action(context.Background(), daemon.UpdateActionDownload); err != nil {
		t.Fatal(err)
	}
	waitForUpdate(t, func() bool { return runner.Snapshot().Phase == daemon.UpdateReady })
	if err := runner.Action(context.Background(), daemon.UpdateActionInstall); err != nil {
		t.Fatal(err)
	}
	waitForUpdate(t, func() bool { return backend.installs() == 1 })
	if !stopUICalled {
		t.Fatal("explicit install did not stop the UI")
	}
	if got := runner.Snapshot().InstallMode; got != daemon.UpdateInstallInstaller {
		t.Fatalf("install mode = %q, want installer", got)
	}
}

func TestUpdateRunnerRefusesSwappedPayloadBeforeStoppingTheUI(t *testing.T) {
	backend := &updateBackendFake{recheckErr: selfupdate.ErrStagedPayloadChanged}
	var stopUICalled bool
	runner := &updateRunner{
		backend: backend,
		current: "1.0.0",
		state:   daemon.UpdateSnapshot{Phase: daemon.UpdateReady, CurrentVersion: "1.0.0", TargetVersion: "2.0.0", InstallMode: daemon.UpdateInstallInstaller},
		pending: pendingFor("2.0.0", 100),
		stopUI:  func() error { stopUICalled = true; return nil },
	}
	if err := runner.Action(context.Background(), daemon.UpdateActionInstall); err != nil {
		t.Fatal(err)
	}
	waitForUpdate(t, func() bool { return runner.Snapshot().Phase == daemon.UpdateError })
	if stopUICalled {
		t.Fatal("a payload that failed re-verification still stopped the UI")
	}
	if backend.installs() != 0 {
		t.Fatal("a payload that failed re-verification still reached the installer")
	}
}

func TestUpdateRunnerDoesNotEnterInstallingWhenUIStopFails(t *testing.T) {
	backend := &updateBackendFake{}
	runner := &updateRunner{
		backend: backend,
		current: "1.0.0",
		state:   daemon.UpdateSnapshot{Phase: daemon.UpdateReady, CurrentVersion: "1.0.0", TargetVersion: "2.0.0", InstallMode: daemon.UpdateInstallInstaller},
		pending: pendingFor("2.0.0", 100),
		stopUI:  func() error { return errors.New("ui still running") },
	}
	if err := runner.Action(context.Background(), daemon.UpdateActionInstall); err != nil {
		t.Fatal(err)
	}
	waitForUpdate(t, func() bool { return runner.Snapshot().Phase == daemon.UpdateError })
	if backend.installs() != 0 {
		t.Fatal("the installer ran after the UI stop failed")
	}
}

func TestUpdateRunnerCheckFailureIsVisibleWithoutRawError(t *testing.T) {
	runner := &updateRunner{
		backend: &updateBackendFake{checkErr: errors.New("private feed details")},
		current: "1.0.0",
		state:   daemon.UpdateSnapshot{Phase: daemon.UpdateIdle, CurrentVersion: "1.0.0"},
	}
	runner.Trigger(context.Background(), false)
	waitForUpdate(t, func() bool { return runner.Snapshot().Phase == daemon.UpdateError })
	state := runner.Snapshot()
	if state.ErrorCode != "check" {
		t.Fatalf("error code = %q, want check", state.ErrorCode)
	}
	if state.ErrorCode == "private feed details" {
		t.Fatal("raw provider error leaked into update state")
	}
}

func TestUpdateRunnerChannelSwitchDiscardsCandidateAndReadsTheNewFeed(t *testing.T) {
	backend := &updateBackendFake{release: pendingFor("0.4.0-nightly.20261013.1", 100)}
	runner := &updateRunner{
		backend:  backend,
		current:  "0.3.0",
		selected: "stable",
		state: daemon.UpdateSnapshot{
			Phase: daemon.UpdateReady, CurrentVersion: "0.3.0",
			SelectedChannel: "stable", TargetVersion: "0.3.0", InstallMode: daemon.UpdateInstallInstaller,
		},
		pending: pendingFor("0.3.0", 100),
	}
	if err := runner.SetChannel(context.Background(), settings.UpdateChannelNightly); err != nil {
		t.Fatal(err)
	}
	if got := runner.Snapshot().SelectedChannel; got != "nightly" {
		t.Fatalf("selected channel = %q, want nightly", got)
	}
	backend.mu.Lock()
	discarded := backend.discarded
	switches := append([]string(nil), backend.switches...)
	backend.mu.Unlock()
	if !discarded {
		t.Fatal("channel switch kept the staged candidate")
	}
	if len(switches) != 1 || switches[0] != "nightly" {
		t.Fatalf("backend switched to %v, want [nightly]", switches)
	}
	waitForUpdate(t, func() bool { return runner.Snapshot().Phase == daemon.UpdateAvailable })
	if got := runner.Snapshot().TargetVersion; got != "0.4.0-nightly.20261013.1" {
		t.Fatalf("target version = %q, want the nightly release", got)
	}
}

func TestUpdateRunnerRefusesChannelSwitchWhileInstalling(t *testing.T) {
	runner := &updateRunner{
		backend:  &updateBackendFake{},
		current:  "1.0.0",
		selected: "stable",
		state:    daemon.UpdateSnapshot{Phase: daemon.UpdateInstalling, CurrentVersion: "1.0.0", SelectedChannel: "stable"},
	}
	if err := runner.SetChannel(context.Background(), settings.UpdateChannelBeta); err == nil {
		t.Fatal("channel switch was accepted while an update was installing")
	}
	if got := runner.Snapshot().SelectedChannel; got != "stable" {
		t.Fatalf("selected channel = %q after a refused switch, want stable", got)
	}
}

func waitForUpdate(t *testing.T, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if done() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("update flow did not reach the expected state")
}

// TestManifestURLNamesTheChannelReleaseAsset pins the feed layout the release
// pipeline publishes: one fixed release tag per channel holding update.json.
func TestManifestURLNamesTheChannelReleaseAsset(t *testing.T) {
	root := "https://github.com/dsh-work-dev/dsh-work/releases/download"
	for _, root := range []string{root, root + "/"} {
		got := manifestURL(root, "beta")
		want := "https://github.com/dsh-work-dev/dsh-work/releases/download/updater-beta/update.json"
		if got != want {
			t.Fatalf("manifestURL(%q, beta) = %q, want %q", root, got, want)
		}
	}
}
