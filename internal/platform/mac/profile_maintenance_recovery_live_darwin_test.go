//go:build darwin && cgo

package mac

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/dshadapter"
	"github.com/local/dsh-work/internal/dshmanager"
	"github.com/local/dsh-work/internal/embeddedbrowser/macowner"
	"github.com/local/dsh-work/internal/lifecycle"
	"github.com/local/dsh-work/internal/localipc"
	"github.com/local/dsh-work/internal/supervisor"
)

const hostLossFixtureBundle = "dsh-work-package-host-loss-fixture"

func TestLiveMacProfileMaintenanceHostLoss(t *testing.T) {
	testLiveMacProfileHostLoss(t, profileHostLoss{})
}

func TestLiveMacProfileMaintenanceHostLossAfterPackageWrite(t *testing.T) {
	testLiveMacProfileHostLoss(t, profileHostLoss{tarball: true, publish: true})
}

func TestLiveMacProfileMaintenanceRecoveryAfterForcedStop(t *testing.T) {
	testLiveMacProfileHostLoss(t, profileHostLoss{tarball: true, publish: true, forceCLI: true})
}

func TestLiveMacProfileMaintenanceRecoveryBeforeReceipt(t *testing.T) {
	testLiveMacProfileHostLoss(t, profileHostLoss{tarball: true, forceCLI: true})
}

func TestLiveMacProfileMaintenanceRecoveryInitialProfile(t *testing.T) {
	testLiveMacProfileHostLoss(t, profileHostLoss{tarball: true, publish: true, forceCLI: true, initial: true})
}

func TestLiveMacProfileMaintenanceRecoveryResumesPartialRollback(t *testing.T) {
	testLiveMacProfileHostLoss(t, profileHostLoss{tarball: true, publish: true, forceCLI: true, replay: true})
}

type profileHostLoss struct {
	tarball, publish, forceCLI, initial, replay bool
}

func testLiveMacProfileHostLoss(t *testing.T, cut profileHostLoss) {
	t.Helper()
	if os.Getenv("DSH_WORK_TEST_LIVE_MAC_PROFILE_PACKAGES") != "1" {
		t.Skip("requires the signed Mac CEF fixture, pinned DSH, and npm registry access")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	commands := dshadapter.NewPluginCommands()
	open := func(root string) *Runtime {
		t.Helper()
		t.Setenv("DSH_WORK_EMBEDDED_ROOT", root)
		t.Setenv("DSH_WORK_EMBEDDED_CHECKPOINT", "1")
		runtime, err := NewRuntime()
		if err != nil {
			t.Fatal(err)
		}
		runtime.DSHInstaller(filepath.Join(root, "runtimes"))
		return runtime
	}
	if registry := os.Getenv("DSH_WORK_TEST_PROFILE_HOST_LOSS_REGISTRY"); registry != "" {
		root := os.Getenv("DSH_WORK_EMBEDDED_ROOT")
		home := filepath.Join(root, "home")
		runtime := open(root)
		spec := hostLossFixtureBundle + "@1.0.0"
		environment := map[string]string{"DSH_HOME": home, "npm_config_registry": registry}
		if cut.tarball {
			spec = registry + "/bundle.tgz"
			delete(environment, "npm_config_registry")
		}
		args, err := commands.Install("web", spec)
		if err != nil {
			t.Fatal(err)
		}
		result, err := runtime.Run(ctx, runtime.cli, args, environment, home)
		t.Fatalf("fixture command ended before Host termination: %v\n%s\n%s", err, result.Stdout, result.Stderr)
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "home")
	if err = os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	run := func(runtime *Runtime, args []string, err error) dshadapter.CommandResult {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		result, err := runtime.Run(ctx, runtime.cli, args, map[string]string{"DSH_HOME": home}, home)
		if err != nil {
			t.Fatalf("DSH %v: %v\n%s\n%s", args, err, result.Stdout, result.Stderr)
		}
		return result
	}
	runtime := open(root)
	lock := filepath.Join(home, "profiles", "web", "pnpm-lock.yaml")
	var before []byte
	if !cut.initial {
		install, err := commands.Install("web", "is-number@7.0.0")
		run(runtime, install, err)
		before, err = os.ReadFile(lock)
		if err != nil {
			t.Fatal(err)
		}
	}
	profile := filepath.Dir(lock)
	manifestPath := filepath.Join(profile, "package.json")
	recoveryRecord := filepath.Join(profile, ".dsh-work", "mac-package-operation.json")
	var checkpoint dshmanager.ProfileVersionInput
	var originalManifest []byte
	var bundle []byte
	if cut.tarball {
		bundle = maintenanceBundleArchive(t)
	}

	requested := make(chan struct{}, 1)
	stopRegistry := make(chan struct{})
	release := make(chan struct{})
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if cut.tarball && r.URL.Path != "/bundle.tgz" {
			http.NotFound(w, r)
			return
		}
		select {
		case requested <- struct{}{}:
		default:
		}
		select {
		case <-r.Context().Done():
		case <-stopRegistry:
		case <-release:
			w.Header().Set("Content-Type", "application/gzip")
			_, _ = w.Write(bundle)
		}
	}))
	defer registry.Close()
	defer close(stopRegistry)
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	host := exec.CommandContext(ctx, binary, "-test.run", "^"+t.Name()+"$")
	host.Env = append(os.Environ(), "DSH_WORK_TEST_PROFILE_HOST_LOSS_REGISTRY="+registry.URL)
	var logs bytes.Buffer
	host.Stdout, host.Stderr = &logs, &logs
	if err = host.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if host.ProcessState == nil {
			_ = host.Process.Kill()
			_ = host.Wait()
		}
	}()
	select {
	case <-requested:
	case <-ctx.Done():
		t.Fatal("fixture Host did not reach the stalled registry")
	}
	if cut.tarball {
		awaitPackageRecoveryPhase(t, ctx, recoveryRecord, "running")
		checkpoint, err = commands.CaptureVersions(ctx, profile)
		if err != nil || checkpoint.Unavailable != "" {
			t.Fatalf("capture prior package metadata: %v (%s)", err, checkpoint.Unavailable)
		}
		originalManifest, err = os.ReadFile(manifestPath)
		if err != nil {
			t.Fatal(err)
		}
	}

	// The request proves pnpm executed. Measure the specific owner and both
	// registered Node instances before terminating their Host.
	pattern := filepath.Join(root, "cache", "mac-preview-owner", "generation-*", "attempt-*.json")
	attempts, err := filepath.Glob(pattern)
	if err != nil || len(attempts) != 1 {
		t.Fatalf("expected one live maintenance attempt: %v (%v)", attempts, err)
	}
	var attempt struct{ Generation, Run string }
	readMaintenanceJournal(t, attempts[0], &attempt)
	statePath := filepath.Join(filepath.Dir(attempts[0]), "generation-"+attempt.Generation+".json")
	var state struct {
		Owner, Broker macowner.Process
		RuntimeReady  bool
		Owned         map[string]struct {
			Process macowner.Process
			Role    string
			Target  *localipc.ProcessTarget
		}
	}
	readMaintenanceJournal(t, statePath, &state)
	members := []macowner.Process{state.Owner, state.Broker}
	var cli *localipc.ProcessTarget
	for _, process := range state.Owned {
		if process.Role == "profile-pnpm" {
			members = append(members, process.Process)
		}
		if process.Role == "restricted-dsh" && process.Process.PID == state.Broker.PID {
			cli = process.Target
		}
	}
	if !state.RuntimeReady || len(members) != 3 || attempt.Run == "" {
		t.Fatalf("incomplete maintenance ownership evidence: %+v", state)
	}
	for _, process := range members {
		started, err := localipc.ProcessStarted(process.PID)
		if err != nil || process.Started == 0 || started != process.Started {
			t.Fatalf("maintenance process did not match its live instance: %+v (%v)", process, err)
		}
	}
	bundleURL := registry.URL + "/bundle.tgz"
	if cut.publish {
		if cli == nil {
			t.Fatal("missing authenticated CLI process instance")
		}
		if err := localipc.SignalTarget(*cli, syscall.SIGSTOP); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = localipc.SignalTarget(*cli, syscall.SIGCONT) }()
		close(release)
		awaitUnregisteredMaintenanceBundle(t, ctx, commands, profile, bundleURL)
		if cut.forceCLI {
			awaitPackageRecoveryPhase(t, ctx, recoveryRecord, "published")
		}
	}
	if cut.forceCLI {
		// Stop the driver so it cannot process the CLI exit before Host loss.
		if cli == nil {
			t.Fatal("missing authenticated CLI process instance")
		}
		if err = host.Process.Signal(syscall.SIGSTOP); err != nil {
			t.Fatal(err)
		}
		if err = localipc.SignalTarget(*cli, syscall.SIGKILL); err != nil {
			t.Fatal(err)
		}
	}
	if err = host.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	var exit *exec.ExitError
	if err = host.Wait(); !errors.As(err, &exit) || exit.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
		t.Fatalf("fixture Host was not killed: %v\n%s", err, logs.String())
	}

	replacement := open(root)
	for _, process := range members {
		started, err := localipc.ProcessStarted(process.PID)
		if err == nil && started == process.Started {
			t.Fatalf("maintenance process survived Host recovery: %+v", process)
		}
		if err != nil && !errors.Is(syscall.Kill(int(process.PID), 0), syscall.ESRCH) {
			t.Fatalf("could not confirm process retirement: %+v (%v)", process, err)
		}
	}
	for _, path := range []string{attempts[0], attempt.Run} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("maintenance endpoint or attempt remains: %s (%v)", path, err)
		}
	}
	list, err := commands.List("web")
	if err != nil {
		t.Fatal(err)
	}
	if cut.publish && cut.forceCLI && !cut.replay {
		runRecord := filepath.Join(profile, ".plugin-manager", "run.json")
		previousRun, readErr := os.ReadFile(runRecord)
		if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
			t.Fatal(readErr)
		}
		liveRun, err := json.Marshal(map[string]any{"pid": os.Getpid(), "grouped": false})
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(runRecord, liveRun, 0600); err != nil {
			t.Fatal(err)
		}
		result, err := replacement.Run(ctx, replacement.cli, list, map[string]string{"DSH_HOME": home}, home)
		if !errors.Is(err, dshmanager.ErrProfilePackageRecoveryPending) {
			t.Fatalf("expected recovery to wait for the recorded DSH process: %v\n%s", err, result.Stderr)
		}
		awaitUnregisteredMaintenanceBundle(t, ctx, commands, profile, bundleURL)
		if errors.Is(readErr, os.ErrNotExist) {
			err = os.Remove(runRecord)
		} else {
			err = os.WriteFile(runRecord, previousRun, 0600)
		}
		if err != nil {
			t.Fatal(err)
		}

		var manifest map[string]any
		data, err := os.ReadFile(manifestPath)
		if err != nil || json.Unmarshal(data, &manifest) != nil {
			t.Fatalf("read interrupted package manifest: %v", err)
		}
		dependencies := manifest["dependencies"].(map[string]any)
		writeManifest := func() []byte {
			t.Helper()
			data, err := json.Marshal(manifest)
			if err == nil {
				err = os.WriteFile(manifestPath, data, 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			return data
		}
		manifest["description"] = "Keep this unrelated edit"
		dependencies["outside-edit"] = "1.0.0"
		conflict := writeManifest()
		conflictingLock, err := os.ReadFile(lock)
		if err != nil {
			t.Fatal(err)
		}
		result, err = replacement.Run(ctx, replacement.cli, list, map[string]string{"DSH_HOME": home}, home)
		if !errors.Is(err, dshmanager.ErrProfilePackageRecoveryConflict) {
			t.Fatalf("expected recovery conflict: %v\n%s", err, result.Stderr)
		}
		_, err = replacement.Start(ctx, supervisor.LaunchPlan{
			Executable: replacement.cli, Args: []string{"--patch", "unused.yml", "--profile", "web"},
			Env: map[string]string{"DSH_HOME": home}, WorkingDirectory: home,
		}, nil)
		var failure lifecycle.Failure
		if !errors.As(err, &failure) || failure.Code != lifecycle.ErrorPluginCommandFailed ||
			!strings.Contains(failure.Detail, "preserved") {
			t.Fatalf("Workbench startup did not surface the profile conflict: %v", err)
		}
		preserved, err := os.ReadFile(manifestPath)
		preservedLock, lockErr := os.ReadFile(lock)
		if err != nil || lockErr != nil || !bytes.Equal(conflict, preserved) || !bytes.Equal(conflictingLock, preservedLock) {
			t.Fatalf("recovery touched conflicting inputs: %v / %v", err, lockErr)
		}
		delete(dependencies, "outside-edit")
		writeManifest()
	}
	if cut.replay {
		// Reproduce the durable state after recovery replaced the manifest
		// and before it restored the lockfile.
		body, err := os.ReadFile(recoveryRecord)
		if err != nil || !bytes.Contains(body, []byte(`"phase":"published"`)) {
			t.Fatalf("read recovery phase: %v", err)
		}
		body = bytes.Replace(body, []byte(`"phase":"published"`), []byte(`"phase":"restoring"`), 1)
		if err = os.WriteFile(recoveryRecord, body, 0600); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(manifestPath, originalManifest, 0600); err != nil {
			t.Fatal(err)
		}
	}
	result := run(replacement, list, err)
	after, err := os.ReadFile(lock)
	if (!cut.initial && (err != nil || !json.Valid([]byte(strings.TrimSpace(result.Stdout))) ||
		!strings.Contains(result.Stdout, `"is-number"`))) ||
		(cut.initial && !errors.Is(err, os.ErrNotExist)) {
		t.Fatalf("replacement Host did not reuse the original profile: %v\n%s", err, result.Stdout)
	}
	if cut.publish {
		current, err := commands.CaptureVersions(ctx, profile)
		if err != nil {
			t.Fatal(err)
		}
		if cut.forceCLI {
			if !reflect.DeepEqual(checkpoint, current) {
				t.Fatal("automatic recovery did not restore previous dependencies and bundle selection")
			}
			if _, err = os.Lstat(filepath.Join(profile, "node_modules", hostLossFixtureBundle)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("automatic recovery left the interrupted package linked: %v", err)
			}
			manifest, err := os.ReadFile(filepath.Join(profile, "package.json"))
			if err != nil || (!cut.replay && !bytes.Contains(manifest, []byte("Keep this unrelated edit"))) {
				t.Fatalf("automatic recovery dropped the unrelated edit: %v", err)
			}
			retry, err := commands.Install("web", bundleURL)
			run(replacement, retry, err)
			current, err = commands.CaptureVersions(ctx, profile)
			if err != nil {
				t.Fatal(err)
			}
		}
		if !slices.Contains(current.Bundles, hostLossFixtureBundle) || !strings.Contains(current.Lock, bundleURL) {
			t.Fatal("the installed package and the bundle selection did not both commit")
		}
	} else if !bytes.Equal(before, after) {
		t.Fatal("canceling before the registry response changed the frozen lock")
	}
	if _, err = os.Stat(recoveryRecord); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("completed maintenance left a recovery journal: %v", err)
	}
	// Recovery can be invoked again after a fully retired attempt.
	open(root)
	t.Logf("recovered maintenance generation %s and retired all %d measured processes", attempt.Generation, len(members))
}

func awaitPackageRecoveryPhase(t *testing.T, ctx context.Context, path, phase string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		body, err := os.ReadFile(path)
		if err == nil && bytes.Contains(body, []byte(`"phase":"`+phase+`"`)) {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("pnpm recovery record did not reach %s: %v", phase, err)
		case <-tick.C:
		}
	}
}

func maintenanceBundleArchive(t *testing.T) []byte {
	t.Helper()
	var payload bytes.Buffer
	gz := gzip.NewWriter(&payload)
	archive := tar.NewWriter(gz)
	for path, content := range map[string]string{
		"package.json":     `{"name":"` + hostLossFixtureBundle + `","version":"1.0.0","dsh":{"bundle":{"patch":"./cordis.patch.yml"}}}`,
		"cordis.patch.yml": "[]\n",
	} {
		if err := archive.WriteHeader(&tar.Header{Name: "package/" + path, Mode: 0644, Size: int64(len(content))}); err != nil {
			t.Fatal(err)
		}
		if _, err := archive.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return payload.Bytes()
}

func awaitUnregisteredMaintenanceBundle(t *testing.T, ctx context.Context, commands dshadapter.PluginCommands, profile, source string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		input, err := commands.CaptureVersions(ctx, profile)
		if err == nil && strings.Contains(input.Lock, source) && slices.ContainsFunc(input.Plugins, func(plugin dshmanager.VersionPlugin) bool {
			return plugin.Name == hostLossFixtureBundle && plugin.Source == source
		}) {
			if slices.Contains(input.Bundles, hostLossFixtureBundle) {
				t.Fatal("the suspended CLI already activated the bundle")
			}
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("did not observe pnpm's written package, manifest and lock before activation: %v", err)
		case <-tick.C:
		}
	}
}

func readMaintenanceJournal(t *testing.T, path string, state any) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var record struct {
		Body json.RawMessage
		MAC  string
	}
	if err = json.Unmarshal(body, &record); err != nil {
		t.Fatal(err)
	}
	key, err := os.ReadFile(filepath.Join(filepath.Dir(path), "journal.key"))
	if err != nil {
		t.Fatal(err)
	}
	domain := "cef-generation-v1"
	if strings.HasPrefix(filepath.Base(path), "attempt-") {
		domain = "cef-attempt-v1"
	}
	measure := hmac.New(sha256.New, key)
	measure.Write([]byte(domain + "\x00"))
	measure.Write(record.Body)
	provided, err := hex.DecodeString(record.MAC)
	if err != nil || !hmac.Equal(provided, measure.Sum(nil)) {
		t.Fatal("maintenance journal signature rejected")
	}
	if err = json.Unmarshal(record.Body, state); err != nil {
		t.Fatal(err)
	}
}
