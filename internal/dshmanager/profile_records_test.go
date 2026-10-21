package dshmanager

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func recordsFixture(t *testing.T) (*Manager, State) {
	t.Helper()
	root := t.TempDir()
	directory := DataDirectoryInfo{ID: "home", Path: filepath.Join(root, "data"), Ownership: DataDirectoryOwnershipDSHWork}
	for _, name := range []string{"a", "b"} {
		path := filepath.Join(directory.Path, "profiles", name)
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "package.json"), []byte(`{}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	m, err := New(Config{StatePath: filepath.Join(root, "app", "state.json"), DataDirectories: []DataDirectoryInfo{directory}})
	if err != nil {
		t.Fatal(err)
	}
	state := m.stateLocked()
	state.VersionRecovery = &VersionRecoveryState{SchemaVersion: 1, LastByProfile: map[string]string{}, LastRunning: "b", Pending: &RecoveryOperation{PointID: "b", Status: "running"}}
	for _, name := range []string{"a", "b"} {
		ref := ProfileRef{DataDirectoryID: "home", Name: name}
		state.VersionRecovery.Points = append(state.VersionRecovery.Points, storedRestorePoint{RestorePoint: RestorePoint{ID: name, Kind: "manual", Target: RunContext{RuntimeID: "dsh", Node: NodeSelection{Kind: NodeSelectionSystem}, Profile: ref}}})
		state.VersionRecovery.LastByProfile[profilePointKey(ref)] = name
		state.PluginProvenance = append(state.PluginProvenance, PluginProvenanceRecord{Profile: ref, Package: "@example/" + name, SourceKind: PluginSourcePublicRegistry, RecordedAt: "2026-10-21T00:00:00Z"})
	}
	return m, state
}

func recordPath(m *Manager, profile, file string) string {
	return filepath.Join(profileRecordsDir(m.config.DataDirectories[0], profile), file)
}

func recordBytes(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func saveRecordFixture(t *testing.T, m *Manager, state State) {
	t.Helper()
	if err := m.saveState(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	m.versionRecovery = state.VersionRecovery
	m.pluginProvenance = state.PluginProvenance
}

func TestProfileRecordsSeparateFilesAndRoundTrip(t *testing.T) {
	m, state := recordsFixture(t)
	saveRecordFixture(t, m, state)
	for _, name := range []string{"a", "b"} {
		var recovery profileRecoveryFile
		if err := json.Unmarshal(recordBytes(t, recordPath(m, name, "recovery.json")), &recovery); err != nil {
			t.Fatal(err)
		}
		if recovery.SchemaVersion != 1 || recovery.Last != name || len(recovery.Points) != 1 || recovery.Points[0].Target.Profile.Name != name {
			t.Fatalf("%s recovery = %+v", name, recovery)
		}
		var provenance profileProvenanceFile
		if err := json.Unmarshal(recordBytes(t, recordPath(m, name, "provenance.json")), &provenance); err != nil {
			t.Fatal(err)
		}
		if len(provenance.Records) != 1 || provenance.Records[0].Profile.Name != name {
			t.Fatalf("%s provenance = %+v", name, provenance)
		}
	}
	var app map[string]json.RawMessage
	if err := json.Unmarshal(recordBytes(t, m.config.StatePath), &app); err != nil {
		t.Fatal(err)
	}
	if _, ok := app["pluginProvenance"]; ok {
		t.Fatal("app state contains provenance")
	}
	var recovery map[string]json.RawMessage
	if err := json.Unmarshal(app["versionRecovery"], &recovery); err != nil {
		t.Fatal(err)
	}
	if len(recovery) != 3 || recovery["schemaVersion"] == nil || recovery["lastRunning"] == nil || recovery["pending"] == nil {
		t.Fatalf("app recovery keys = %v", recovery)
	}
	appState, err := (FileStateStore{}).Load(context.Background(), m.config.StatePath)
	if err != nil || appState.VersionRecovery.LastRunning != "b" || len(appState.VersionRecovery.Points) != 0 || len(appState.VersionRecovery.LastByProfile) != 0 {
		t.Fatalf("app-only load = %+v, %v", appState, err)
	}
	reloaded, err := New(m.config)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(reloaded.versionRecovery, state.VersionRecovery) || !reflect.DeepEqual(reloaded.pluginProvenance, state.PluginProvenance) || reloaded.knownGood == nil || *reloaded.knownGood != state.VersionRecovery.Points[1].Target {
		t.Fatalf("round trip: recovery=%+v knownGood=%+v", reloaded.versionRecovery, reloaded.knownGood)
	}
	before, _ := m.Snapshot(context.Background())
	after, _ := reloaded.Snapshot(context.Background())
	if !reflect.DeepEqual(before.RestorePoints, after.RestorePoints) {
		t.Fatalf("views differ: %+v %+v", before.RestorePoints, after.RestorePoints)
	}
}

func TestProfileRecordsAppOnlyPointerLoadsBeforeMerge(t *testing.T) {
	m, state := recordsFixture(t)
	saveRecordFixture(t, m, state)
	if err := os.WriteFile(m.config.StatePath, []byte(`{"versionRecovery":{"schemaVersion":1,"lastRunning":"a"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	app, err := (FileStateStore{}).Load(context.Background(), m.config.StatePath)
	if err != nil || app.VersionRecovery.LastRunning != "a" {
		t.Fatalf("load = %+v %v", app, err)
	}
	reloaded, err := New(m.config)
	if err != nil || reloaded.knownGood == nil || reloaded.knownGood.Profile.Name != "a" {
		t.Fatalf("knownGood after merge = %v, %v", reloaded, err)
	}
}

func TestProfileRecordsFollowInternalAndExternalRename(t *testing.T) {
	m, state := recordsFixture(t)
	saveRecordFixture(t, m, state)
	ref := ProfileRef{DataDirectoryID: "home", Name: "a"}
	if _, err := m.RenameProfile(context.Background(), ProfileRenameRequest{Profile: ref, NewName: "c"}); err != nil {
		t.Fatal(err)
	}
	var provenance profileProvenanceFile
	if err := json.Unmarshal(recordBytes(t, recordPath(m, "c", "provenance.json")), &provenance); err != nil {
		t.Fatal(err)
	}
	if len(provenance.Records) != 1 || provenance.Records[0].Profile.Name != "c" || m.pluginProvenance[0].Profile.Name != "c" || m.versionRecovery.Points[0].Target.Profile.Name != "c" {
		t.Fatalf("rename did not update records: %+v %+v", provenance, m.versionRecovery)
	}
	root := filepath.Join(m.config.DataDirectories[0].Path, "profiles")
	if err := os.Rename(filepath.Join(root, "b"), filepath.Join(root, "d")); err != nil {
		t.Fatal(err)
	}
	reloaded, err := New(m.config)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.versionRecovery.Points) != 2 || len(reloaded.pluginProvenance) != 2 {
		t.Fatal("rename lost records")
	}
	for _, point := range reloaded.versionRecovery.Points {
		if point.Target.Profile.Name != "c" && point.Target.Profile.Name != "d" {
			t.Fatalf("stale point: %+v", point)
		}
	}
	for _, record := range reloaded.pluginProvenance {
		if record.Profile.Name != "c" && record.Profile.Name != "d" {
			t.Fatalf("stale provenance: %+v", record)
		}
	}
}

func TestDeletedProfilePrunesDanglingReferences(t *testing.T) {
	m, state := recordsFixture(t)
	saveRecordFixture(t, m, state)
	profile := filepath.Join(m.config.DataDirectories[0].Path, "profiles", "b")
	if err := os.RemoveAll(profile); err != nil {
		t.Fatal(err)
	}
	if err := m.saveState(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(profile); !os.IsNotExist(err) {
		t.Fatal("save recreated deleted profile")
	}
	reloaded, err := New(m.config)
	if err != nil {
		t.Fatal(err)
	}
	r := reloaded.versionRecovery
	if len(r.Points) != 1 || len(r.LastByProfile) != 1 || r.LastRunning != "" || r.Pending != nil || reloaded.knownGood != nil || len(reloaded.pluginProvenance) != 1 {
		t.Fatalf("dangling records = %+v", r)
	}
	if err = reloaded.saveState(context.Background(), reloaded.stateLocked()); err != nil {
		t.Fatal(err)
	}
}

func TestUnreadableProfileRecordsArePreserved(t *testing.T) {
	m, state := recordsFixture(t)
	saveRecordFixture(t, m, state)
	path := recordPath(m, "b", "recovery.json")
	broken := []byte("{broken")
	if err := os.WriteFile(path, broken, 0o600); err != nil {
		t.Fatal(err)
	}
	reloaded, err := New(m.config)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.versionRecovery.Points) != 1 || reloaded.versionRecovery.LastRunning != "" || reloaded.versionRecovery.Pending != nil {
		t.Fatalf("corrupt record pointers = %+v", reloaded.versionRecovery)
	}
	if err = reloaded.saveState(context.Background(), reloaded.stateLocked()); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(recordBytes(t, path), broken) {
		t.Fatal("corrupt file overwritten")
	}
}

func TestDuplicateRestorePointRejectsAllWrites(t *testing.T) {
	m, state := recordsFixture(t)
	saveRecordFixture(t, m, state)
	paths := []string{m.config.StatePath, recordPath(m, "a", "recovery.json"), recordPath(m, "b", "recovery.json"), recordPath(m, "a", "provenance.json"), recordPath(m, "b", "provenance.json")}
	before := map[string][]byte{}
	for _, path := range paths {
		before[path] = recordBytes(t, path)
	}
	state.VersionRecovery.Points = append(state.VersionRecovery.Points, state.VersionRecovery.Points[0])
	if err := m.saveState(context.Background(), state); err == nil {
		t.Fatal("duplicate accepted")
	}
	for _, path := range paths {
		if !bytes.Equal(recordBytes(t, path), before[path]) {
			t.Fatalf("invalid save changed %s", path)
		}
	}
}

func TestSaveStateDoesNotCreateEmptyProfileRecordFiles(t *testing.T) {
	m, state := recordsFixture(t)
	directory := m.config.DataDirectories[0]
	directory.Ownership = DataDirectoryOwnershipUser
	root := filepath.Join(directory.Path, "profiles")
	for old, next := range map[string]string{"a": "x", "b": "y"} {
		if err := os.Rename(filepath.Join(root, old), filepath.Join(root, next)); err != nil {
			t.Fatal(err)
		}
	}
	m.config.DataDirectories[0] = directory
	state.DataDirectories = []DataDirectoryInfo{directory}
	state.VersionRecovery = nil
	state.PluginProvenance = nil
	if err := m.saveState(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"x", "y"} {
		if _, err := os.Stat(filepath.Join(root, name, profileRecordsFolder)); !os.IsNotExist(err) {
			t.Fatalf("empty save created %s/.dsh-work: %v", name, err)
		}
	}

	state.PluginProvenance = []PluginProvenanceRecord{{
		Profile: ProfileRef{DataDirectoryID: directory.ID, Name: "x"},
		Package: "@example/x", SourceKind: PluginSourcePublicRegistry, RecordedAt: "2026-10-21T00:00:00Z",
	}}
	if err := m.saveState(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	xDir := filepath.Join(root, "x", profileRecordsFolder)
	if _, err := os.Stat(filepath.Join(xDir, "provenance.json")); err != nil {
		t.Fatalf("source record missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(xDir, "recovery.json")); !os.IsNotExist(err) {
		t.Fatalf("empty recovery file created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "y", profileRecordsFolder)); !os.IsNotExist(err) {
		t.Fatalf("unrelated profile records dir created: %v", err)
	}

	state.PluginProvenance = nil
	if err := m.saveState(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	var sources profileProvenanceFile
	if err := json.Unmarshal(recordBytes(t, filepath.Join(xDir, "provenance.json")), &sources); err != nil {
		t.Fatal(err)
	}
	if sources.Records == nil || len(sources.Records) != 0 {
		t.Fatalf("cleared existing provenance = %#v", sources.Records)
	}
}

type raceProfileVersionCommands struct{ testPluginCommands }

func (raceProfileVersionCommands) CheckVersionProfile(context.Context, string) error { return nil }
func (raceProfileVersionCommands) CaptureVersions(context.Context, string) (ProfileVersionInput, error) {
	return ProfileVersionInput{PackageManager: "pnpm@11.19.0"}, nil
}
func (raceProfileVersionCommands) ApplyVersions(context.Context, string, ProfileVersionInput) error {
	return nil
}
func (raceProfileVersionCommands) ResetVersions(context.Context, string) ([]string, error) {
	return nil, nil
}
func (raceProfileVersionCommands) ForceInstall(string, bool) ([]string, error) { return nil, nil }
func (raceProfileVersionCommands) RemoveVersions(string, []string) ([]string, error) {
	return nil, nil
}

func TestConcurrentSwitchAttemptsAndRestorePoints(t *testing.T) {
	m := newTestManager(t)
	m.config.PluginCommands = raceProfileVersionCommands{}
	ctx := context.Background()
	ref := ProfileRef{DataDirectoryID: "dsh-work", Name: "web"}
	launch, err := m.ResolveLaunch(ctx, LaunchRequest{RuntimeID: "dsh-test", Profile: ref})
	if err != nil {
		t.Fatal(err)
	}
	launch = m.CaptureLaunchVersions(ctx, launch)
	if launch.VersionSeed == nil {
		t.Fatalf("could not capture initial version: %s", launch.VersionError)
	}
	if _, err := m.CommitHealthy(ctx, launch); err != nil {
		t.Fatal(err)
	}

	const calls = 50
	start := make(chan struct{})
	errs := make(chan error, calls*2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < calls; i++ {
			if _, err := m.RecordSwitchAttempt(ctx, SwitchAttempt{Target: launch.Target}, false); err != nil {
				errs <- err
			}
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < calls; i++ {
			if _, err := m.SaveRestorePoint(ctx, "parallel"); err != nil {
				errs <- err
			}
		}
	}()
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestCloneExportSkipProfileRecordsAndBackupsUseNewFolder(t *testing.T) {
	m, state := recordsFixture(t)
	saveRecordFixture(t, m, state)
	ref := ProfileRef{DataDirectoryID: "home", Name: "a"}
	clone, err := m.CloneProfile(context.Background(), ProfileCloneRequest{Profile: ref})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(recordPath(m, clone.Profile.Name, "")); !os.IsNotExist(err) {
		t.Fatalf("clone inherited records: %v", err)
	}
	output := filepath.Join(t.TempDir(), "export.zip")
	if err := m.ExportProfile(context.Background(), ref, output); err != nil {
		t.Fatal(err)
	}
	archive, err := zip.OpenReader(output)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	for _, entry := range archive.File {
		if strings.Split(entry.Name, "/")[0] == profileRecordsFolder {
			t.Fatalf("export includes %s", entry.Name)
		}
	}
	backup, err := m.BackupProfile(context.Background(), ProfileBackupRequest{Profile: ref})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(recordPath(m, "a", "backups"), backup.FileName)); err != nil {
		t.Fatal(err)
	}
	backups, err := m.ListProfileBackups(context.Background(), ref)
	if err != nil || len(backups) != 1 {
		t.Fatalf("backups = %+v, %v", backups, err)
	}
}

func TestManagedDataDirectoryUsesConfiguredPath(t *testing.T) {
	base := []DataDirectoryInfo{{ID: "dsh-work", Path: "/new", Ownership: DataDirectoryOwnershipDSHWork}}
	persisted := []DataDirectoryInfo{{ID: "dsh-work", Path: "/old"}, {ID: "u1", Path: "/user", Ownership: DataDirectoryOwnershipUser}}
	merged := mergeDataDirectories(base, persisted)
	if len(merged) != 2 || merged[0] != base[0] || merged[1] != persisted[1] {
		t.Fatalf("merged = %+v", merged)
	}
}
