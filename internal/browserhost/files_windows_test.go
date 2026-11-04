//go:build windows

package browserhost

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// File-only preflight. These tests never create browser registry entries.
type testReplace struct{}

func (testReplace) Replace(from, to string) error { return os.Rename(from, to) }
func TestNativeManifestOwnershipAndPathUpdate(t *testing.T) {
	root := t.TempDir()
	exe := filepath.Join(root, "dsh-work.exe")
	if e := os.WriteFile(exe, []byte("fixture, not executed"), 0600); e != nil {
		t.Fatal(e)
	}
	r := &registrar{config: Config{Directory: filepath.Join(root, "native"), Executable: exe, Identity: "scratch-owner", Replacer: testReplace{}}}
	if e := r.prepare(); e != nil {
		t.Fatal(e)
	}
	path, owner := r.paths()
	if !r.owns(path) {
		t.Fatal("own manifest not recognized")
	}
	other := &registrar{config: r.config}
	other.config.Identity = "another-owner"
	if other.owns(path) {
		t.Fatal("another identity claimed manifest")
	}
	moved := filepath.Join(root, "moved.exe")
	if e := os.WriteFile(moved, []byte("fixture, not executed"), 0600); e != nil {
		t.Fatal(e)
	}
	r.config.Executable = moved
	if e := r.prepare(); e != nil {
		t.Fatal(e)
	}
	var m manifest
	if e := readSmallJSON(path, &m); e != nil || m.Path != moved {
		t.Fatal(m, e)
	}
	foreign := manifest{Name: Name, Path: exe, Type: "stdio", Origins: []string{"chrome-extension://aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/"}}
	b, _ := json.Marshal(foreign)
	if e := os.WriteFile(path, b, 0600); e != nil {
		t.Fatal(e)
	}
	if r.owns(path) {
		t.Fatal("foreign origins treated as owned")
	}
	if e := r.prepare(); e == nil {
		t.Fatal("overwrote altered manifest")
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(b) {
		t.Fatal("changed foreign manifest")
	}
	if e := os.Remove(path); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(owner, []byte(`{"application":"other"}`), 0600); e != nil {
		t.Fatal(e)
	}
	if e := r.prepare(); e == nil {
		t.Fatal("overwrote orphaned foreign owner record")
	}
}
func TestDevelopmentBundleExcludesFaultControls(t *testing.T) {
	c := Config{Directory: t.TempDir(), Identity: "scratch-owner", Replacer: testReplace{}}
	if e := InstallDevelopmentBundle(context.Background(), c); e != nil {
		t.Fatal(e)
	}
	dir := DevelopmentDirectory(c)
	if _, e := os.Stat(filepath.Join(dir, "acceptance-worker.js")); !os.IsNotExist(e) {
		t.Fatal("fault controls shipped")
	}
	if _, e := os.Stat(filepath.Join(dir, "background.js")); e != nil {
		t.Fatal(e)
	}
	if e := InstallDevelopmentBundle(context.Background(), c); e != nil {
		t.Fatal("repeat owned extraction failed", e)
	}
	c.Identity = "another-owner"
	if e := InstallDevelopmentBundle(context.Background(), c); e == nil {
		t.Fatal("overwrote another owner's module")
	}
	for _, path := range []string{`\\server\share\app.exe`, `//server/share/app.exe`, `\\?\C:\app.exe`, `relative.exe`} {
		if localAbsolute(path) {
			t.Fatal("nonlocal or device path accepted", path)
		}
	}
}
