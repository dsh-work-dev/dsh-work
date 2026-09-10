package dshmanager

import (
	"archive/zip"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/local/dsh-work/internal/lifecycle"
)

func TestProfileExportIsPortableAndIndependentOfLocalBackups(t *testing.T) {
	m := newTestManager(t)
	m.config.ProfileCatalog = testProfileCatalog{definitions: []ProfileDefinition{{Name: "web"}}}
	ctx := context.Background()
	ref := ProfileRef{DataDirectoryID: "dsh-work", Name: "web"}
	profile := filepath.Join(m.config.DataDirectories[0].Path, "profiles", ref.Name)
	if err := os.WriteFile(filepath.Join(profile, "package.json"), []byte(`{"name":"portable"}`), 0600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "profile.zip")
	if err := m.ExportProfile(ctx, ref, output); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(profile, profileBackupFolder)); !os.IsNotExist(err) {
		t.Fatal("export created a local backup")
	}
	result, err := m.ImportProfileBackup(ctx, ref.DataDirectoryID, output)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(m.config.DataDirectories[0].Path, "profiles", result.Profile.Name, "package.json"))
	if err != nil || string(data) != `{"name":"portable"}` {
		t.Fatalf("imported content = %s, %v", data, err)
	}
	before, _ := os.ReadFile(output)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	assertFailureCode(t, m.ExportProfile(cancelled, ref, output), lifecycle.ErrorCancelled)
	after, _ := os.ReadFile(output)
	if string(before) != string(after) {
		t.Fatal("cancelled export changed existing file")
	}
	if err := m.ExportProfile(ctx, ref, filepath.Join(profile, "recursive.zip")); err == nil {
		t.Fatal("export inside its own source accepted")
	}
	if _, err := m.DeleteProfile(ctx, ProfileDeleteRequest{Profile: result.Profile}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(output); err != nil {
		t.Fatalf("profile deletion removed external export: %v", err)
	}
}

func TestProfileBackupsFollowOwnerAndDelete(t *testing.T) {
	for _, rename := range []bool{false, true} {
		t.Run(map[bool]string{false: "delete-legacy", true: "rename-then-delete"}[rename], func(t *testing.T) {
			m := newTestManager(t)
			m.config.ProfileCatalog = testProfileCatalog{definitions: []ProfileDefinition{{Name: "web"}}}
			ctx := context.Background()
			home := m.config.DataDirectories[0].Path
			web := ProfileRef{DataDirectoryID: "dsh-work", Name: "web"}
			if err := os.WriteFile(filepath.Join(home, "profiles", "web", "package.json"), []byte(`{}`), 0600); err != nil {
				t.Fatal(err)
			}
			other, err := m.BackupProfile(ctx, ProfileBackupRequest{Profile: web})
			if err != nil {
				t.Fatal(err)
			}
			clone, err := m.CloneProfile(ctx, ProfileCloneRequest{Profile: web})
			if err != nil {
				t.Fatal(err)
			}
			owner := clone.Profile
			inherited, err := m.ListProfileBackups(ctx, owner)
			if err != nil || len(inherited) != 0 {
				t.Fatalf("clone inherited history: %#v %v", inherited, err)
			}
			backup, err := m.BackupProfile(ctx, ProfileBackupRequest{Profile: owner})
			if err != nil {
				t.Fatal(err)
			}
			backupPath := filepath.Join(home, "profiles", owner.Name, profileBackupFolder, backup.FileName)
			archive, err := zip.OpenReader(backupPath)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range archive.File {
				if strings.Contains(entry.Name, profileBackupFolder) {
					t.Fatal("backup includes backup history")
				}
			}
			archive.Close()
			legacy := filepath.Join(home, "profile-backups")
			if err := os.MkdirAll(legacy, 0700); err != nil {
				t.Fatal(err)
			}
			legacyFile := filepath.Join(legacy, backup.FileName)
			if err := os.Rename(backupPath, legacyFile); err != nil {
				t.Fatal(err)
			}
			if rename {
				if _, err := m.RenameProfile(ctx, ProfileRenameRequest{Profile: owner, NewName: "renamed"}); err != nil {
					t.Fatal(err)
				}
				owner.Name = "renamed"
				backups, err := m.ListProfileBackups(ctx, owner)
				if err != nil || len(backups) != 1 || backups[0].Profile != owner {
					t.Fatalf("renamed history: %#v %v", backups, err)
				}
				if _, err := m.RestoreProfileBackup(ctx, ProfileRestoreRequest{DataDirectoryID: owner.DataDirectoryID, ProfileName: owner.Name, FileName: backup.FileName}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := m.DeleteProfile(ctx, ProfileDeleteRequest{Profile: owner}); err != nil {
				t.Fatal(err)
			}
			for _, removed := range []string{legacyFile, filepath.Join(home, "profiles", owner.Name)} {
				if _, err := os.Stat(removed); !os.IsNotExist(err) {
					t.Fatalf("deleted owner retained %s: %v", removed, err)
				}
			}
			if _, err := os.Stat(filepath.Join(home, "profiles", web.Name, profileBackupFolder, other.FileName)); err != nil {
				t.Fatalf("another profile's backup was affected: %v", err)
			}
		})
	}
}

func TestProfileBackupRestoreRoundTripPreservesSource(t *testing.T) {
	m := newTestManager(t)
	m.config.ProfileCatalog = testProfileCatalog{definitions: []ProfileDefinition{{Name: "web"}}}
	ctx := context.Background()
	ref := ProfileRef{DataDirectoryID: "dsh-work", Name: "web"}
	home := m.config.DataDirectories[0].Path
	source := filepath.Join(home, "profiles", "web", "package.json")
	if err := os.WriteFile(source, []byte(`{"name":"working","dependencies":{"plugin":"1.0.0"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	backup, err := m.BackupProfile(ctx, ProfileBackupRequest{Profile: ref})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte(`{"name":"changed"}`), 0600); err != nil {
		t.Fatal(err)
	}
	list, err := m.ListProfileBackups(ctx, ref)
	if err != nil || len(list) != 1 || list[0].FileName != backup.FileName {
		t.Fatalf("list = %#v, %v", list, err)
	}
	for i := 0; i < 2; i++ {
		result, err := m.RestoreProfileBackup(ctx, ProfileRestoreRequest{DataDirectoryID: ref.DataDirectoryID, FileName: backup.FileName, ProfileName: "web"})
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(home, "profiles", result.Profile.Name, "package.json"))
		if err != nil || string(data) != `{"name":"working","dependencies":{"plugin":"1.0.0"}}` {
			t.Fatalf("restored = %s, %v", data, err)
		}
		if result.Snapshot.Configured.Profile != ref || result.Profile == ref {
			t.Fatal("restore changed the selected profile")
		}
	}
	data, _ := os.ReadFile(source)
	if string(data) != `{"name":"changed"}` {
		t.Fatal("source overwritten")
	}
}

func TestProfileRestoreRejectsUnsafeArchivesWithoutPublication(t *testing.T) {
	for _, name := range []string{"../outside", "/absolute", `C:\escape`, "nested/../../outside", "file:stream", "trailing.", "CON", "linked", "_dsh-work/extra", "missing-package", "invalid-package", "null-package", "invalid-time"} {
		t.Run(name, func(t *testing.T) {
			if name == "CON" && runtime.GOOS != "windows" {
				t.Skip("Windows reserved filename")
			}
			m := newTestManager(t)
			file := filepath.Join(t.TempDir(), "invalid.zip")
			output, err := os.Create(file)
			if err != nil {
				t.Fatal(err)
			}
			archive := zip.NewWriter(output)
			meta, _ := archive.Create("_dsh-work/manifest.json")
			createdAt := "2026-09-09T00:00:00Z"
			if name == "invalid-time" {
				createdAt = ""
			}
			json.NewEncoder(meta).Encode(profileBackupManifest{Format: "dsh-work-profile-v1", CreatedAt: createdAt, Profile: ProfileRef{DataDirectoryID: "old-home", Name: "web"}})
			if name != "missing-package" {
				payload, _ := archive.Create("package.json")
				data := `{}`
				if name == "invalid-package" {
					data = `{`
				}
				if name == "null-package" {
					data = `null`
				}
				payload.Write([]byte(data))
			}
			header := &zip.FileHeader{Name: name, Method: zip.Deflate}
			header.SetMode(0600)
			if name == "linked" {
				header.SetMode(os.ModeSymlink | 0777)
			}
			entry, _ := archive.CreateHeader(header)
			entry.Write([]byte("outside"))
			archive.Close()
			output.Close()
			_, err = m.ImportProfileBackup(context.Background(), "dsh-work", file)
			if err == nil {
				t.Fatal("unsafe archive accepted")
			}
			home := m.config.DataDirectories[0].Path
			profiles, _ := os.ReadDir(filepath.Join(home, "profiles"))
			if len(profiles) != 2 {
				t.Fatal("partial profile published")
			}
			staging, _ := filepath.Glob(filepath.Join(home, ".profile-restore-*"))
			if len(staging) != 0 {
				t.Fatal("staging leaked")
			}
		})
	}
}

func TestSafeModeHomeAndReturnTargetSurviveReload(t *testing.T) {
	m := newTestManager(t)
	original := *m.configured
	target, err := m.PrepareSafeMode(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	launch, err := m.ResolveLaunch(context.Background(), LaunchRequest{RuntimeID: target.RuntimeID, Node: target.Node, Profile: target.Profile})
	if err != nil {
		t.Fatal(err)
	}
	if launch.DataDirectory.Path == m.config.DataDirectories[0].Path {
		t.Fatal("safe mode reused original home")
	}
	if err := m.PrepareRunContext(context.Background(), launch); err != nil {
		t.Fatalf("safe mode required package installation: %v", err)
	}
	if _, err := m.SetConfigured(context.Background(), target); err != nil {
		t.Fatal(err)
	}
	reloaded, err := New(Config{StatePath: m.config.StatePath})
	if err != nil {
		t.Fatal(err)
	}
	got, err := reloaded.SafeModeReturnTarget(context.Background())
	if err != nil || got != original {
		t.Fatalf("return = %#v, %v", got, err)
	}
	if _, err := reloaded.DeleteProfile(context.Background(), ProfileDeleteRequest{Profile: original.Profile}); err == nil {
		t.Fatal("return profile deleted")
	}
	if _, err := reloaded.RemoveRuntime(context.Background(), original.RuntimeID); err == nil {
		t.Fatal("return runtime removed")
	}
}

func TestProfileRestoreRejectsLinkedProfilesDirectory(t *testing.T) {
	m := newTestManager(t)
	m.config.ProfileCatalog = testProfileCatalog{definitions: []ProfileDefinition{{Name: "web"}}}
	ctx := context.Background()
	if err := os.WriteFile(filepath.Join(m.config.DataDirectories[0].Path, "profiles", "web", "package.json"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	backup, err := m.BackupProfile(ctx, ProfileBackupRequest{Profile: ProfileRef{DataDirectoryID: "dsh-work", Name: "web"}})
	if err != nil {
		t.Fatal(err)
	}
	profiles := filepath.Join(m.config.DataDirectories[0].Path, "profiles")
	if err := os.Rename(profiles, profiles+"-original"); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, profiles); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := m.RestoreProfileBackup(ctx, ProfileRestoreRequest{DataDirectoryID: "dsh-work", FileName: backup.FileName, ProfileName: "web"}); err == nil {
		t.Fatal("published through an escaping profiles link")
	}
	entries, _ := os.ReadDir(outside)
	if len(entries) != 0 {
		t.Fatal("restore wrote outside selected home")
	}
}

func TestAbortedSafeModeRemovesUnusedHome(t *testing.T) {
	m := newTestManager(t)
	target, err := m.PrepareSafeMode(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	launch, err := m.ResolveLaunch(context.Background(), LaunchRequest{RuntimeID: target.RuntimeID, Node: target.Node, Profile: target.Profile})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.AbortPreparedSafeMode(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(launch.DataDirectory.Path); !os.IsNotExist(err) {
		t.Fatalf("unused home retained: %v", err)
	}
	reloaded, err := New(Config{StatePath: m.config.StatePath})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reloaded.SafeModeReturnTarget(context.Background()); err == nil {
		t.Fatal("unused reservation persisted")
	}
}
func TestDeleteProfileBackupOnlyRemovesSelectedArchive(t *testing.T) {
	m := newTestManager(t)
	m.config.ProfileCatalog = testProfileCatalog{definitions: []ProfileDefinition{{Name: "web"}}}
	ctx := context.Background()
	ref := ProfileRef{DataDirectoryID: "dsh-work", Name: "web"}
	if err := os.WriteFile(filepath.Join(m.config.DataDirectories[0].Path, "profiles", "web", "package.json"), []byte(`{"name":"backup-delete-test"}`), 0600); err != nil {
		t.Fatal(err)
	}

	backup, err := m.BackupProfile(ctx, ProfileBackupRequest{Profile: ref})
	if err != nil {
		t.Fatal(err)
	}
	directory, err := m.ProfileBackupDirectory(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(directory, "keep.zip")
	if err := os.WriteFile(keep, []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"../keep.zip", `..\keep.zip`, "keep.zip:stream", "package.json"} {
		if err := m.DeleteProfileBackup(ctx, ref, name); err == nil {
			t.Fatalf("accepted invalid name %q", name)
		}
	}
	if err := os.Mkdir(filepath.Join(directory, "folder.zip"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteProfileBackup(ctx, ref, "folder.zip"); err == nil {
		t.Fatal("deleted a directory")
	}
	if err := m.DeleteProfileBackup(ctx, ref, backup.FileName); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(directory, backup.FileName)); !os.IsNotExist(err) {
		t.Fatalf("archive remains: %v", err)
	}
	if data, err := os.ReadFile(keep); err != nil || string(data) != "untouched" {
		t.Fatalf("other file changed: %q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(m.config.DataDirectories[0].Path, "profiles", "web")); err != nil {
		t.Fatal(err)
	}
}
