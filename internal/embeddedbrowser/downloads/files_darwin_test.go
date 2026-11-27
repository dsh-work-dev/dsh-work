//go:build darwin

package downloads

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func makeDownload(t *testing.T, path string, overwrite bool) *Destination {
	t.Helper()
	d, err := PrepareDestination(path, overwrite)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	writer, err := d.OpenWriter()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = writer.Read(make([]byte, 1)); err == nil {
		_ = writer.Close()
		t.Fatal("download writer is readable")
	}
	_, err = writer.WriteString("new content")
	if err = errors.Join(err, writer.Close()); err != nil {
		t.Fatal(err)
	}
	return d
}

func saveFixture(t *testing.T, path, value string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(value), 0600); err != nil {
		t.Fatal(err)
	}
}

func expectFile(t *testing.T, path, expected string) {
	t.Helper()
	value, err := os.ReadFile(path)
	if err != nil || string(value) != expected {
		t.Fatalf("read %s = %q, %v, want %q", path, value, err, expected)
	}
}

func TestMacDownloadWritesAndPublishesOwnedStage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "result.txt")
	d := makeDownload(t, path, false)
	if mime, err := d.ContentType(); err != nil || !strings.HasPrefix(mime, "text/plain") {
		t.Fatalf("content type = %q, %v", mime, err)
	}
	var durable FileRecord
	if err := d.Publish(func(r FileRecord) error { durable = r; return nil }); err != nil {
		t.Fatal(err)
	}
	expectFile(t, path, "new content")
	if !durable.Published || durable.Phase != "published" {
		t.Fatalf("publication journal = %#v", durable)
	}
	if _, err := os.Stat(filepath.Join(durable.Parent, filepath.Dir(durable.Stage))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("private stage remains after completion: %v", err)
	}
}

func TestMacDownloadNeverReplacesAnUnapprovedCollision(t *testing.T) {
	path := filepath.Join(t.TempDir(), "result.txt")
	d := makeDownload(t, path, false)
	saveFixture(t, path, "late occupant")
	if err := d.Publish(func(FileRecord) error { return nil }); err == nil {
		t.Fatal("late occupant was overwritten")
	}
	if err := d.Abort(); err != nil {
		t.Fatal(err)
	}
	expectFile(t, path, "late occupant")
}

func TestMacDownloadRetainsAndRestoresApprovedOriginalAfterCollision(t *testing.T) {
	path := filepath.Join(t.TempDir(), "result.txt")
	saveFixture(t, path, "previous")
	d := makeDownload(t, path, true)
	var durable FileRecord
	err := d.Publish(func(r FileRecord) error {
		durable = r
		if r.Phase == "previous-moved" {
			saveFixture(t, path, "late occupant")
		}
		return nil
	})
	if err == nil || durable.Recovery == "" {
		t.Fatalf("expected retained original: %v, %#v", err, durable)
	}
	expectFile(t, path, "late occupant")
	expectFile(t, filepath.Join(durable.Parent, durable.Recovery), "previous")
	if err = d.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = RecoverFile(durable); !errors.Is(err, ErrDestinationChanged) {
		t.Fatalf("recovery did not refuse to overwrite a late occupant: %v", err)
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	recovered, err := RecoverFile(durable)
	if err != nil || recovered.Published || recovered.Recovery != "" || recovered.Phase != "discarded" {
		t.Fatalf("recovery = %#v, %v", recovered, err)
	}
	expectFile(t, path, "previous")
}

func TestMacDownloadRecoverDiscoversPublicationBeforeLedgerUpdate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "result.txt")
	saveFixture(t, path, "previous")
	d := makeDownload(t, path, true)
	saveFailed := errors.New("journal interrupted")
	var durable FileRecord
	err := d.Publish(func(r FileRecord) error {
		if r.Published {
			return saveFailed
		}
		durable = r
		return nil
	})
	if !errors.Is(err, saveFailed) || durable.Recovery == "" {
		t.Fatalf("expected pre-publication journal and a retained original: %v, %#v", err, durable)
	}
	if err = d.Close(); err != nil {
		t.Fatal(err)
	}
	recovered, err := RecoverFile(durable)
	if err != nil || !recovered.Published || recovered.Recovery == "" {
		t.Fatalf("recovered = %#v, %v", recovered, err)
	}
	expectFile(t, path, "new content")
	old := filepath.Join(recovered.Parent, recovered.Recovery)
	expectFile(t, old, "previous")
	cleaned, err := CleanupPublishedRecovery(recovered)
	if err != nil || cleaned.Recovery != "" || !cleaned.Published {
		t.Fatalf("cleanup = %#v, %v", cleaned, err)
	}
	if _, err = os.Stat(old); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("owned recovery file was not cleaned: %v", err)
	}
	expectFile(t, path, "new content")
}

func TestMacDownloadKeepsOriginalWhenPublishedNameChangesBeforeCleanup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "result.txt")
	saveFixture(t, path, "previous")
	d := makeDownload(t, path, true)
	err := d.Publish(func(r FileRecord) error {
		if r.Published {
			if err := os.Rename(path, path+".moved"); err != nil {
				t.Fatal(err)
			}
			saveFixture(t, path, "unrelated new file")
		}
		return nil
	})
	if !errors.Is(err, ErrDestinationChanged) {
		t.Fatalf("publish after late replacement = %v", err)
	}
	record := d.Record()
	if record.Published || record.Recovery == "" {
		t.Fatalf("original was not retained for recovery: %#v", record)
	}
	expectFile(t, filepath.Join(record.Parent, record.Recovery), "previous")
	expectFile(t, path, "unrelated new file")
	expectFile(t, path+".moved", "new content")
}

func TestMacDownloadRefusesReplacedParentAndUnownedStage(t *testing.T) {
	t.Run("parent replaced", func(t *testing.T) {
		container := t.TempDir()
		parent := filepath.Join(container, "approved")
		if err := os.Mkdir(parent, 0700); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(parent, "result.txt")
		d := makeDownload(t, path, false)
		if err := os.Rename(parent, filepath.Join(container, "retired")); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(parent, 0700); err != nil {
			t.Fatal(err)
		}
		saveFixture(t, path, "unrelated")
		if err := d.Publish(func(FileRecord) error { return nil }); !errors.Is(err, ErrDestinationChanged) {
			t.Fatalf("publication after parent replacement = %v", err)
		}
		if err := d.Abort(); err != nil {
			t.Fatal(err)
		}
		expectFile(t, path, "unrelated")
	})
	t.Run("stage substituted", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "result.txt")
		d := makeDownload(t, path, false)
		r := d.Record()
		stage := filepath.Join(r.Parent, r.Stage)
		if err := os.Rename(stage, stage+".original"); err != nil {
			t.Fatal(err)
		}
		saveFixture(t, stage, "unrelated")
		if writer, err := d.OpenWriter(); !errors.Is(err, ErrOwnedFileChanged) {
			if writer != nil {
				_ = writer.Close()
			}
			t.Fatalf("substituted writer = %v, %v", writer, err)
		}
		if err := d.Abort(); !errors.Is(err, ErrOwnedFileChanged) {
			t.Fatalf("cleanup after stage substitution = %v", err)
		}
		expectFile(t, stage, "unrelated")
	})
}
