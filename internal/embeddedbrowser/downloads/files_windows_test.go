//go:build windows

package downloads

import (
	"bytes"
	"encoding/json"
	"errors"
	win "golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"testing"
)

func journalTo(t *testing.T, dir string) func(FileRecord) error {
	t.Helper()
	return func(r FileRecord) error {
		b, e := json.Marshal(r)
		if e != nil {
			return e
		}
		f, e := os.OpenFile(filepath.Join(dir, "journal.json"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
		if e != nil {
			return e
		}
		_, e = f.Write(b)
		if e == nil {
			e = f.Sync()
		}
		return errors.Join(e, f.Close())
	}
}
func TestNewDestinationNativeHandlePublish(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "普通 文档.txt")
	d, e := PrepareDestination(target, false)
	if e != nil {
		t.Fatal(e)
	}
	defer d.Close()
	remote, e := d.DuplicateWriter(win.CurrentProcess())
	if e != nil {
		t.Fatal(e)
	}
	h := win.Handle(remote)
	content := []byte("owned download\n")
	var n uint32
	if e = win.WriteFile(h, content, &n, nil); e != nil || n != uint32(len(content)) {
		t.Fatalf("write %d %v", n, e)
	}
	var read [1]byte
	if e = win.ReadFile(h, read[:], &n, nil); !errors.Is(e, win.ERROR_ACCESS_DENIED) {
		t.Fatalf("write capability allowed read: %v", e)
	}
	win.CloseHandle(h)
	if e = d.Publish(journalTo(t, dir)); e != nil {
		t.Fatal(e)
	}
	got, e := os.ReadFile(target)
	if e != nil || !bytes.Equal(got, content) {
		t.Fatalf("published %q %v", got, e)
	}
	if e = d.Abort(); e != nil {
		t.Fatal(e)
	} // Completed files are not revoked/deleted.
	if _, e = os.Stat(target); e != nil {
		t.Fatal(e)
	}
}
func TestNewDestinationCollisionDoesNotOverwrite(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "new.bin")
	d, e := PrepareDestination(target, false)
	if e != nil {
		t.Fatal(e)
	}
	defer d.Close()
	d.Write([]byte("download"))
	os.WriteFile(target, []byte("new occupant"), 0600)
	if e = d.Publish(journalTo(t, dir)); e == nil {
		t.Fatal("collision overwrote destination")
	}
	if e = d.Abort(); e != nil {
		t.Fatal(e)
	}
	d.Close()
	got, e := os.ReadFile(target)
	if e != nil || string(got) != "new occupant" {
		t.Fatalf("occupant %q %v", got, e)
	}
	if _, e = os.Stat(filepath.Join(dir, d.Record().Stage)); !errors.Is(e, os.ErrNotExist) {
		t.Fatalf("partial retained: %v", e)
	}
}
func TestOverwriteUsesHeldOriginalAndNoReplaceCommit(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "existing.txt")
	os.WriteFile(target, []byte("original"), 0600)
	d, e := PrepareDestination(target, true)
	if e != nil {
		t.Fatal(e)
	}
	defer d.Close()
	d.Write([]byte("replacement"))
	journal := journalTo(t, dir)
	sawRecovery := false
	e = d.Publish(func(r FileRecord) error {
		if e := journal(r); e != nil {
			return e
		}
		if r.Phase == "previous-moved" {
			sawRecovery = true
			// Competing real file at the vacated path, after the durable recovery intent.
			return os.WriteFile(target, []byte("competing writer"), 0600)
		}
		return nil
	})
	if e == nil || !sawRecovery {
		t.Fatalf("collision %v recovery=%t", e, sawRecovery)
	}
	r := d.Record()
	if r.Recovery == "" {
		t.Fatal("lost recovery name")
	}
	got, _ := os.ReadFile(target)
	if string(got) != "competing writer" {
		t.Fatalf("wrong file altered: %q", got)
	}
	original, e := os.ReadFile(filepath.Join(dir, r.Recovery))
	if e != nil || string(original) != "original" {
		t.Fatalf("original lost %q %v", original, e)
	}
	if e = d.Abort(); e != nil {
		t.Fatal(e)
	}
}
func TestOverwriteChangedObjectRejected(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "existing.txt")
	os.WriteFile(target, []byte("old"), 0600)
	d, e := PrepareDestination(target, true)
	if e != nil {
		t.Fatal(e)
	}
	defer d.Close()
	defer d.Abort()
	os.Remove(target)
	os.WriteFile(target, []byte("other"), 0600)
	if e = d.Publish(journalTo(t, dir)); !errors.Is(e, ErrDestinationChanged) {
		t.Fatalf("changed target: %v", e)
	}
	b, _ := os.ReadFile(target)
	if string(b) != "other" {
		t.Fatalf("changed target altered: %q", b)
	}
}
func TestOverwriteNormalSuccessCleansOnlyApprovedOldObject(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "existing.txt")
	os.WriteFile(target, []byte("old"), 0600)
	d, e := PrepareDestination(target, true)
	if e != nil {
		t.Fatal(e)
	}
	defer d.Close()
	d.Write([]byte("new"))
	if e = d.Publish(journalTo(t, dir)); e != nil {
		t.Fatal(e)
	}
	r := d.Record()
	if r.Recovery != "" || !r.Published {
		t.Fatalf("record %#v", r)
	}
	b, e := os.ReadFile(target)
	if e != nil || string(b) != "new" {
		t.Fatalf("contents %q %v", b, e)
	}
	entries, _ := os.ReadDir(dir)
	for _, v := range entries {
		if v.Name() != "existing.txt" && v.Name() != "journal.json" {
			t.Fatalf("unmanaged leftover %s", v.Name())
		}
	}
}

func TestAttachmentServicesOwnedStage(t *testing.T) {
	dir := t.TempDir()
	d, err := PrepareDestination(filepath.Join(dir, "fixture.txt"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	defer d.Abort()
	if _, err = d.Write([]byte("DSH isolated attachment fixture\n")); err != nil {
		t.Fatal(err)
	}
	if err = d.CheckRisk("https://download-fixture.invalid/fixture.txt", "https://download-fixture.invalid/source"); err != nil {
		t.Fatal(err)
	}
	if err = d.Publish(journalTo(t, dir)); err != nil {
		t.Fatal(err)
	}
	zone, err := os.ReadFile(filepath.Join(dir, "fixture.txt") + ":Zone.Identifier")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(zone, []byte("[ZoneTransfer]")) || !bytes.Contains(zone, []byte("ZoneId=")) {
		t.Fatalf("invalid source mark %q", zone)
	}
	t.Logf("Attachment Services Save produced source zone on owned published fixture: %s", zone)
}

func TestAttachmentPathAncestorRenameWhileScannerLeaseHeld(t *testing.T) {
	root := t.TempDir()
	ancestor := filepath.Join(root, "ancestor")
	parent := filepath.Join(ancestor, "destination")
	if err := os.MkdirAll(parent, 0700); err != nil {
		t.Fatal(err)
	}
	d, err := PrepareDestination(filepath.Join(parent, "fixture.txt"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	defer d.Abort()
	d.Write([]byte("owned ancestor-race fixture\n"))
	err = d.checkRisk("https://download-fixture.invalid/fixture.txt", "https://download-fixture.invalid/source", func(path, name, source, referrer string) error {
		err := os.Rename(ancestor, filepath.Join(root, "moved-ancestor"))
		if err == nil {
			t.Fatal("ancestor rename succeeded while AES path lease held")
		}
		t.Logf("actual Windows ancestor rename denied during scanner lease: %v", err)
		return saveAttachment(path, name, source, referrer)
	})
	if err != nil {
		t.Fatal(err)
	}
}
