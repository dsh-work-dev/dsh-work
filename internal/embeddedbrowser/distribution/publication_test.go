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
