package distribution

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestInspectPublication(t *testing.T) {
	root := t.TempDir()
	exe := filepath.Join(root, "dsh-work.exe")
	if got := Inspect(exe, "0.3.0", "amd64", ""); got.State != Missing {
		t.Fatal(got)
	}
	dir := filepath.Join(root, Directory)
	if err := os.MkdirAll(filepath.Join(dir, "locales"), 0700); err != nil {
		t.Fatal(err)
	}
	pub := Publication{SchemaVersion: 1, AppVersion: "0.3.0", CEFVersion: CEFVersion, Architecture: "amd64"}
	for _, name := range required {
		data := []byte(name)
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), data, 0600); err != nil {
			t.Fatal(err)
		}
		h := sha256.Sum256(data)
		pub.Files = append(pub.Files, File{Path: name, Size: int64(len(data)), SHA256: hex.EncodeToString(h[:])})
	}
	write := func() string {
		data, err := json.Marshal(pub)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, Manifest), data, 0600); err != nil {
			t.Fatal(err)
		}
		h := sha256.Sum256(data)
		return hex.EncodeToString(h[:])
	}
	anchor := write()
	if got := Inspect(exe, "0.3.0", "amd64", anchor); got.State != Ready || got.Executable != filepath.Join(dir, "dsh_cef_broker.exe") {
		t.Fatal(got)
	}
	if got := Inspect(exe, "0.2.9", "amd64", anchor); got.State != Incompatible {
		t.Fatal(got)
	}
	if got := Inspect(exe, "0.3.0", "arm64", anchor); got.State != Incompatible {
		t.Fatal(got)
	}
	if err := os.WriteFile(filepath.Join(dir, "unlisted.pak"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := Inspect(exe, "0.3.0", "amd64", anchor); got.State != Corrupt {
		t.Fatal(got)
	}
	if err := os.Remove(filepath.Join(dir, "unlisted.pak")); err != nil {
		t.Fatal(err)
	}
	// Rehashing a replacement DLL and rewriting the adjacent manifest cannot
	// change the digest already compiled into the application executable.
	newDLL := []byte("replacement")
	if err := os.WriteFile(filepath.Join(dir, "libcef.dll"), newDLL, 0600); err != nil {
		t.Fatal(err)
	}
	for i := range pub.Files {
		if pub.Files[i].Path == "libcef.dll" {
			h := sha256.Sum256(newDLL)
			pub.Files[i].Size = int64(len(newDLL))
			pub.Files[i].SHA256 = hex.EncodeToString(h[:])
		}
	}
	write()
	if got := Inspect(exe, "0.3.0", "amd64", anchor); got.State != Corrupt {
		t.Fatal(got)
	}
	anchor = write()
	if err := os.Remove(filepath.Join(dir, "libcef.dll")); err != nil {
		t.Fatal(err)
	}
	if got := Inspect(exe, "0.3.0", "amd64", anchor); got.State != Corrupt {
		t.Fatal(got)
	}
	pub.Files[0].Path = "../escape"
	anchor = write()
	if got := Inspect(exe, "0.3.0", "amd64", anchor); got.State != Corrupt {
		t.Fatal(got)
	}
}

func TestAdmitProfileRejectsUnknownOrDifferentEngine(t *testing.T) {
	profile := filepath.Join(t.TempDir(), "profile")
	if err := AdmitProfile(profile); err != nil {
		t.Fatal(err)
	}
	if err := AdmitProfile(profile); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profile, profileMarker), []byte(`{"cefVersion":"older"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := AdmitProfile(profile); err == nil {
		t.Fatal("older engine accepted")
	}
	unknown := filepath.Join(t.TempDir(), "unknown")
	if err := os.Mkdir(unknown, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unknown, "Cookies"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := AdmitProfile(unknown); err == nil {
		t.Fatal("unversioned data accepted")
	}
	if _, err := os.Stat(filepath.Join(unknown, "Cookies")); err != nil {
		t.Fatal(err)
	}
}

func TestMacProfileKeychainIdentity(t *testing.T) {
	profile := filepath.Join(t.TempDir(), "profile")
	if err := admitProfile(profile, true); err != nil {
		t.Fatal(err)
	}
	if err := admitProfile(profile, true); err != nil {
		t.Fatal(err)
	}
	if app, err := UsesAppMacKeychain(profile); err != nil || !app {
		t.Fatalf("new Mac profile keychain = %t, error = %v", app, err)
	}
	if err := admitProfile(profile, false); err == nil {
		t.Fatal("Mac keychain profile was accepted on another platform")
	}

	legacy := t.TempDir()
	marker := filepath.Join(legacy, profileMarker)
	original := `{"cefVersion":"` + CEFVersion + `"}`
	if err := os.WriteFile(marker, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	if err := admitProfile(legacy, true); err != nil {
		t.Fatal(err)
	}
	if app, err := UsesAppMacKeychain(legacy); err != nil || app {
		t.Fatalf("legacy keychain = %t, error = %v", app, err)
	}
	if saved, err := os.ReadFile(marker); err != nil || string(saved) != original {
		t.Fatalf("legacy identity was modified: %q (%v)", saved, err)
	}
	if err := os.WriteFile(marker, []byte(`{"cefVersion":"`+CEFVersion+`","macKeychain":"unknown"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := UsesAppMacKeychain(legacy); err == nil {
		t.Fatal("unknown keychain identity was accepted")
	}
	if app, err := UsesAppMacKeychain(t.TempDir()); err != nil || app {
		t.Fatalf("unmarked fixture keychain = %t, error = %v", app, err)
	}
}

func TestMacProfileAdmissionRecoversInterruptedMarkerWrite(t *testing.T) {
	for _, test := range []struct {
		name, suffix               string
		directory, cookies, reject bool
	}{
		{name: "staging file only", suffix: "327680001"},
		{name: "staging file and browser data", suffix: "327680001", cookies: true, reject: true},
		{name: "staging directory", suffix: "327680001", directory: true, reject: true},
		{name: "unrecognized staging name", suffix: "backup", reject: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			profile := t.TempDir()
			stage := filepath.Join(profile, profileMarkerStagePrefix+test.suffix)
			if test.directory {
				if err := os.Mkdir(stage, 0700); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.WriteFile(stage, []byte(`{"cefVersion":`), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if test.cookies {
				if err := os.WriteFile(filepath.Join(profile, "Cookies"), []byte("keep"), 0600); err != nil {
					t.Fatal(err)
				}
			}

			err := admitProfile(profile, true)
			if test.reject {
				if err == nil || err.Error() != "CEF_PROFILE_ENGINE_UNKNOWN" {
					t.Fatalf("unknown profile data was not rejected: %v", err)
				}
				if _, statErr := os.Stat(filepath.Join(profile, profileMarker)); !os.IsNotExist(statErr) {
					t.Fatalf("unexpected profile marker after rejection: %v", statErr)
				}
				if test.cookies {
					data, err := os.ReadFile(filepath.Join(profile, "Cookies"))
					if err != nil || string(data) != "keep" {
						t.Fatalf("browser data changed after rejection: %q (%v)", data, err)
					}
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if appKeychain, err := UsesAppMacKeychain(profile); err != nil || !appKeychain {
				t.Fatalf("recovered profile did not retain the app keychain policy: %t (%v)", appKeychain, err)
			}
		})
	}
}
