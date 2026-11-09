//go:build windows

package downloads

import (
	"context"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	win "golang.org/x/sys/windows"
)

func fixturePending() Pending {
	return Pending{ID: rand.Text(), Generation: "owned-generation", Owner: "owned-tab", Profile: "owned-profile", RequestID: "unique-fetch-request", FrameID: "source-frame", Document: "source-document", Presentation: "visible-epoch", SourceOrigin: "https://download-fixture.invalid", SourceURL: "https://download-fixture.invalid/source?token=source-secret", URL: "https://download-fixture.invalid/fixture.txt?token=download-secret", Method: "GET", MIME: "text/plain", SuggestedName: "fixture.txt", TotalBytes: -1}
}
func fixtureManager(t *testing.T, dir string) *Manager {
	t.Helper()
	m, e := OpenManager(dir, Policy{MaxBytes: 64 << 20, MaxConcurrent: 2}, nil)
	if e != nil {
		t.Fatal(e)
	}
	return m
}
func TestManagerRealFileCompleteAndRestartDoesNotReplay(t *testing.T) {
	dir := t.TempDir()
	m := fixtureManager(t, filepath.Join(dir, "ledger"))
	p := fixturePending()
	if e := m.Offer(p); e != nil {
		t.Fatal(e)
	}
	id, e := m.Approve(context.Background(), p.ID, Selection{Path: filepath.Join(dir, "fixture.txt")}, func(context.Context, Pending) bool { return true })
	if e != nil {
		t.Fatal(e)
	}
	if e = m.Arm(id, func(context.Context) error { return nil }); e != nil {
		t.Fatal(e)
	}
	h, e := m.DuplicateWriter(id, win.CurrentProcess())
	if e != nil {
		t.Fatal(e)
	}
	data := []byte("isolated manager streamed file\n")
	var written uint32
	if e = win.WriteFile(win.Handle(h), data, &written, nil); e != nil {
		t.Fatal(e)
	}
	win.CloseHandle(win.Handle(h))
	if e = m.Running(id); e != nil {
		t.Fatal(e)
	}
	if e = m.Progress(id, int64(written), int64(written)); e != nil {
		t.Fatal(e)
	}
	if e = m.Complete(id, nil); e != nil {
		t.Fatal(e)
	}
	state := m.Snapshots()
	if len(state) != 1 || state[0].State != "complete" {
		t.Fatalf("state %#v", state)
	}
	raw, e := os.ReadFile(filepath.Join(dir, "ledger", id+".json"))
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(raw), "source-secret") || strings.Contains(string(raw), "download-secret") {
		t.Fatal("URL credentials persisted")
	}
	restored := fixtureManager(t, filepath.Join(dir, "ledger"))
	if _, _, e = restored.StartData(id); !errors.Is(e, ErrApprovalExpired) {
		t.Fatalf("old grant restarted: %v", e)
	}
	if e = restored.Cancel(context.Background(), id, true); e != nil {
		t.Fatal(e)
	}
	got, e := os.ReadFile(filepath.Join(dir, "fixture.txt"))
	if e != nil || string(got) != string(data) {
		t.Fatalf("completed user file altered %q %v", got, e)
	}
}
func TestManagerReceivedBytesRevokeWaitsForDrainAndStartupCleansOwnedPartial(t *testing.T) {
	dir := t.TempDir()
	ledger := filepath.Join(dir, "ledger")
	m := fixtureManager(t, ledger)
	p := fixturePending()
	m.Offer(p)
	id, e := m.Approve(context.Background(), p.ID, Selection{Path: filepath.Join(dir, "canceled.txt")}, func(context.Context, Pending) bool { return true })
	if e != nil {
		t.Fatal(e)
	}
	canceled := false
	if e = m.Arm(id, func(context.Context) error { canceled = true; return nil }); e != nil {
		t.Fatal(e)
	}
	h, e := m.DuplicateWriter(id, win.CurrentProcess())
	if e != nil {
		t.Fatal(e)
	}
	var written uint32
	if e = win.WriteFile(win.Handle(h), []byte("received actual bytes"), &written, nil); e != nil {
		t.Fatal(e)
	}
	m.Running(id)
	m.Progress(id, int64(written), 0)
	if e = m.Cancel(context.Background(), id, true); e != nil || !canceled {
		t.Fatalf("cancel %v %t", e, canceled)
	}
	if m.Snapshots()[0].State != "stopping" {
		t.Fatal("cleanup claimed before writer drain")
	}
	win.CloseHandle(win.Handle(h))
	if e = m.Drained(id, "", int64(written)); e != nil {
		t.Fatal(e)
	}
	if m.Snapshots()[0].State != "revoked" {
		t.Fatalf("state %#v", m.Snapshots())
	}
	files, _ := os.ReadDir(dir)
	for _, f := range files {
		if strings.HasPrefix(f.Name(), ".dsh-download-") {
			t.Fatalf("partial remains %s", f.Name())
		}
	}
	// A separate transaction loses its owner after real bytes, with the durable
	// ledger left active. Close only its handles to simulate post-process drain.
	p = fixturePending()
	m.Offer(p)
	id, e = m.Approve(context.Background(), p.ID, Selection{Path: filepath.Join(dir, "interrupted.txt")}, func(context.Context, Pending) bool { return true })
	if e != nil {
		t.Fatal(e)
	}
	m.transfers[id].destination.Write([]byte("interrupted bytes"))
	record := m.transfers[id].destination.Record()
	m.transfers[id].destination.Close()
	restored := fixtureManager(t, ledger)
	if _, _, e = restored.StartData(id); !errors.Is(e, ErrApprovalExpired) {
		t.Fatalf("crash grant reused %v", e)
	}
	if _, e = os.Stat(filepath.Join(record.Parent, record.Stage)); !errors.Is(e, os.ErrNotExist) {
		t.Fatalf("owned crash partial not cleaned: %v", e)
	}
	p = fixturePending()
	m.Offer(p)
	m.Invalidate(p.Owner, "new-document", p.Presentation)
	if _, e = m.Approve(context.Background(), p.ID, Selection{Path: filepath.Join(dir, "stale.txt")}, func(context.Context, Pending) bool { return true }); !errors.Is(e, ErrApprovalExpired) {
		t.Fatalf("stale approval: %v", e)
	}
}

func TestCancelStopsIssuedWriterWhenJournalCannotPersist(t *testing.T) {
	dir := t.TempDir()
	ledger := filepath.Join(dir, "ledger")
	m := fixtureManager(t, ledger)
	p := fixturePending()
	m.Offer(p)
	id, e := m.Approve(context.Background(), p.ID, Selection{Path: filepath.Join(dir, "cancel-journal.txt")}, func(context.Context, Pending) bool { return true })
	if e != nil {
		t.Fatal(e)
	}
	var remote uintptr
	called := false
	if e = m.Arm(id, func(ctx context.Context) error {
		called = true
		if ctx.Err() != nil {
			t.Fatal("cancel inherited an already expired request context")
		}
		if e := win.CloseHandle(win.Handle(remote)); e != nil {
			t.Fatal(e)
		}
		return m.Drained(id, "", 4)
	}); e != nil {
		t.Fatal(e)
	}
	remote, e = m.DuplicateWriter(id, win.CurrentProcess())
	if e != nil {
		t.Fatal(e)
	}
	var written uint32
	if e = win.WriteFile(win.Handle(remote), []byte("real"), &written, nil); e != nil {
		t.Fatal(e)
	}
	m.Running(id)
	m.Progress(id, int64(written), 0)
	offline := ledger + "-offline"
	if e = os.Rename(ledger, offline); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(ledger, []byte("not a directory"), 0600); e != nil {
		t.Fatal(e)
	}
	ctx, stop := context.WithCancel(context.Background())
	stop()
	e = m.Cancel(ctx, id, true)
	if e == nil || !called {
		t.Fatalf("journal failure lost or writer not canceled: %v called=%t", e, called)
	}
	if m.Snapshots()[0].State != "revoked" {
		t.Fatalf("state %#v", m.Snapshots())
	}
	if e = os.Remove(ledger); e != nil {
		t.Fatal(e)
	}
	if e = os.Rename(offline, ledger); e != nil {
		t.Fatal(e)
	}
	restored := fixtureManager(t, ledger)
	if _, _, e = restored.StartData(id); !errors.Is(e, ErrApprovalExpired) {
		t.Fatalf("unpersisted revoke restarted grant: %v", e)
	}
	for _, f := range mustReadDir(t, dir) {
		if strings.HasPrefix(f.Name(), ".dsh-download-") {
			t.Fatalf("partial remains: %s", f.Name())
		}
	}
}
func mustReadDir(t *testing.T, path string) []os.DirEntry {
	t.Helper()
	entries, e := os.ReadDir(path)
	if e != nil {
		t.Fatal(e)
	}
	return entries
}

func TestRiskReopenFailureRetainsCleanupLedgerUntilOwnedRecovery(t *testing.T) {
	dir := t.TempDir()
	ledger := filepath.Join(dir, "ledger")
	m := fixtureManager(t, ledger)
	p := fixturePending()
	m.Offer(p)
	id, e := m.Approve(context.Background(), p.ID, Selection{Path: filepath.Join(dir, "risk-reopen.txt")}, func(context.Context, Pending) bool { return true })
	if e != nil {
		t.Fatal(e)
	}
	tx := m.transfers[id]
	d := tx.destination
	d.Write([]byte("isolated risk reopen fixture\n"))
	var blocker win.Handle
	e = d.checkRisk(p.URL, p.SourceURL, func(path, name, source, referrer string) error {
		if e := saveAttachment(path, name, source, referrer); e != nil {
			return e
		}
		// A real lingering read handle, as a trust-service client may retain, makes
		// the DELETE-access reopen fail. The actual AES Save still ran above.
		var e error
		blocker, e = openChild(d.parent, d.record.Stage, win.FILE_GENERIC_READ, win.FILE_SHARE_READ|win.FILE_SHARE_WRITE, win.FILE_OPEN)
		return e
	})
	if e == nil || d.stage != nil {
		t.Fatalf("expected released stage + failed reopen: %v", e)
	}
	if ae := d.Abort(); ae == nil {
		t.Fatal("missing stage handle was reported as successful cleanup")
	}
	record := d.Record()
	m.mu.Lock()
	failure := m.failLocked(tx, "DOWNLOAD_RISK_CHECK_FAILED")
	m.mu.Unlock()
	if failure == nil || !tx.entry.CleanupRequired || tx.entry.File.Phase == "discarded" {
		t.Fatalf("cleanup state lost: %v %#v", failure, tx.entry)
	}
	win.CloseHandle(blocker)
	if _, e = os.Stat(filepath.Join(record.Parent, record.Stage)); e != nil {
		t.Fatalf("owned residual unexpectedly absent before recovery: %v", e)
	}
	restored := fixtureManager(t, ledger)
	if _, e = os.Stat(filepath.Join(record.Parent, record.Stage)); !errors.Is(e, os.ErrNotExist) {
		t.Fatalf("owned residual not reclaimed: %v", e)
	}
	for _, s := range restored.Snapshots() {
		if s.ID == id && s.CleanupRequired {
			t.Fatal("recovery still claims cleanup required")
		}
	}
}
