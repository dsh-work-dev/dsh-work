//go:build darwin && cgo

package downloads

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestMacDownloadLedgerArmsWriterLabelsContentAndReopens(t *testing.T) {
	root := t.TempDir()
	ledger := filepath.Join(root, "ledger")
	policy := Policy{MaxBytes: 1 << 20, MaxConcurrent: 1}
	m, err := OpenManager(ledger, policy, nil)
	if err != nil {
		t.Fatal(err)
	}
	p := Pending{
		ID: rand.Text(), Generation: "browser-instance", Owner: "tab", Profile: "profile",
		RequestID: "request", FrameID: "frame", Document: "document", Presentation: "visible-epoch",
		SourceOrigin: "https://fixture.invalid", SourceURL: "https://fixture.invalid/view?token=origin-secret",
		URL: "https://fixture.invalid/result.txt?token=download-secret", Method: "GET", MIME: "text/plain",
		SuggestedName: "result.txt", TotalBytes: 12,
	}
	if err = m.Offer(p); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, p.SuggestedName)
	grant, err := m.Approve(context.Background(), p.ID, Selection{Path: path}, func(_ context.Context, offered Pending) bool { return offered == p })
	if err != nil {
		t.Fatal(err)
	}
	assertNoWriter := func() {
		t.Helper()
		file, err := m.OpenWriter(grant)
		if file != nil {
			_ = file.Close()
		}
		if !errors.Is(err, ErrApprovalExpired) {
			t.Fatalf("unexpected writer after grant check: %v", err)
		}
	}
	assertNoWriter()
	if err = m.Arm(grant, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	file, err := m.OpenWriter(grant)
	if err != nil {
		t.Fatal(err)
	}
	assertNoWriter()
	_, err = file.WriteString("new content")
	if err = errors.Join(err, file.Close()); err != nil {
		t.Fatal(err)
	}
	if err = m.Running(grant); err != nil {
		t.Fatal(err)
	}
	if err = m.Progress(grant, 11, 12); err != nil {
		t.Fatal(err)
	}
	if err = m.Complete(grant, nil); err != nil {
		t.Fatal(err)
	}
	expectFile(t, path, "new content")
	if n, err := unix.Getxattr(path, "com.apple.quarantine", nil); err != nil || n == 0 {
		t.Fatalf("download quarantine was not retained after publication: %d, %v", n, err)
	}
	journal, err := os.ReadFile(filepath.Join(ledger, grant+".json"))
	if err != nil || strings.Contains(string(journal), "-secret") {
		t.Fatalf("invalid or unredacted download journal: %v", err)
	}
	reopened, err := OpenManager(ledger, policy, nil)
	if err != nil {
		t.Fatal(err)
	}
	state := reopened.Snapshots()
	if len(state) != 1 || state[0].State != "complete" || state[0].CleanupRequired {
		t.Fatalf("reopened download state = %#v", state)
	}
}

func TestMacDownloadStartFailureWaitsForDrainAndPreservesAnExplicitStop(t *testing.T) {
	for _, tc := range []struct{ name, state, reason string }{
		{"native start failed", "failed", "DOWNLOAD_WRITER_REQUIRED"},
		{"explicitly revoked", "revoked", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			m, err := OpenManager(filepath.Join(root, "ledger"), Policy{MaxBytes: 4096, MaxConcurrent: 1}, nil)
			if err != nil {
				t.Fatal(err)
			}
			p := Pending{
				ID: rand.Text(), Generation: "browser", Owner: "tab", Profile: "profile",
				RequestID: "request", FrameID: "frame", Document: "document", Presentation: "visible",
				SourceOrigin: "https://fixture.invalid", SourceURL: "https://fixture.invalid/view",
				URL: "https://fixture.invalid/result.txt", Method: "GET", MIME: "text/plain",
			}
			if err := m.Offer(p); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "result.txt")
			id, err := m.Approve(ctx, p.ID, Selection{Path: path}, func(context.Context, Pending) bool { return true })
			if err != nil {
				t.Fatal(err)
			}
			cancelCalls := 0
			if err := m.Arm(id, func(context.Context) error { cancelCalls++; return nil }); err != nil {
				t.Fatal(err)
			}
			writer, err := m.OpenWriter(id)
			if err != nil {
				t.Fatal(err)
			}
			if tc.state == "revoked" {
				if err := m.Cancel(ctx, id, true); err != nil {
					t.Fatal(err)
				}
			}
			if err := m.FailStart(ctx, id); err != nil {
				t.Fatal(err)
			}
			stages, err := filepath.Glob(filepath.Join(root, ".dsh-download-*"))
			if err != nil || len(stages) != 1 || cancelCalls != 1 || m.Snapshots()[0].State != "stopping" {
				t.Fatalf("start failure bypassed the drain: state=%#v cancels=%d stages=%v (%v)", m.Snapshots(), cancelCalls, stages, err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			if err := m.Drained(id, "DOWNLOAD_WRITER_REQUIRED", 0); err != nil {
				t.Fatal(err)
			}
			got := m.Snapshots()[0]
			if got.State != tc.state || got.Error != tc.reason {
				t.Fatalf("terminal transfer = %#v", got)
			}
			for _, name := range []string{path, stages[0]} {
				if _, err := os.Lstat(name); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("download path remains: %s (%v)", name, err)
				}
			}
		})
	}
}

func TestMacDownloadAbandonedStageBecomesAnonymousWithWriterOpen(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "download.txt")
	if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	manager, err := OpenManager(filepath.Join(root, "ledger"), Policy{MaxBytes: 4096, MaxConcurrent: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	pending := Pending{
		ID: rand.Text(), Generation: "failed-browser", Owner: "tab", Profile: "profile",
		RequestID: "request", FrameID: "frame", Document: "document", Presentation: "visible",
		SourceOrigin: "https://fixture.invalid", SourceURL: "https://fixture.invalid/view",
		URL: "https://fixture.invalid/download.txt", Method: "GET", MIME: "text/plain",
	}
	if err := manager.Offer(pending); err != nil {
		t.Fatal(err)
	}
	destination, err := PrepareDestination(path, true)
	if err != nil {
		t.Fatal(err)
	}
	stage := destination.Record().Stage
	id, err := manager.Approve(context.Background(), pending.ID, Selection{Path: path, Prepared: destination, OverwriteConfirmed: true}, func(context.Context, Pending) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Arm(id, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	writer, err := manager.OpenWriter(id)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if err := manager.Running(id); err != nil {
		t.Fatal(err)
	}
	if err := manager.DiscardAbandonedMacStages(pending.Generation); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.WriteString("still-open descriptor"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(root, filepath.Dir(stage))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staging directory remained after abandonment: %v", err)
	}
	expectFile(t, path, "original")
	snapshots := manager.Snapshots()
	if len(snapshots) != 1 || snapshots[0].State != "interrupted" || snapshots[0].CleanupRequired {
		t.Fatalf("abandoned download state: %#v", snapshots)
	}
}

func TestMacDownloadShutdownRetriesFailedBrokerCleanup(t *testing.T) {
	root := t.TempDir()
	manager, err := OpenManager(filepath.Join(root, "ledger"), Policy{MaxBytes: 4096, MaxConcurrent: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	pending := Pending{
		ID: rand.Text(), Generation: "failed-browser", Owner: "tab", Profile: "profile",
		RequestID: "request", FrameID: "frame", Document: "document", Presentation: "visible",
		SourceOrigin: "https://fixture.invalid", SourceURL: "https://fixture.invalid/view",
		URL: "https://fixture.invalid/download.txt", Method: "GET", MIME: "text/plain", SuggestedName: "download.txt",
	}
	if err := manager.Offer(pending); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, pending.SuggestedName)
	id, err := manager.Approve(context.Background(), pending.ID, Selection{Path: path}, func(context.Context, Pending) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	stage := filepath.Join(root, filepath.Dir(manager.transfers[id].entry.File.Stage))
	blocker := filepath.Join(stage, "unrelated-file")
	if err := os.WriteFile(blocker, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}

	if err := manager.BrokerFailed(pending.Generation); !errors.Is(err, unix.ENOTEMPTY) {
		t.Fatalf("broker cleanup failure = %v, want ENOTEMPTY", err)
	}
	if err := manager.ReadyForShutdown(); !errors.Is(err, unix.ENOTEMPTY) {
		t.Fatalf("shutdown cleared a blocked cleanup: %v", err)
	}
	expectFile(t, blocker, "keep")
	snapshots := manager.Snapshots()
	if len(snapshots) != 1 || snapshots[0].State != "interrupted" || !snapshots[0].CleanupRequired {
		t.Fatalf("failed cleanup was not kept pending: %#v", snapshots)
	}

	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	if err := manager.ReadyForShutdown(); err != nil {
		t.Fatal("same manager could not retry the cleanup", err)
	}
	for _, name := range []string{stage, path} {
		if _, err := os.Lstat(name); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("unexpected path after cleanup: %s (%v)", name, err)
		}
	}
	if manager.Snapshots()[0].CleanupRequired {
		t.Fatal("shutdown left the recovered transaction marked as pending")
	}
}

func TestMacDownloadStartupKeepsPublishedCleanupPendingAcrossRestarts(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "result.txt")
	saveFixture(t, path, "previous")
	destination := makeDownload(t, path, true)
	ledgerInterrupted := errors.New("journal interrupted")
	var record FileRecord
	err := destination.Publish(func(file FileRecord) error {
		if file.Published {
			return ledgerInterrupted
		}
		record = file
		return nil
	})
	if !errors.Is(err, ledgerInterrupted) || record.Recovery == "" {
		t.Fatalf("could not establish recovered publication: %#v (%v)", record, err)
	}
	if err := destination.Close(); err != nil {
		t.Fatal(err)
	}
	stage := filepath.Join(root, filepath.Dir(record.Stage))
	blocker := filepath.Join(stage, "unrelated-file")
	saveFixture(t, blocker, "keep")
	recovered, err := RecoverFile(record)
	if err != nil || !recovered.Published || recovered.Recovery == "" {
		t.Fatalf("could not recover the publication with its original intact: %#v (%v)", recovered, err)
	}
	live := &transfer{entry: entry{State: "complete"}}
	live.recordCleanup(recovered, nil)
	if !live.entry.CleanupRequired {
		t.Fatal("an interrupted journal save could skip cleanup of the recovered original")
	}

	ledger := filepath.Join(root, "ledger")
	if err := os.Mkdir(ledger, 0700); err != nil {
		t.Fatal(err)
	}
	id := rand.Text()
	data, err := json.Marshal(entry{Version: 1, ID: id, State: "finalizing", File: record})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ledger, id+".json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	open := func() *Manager {
		t.Helper()
		m, err := OpenManager(ledger, Policy{MaxBytes: 4096, MaxConcurrent: 1}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	assertPending := func(m *Manager) {
		t.Helper()
		got := m.Snapshots()[0]
		if !got.CleanupRequired || got.State != "complete" || got.Error != "DOWNLOAD_RECOVERY_REQUIRED" {
			t.Fatalf("published cleanup was not kept pending: %#v", got)
		}
		if err := m.ReadyForShutdown(); !errors.Is(err, unix.ENOTEMPTY) {
			t.Fatalf("shutdown accepted pending cleanup: %v", err)
		}
		expectFile(t, path, "new content")
		expectFile(t, blocker, "keep")
	}
	assertPending(open())
	assertPending(open())
	// Earlier builds persisted the recovery error without the cleanup flag
	// once the old copy had been removed but its directory was still blocked.
	journal := filepath.Join(ledger, id+".json")
	data, err = os.ReadFile(journal)
	if err != nil {
		t.Fatal(err)
	}
	var legacy entry
	if err := json.Unmarshal(data, &legacy); err != nil {
		t.Fatal(err)
	}
	legacy.CleanupRequired = false
	data, err = json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(journal, data, 0600); err != nil {
		t.Fatal(err)
	}
	assertPending(open())
	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	reopened := open()
	got := reopened.Snapshots()[0]
	if got.CleanupRequired || got.State != "complete" || got.Error != "" {
		t.Fatalf("settled recovery retained a cleanup error: %#v", got)
	}
	if _, err := os.Lstat(stage); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("owned stage remains: %v", err)
	}
	expectFile(t, path, "new content")
}

func TestMacDownloadCancelDrainsWriterDuringLedgerFailure(t *testing.T) {
	root := t.TempDir()
	ledger := filepath.Join(root, "ledger")
	policy := Policy{MaxBytes: 4096, MaxConcurrent: 1}
	manager, err := OpenManager(ledger, policy, nil)
	if err != nil {
		t.Fatal(err)
	}
	pending := Pending{
		ID: rand.Text(), Generation: "browser", Owner: "tab", Profile: "profile",
		RequestID: "request", FrameID: "frame", Document: "document", Presentation: "visible",
		SourceOrigin: "https://fixture.invalid", SourceURL: "https://fixture.invalid/view",
		URL: "https://fixture.invalid/result.txt", Method: "GET", MIME: "text/plain",
	}
	if err := manager.Offer(pending); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "result.txt")
	id, err := manager.Approve(context.Background(), pending.ID, Selection{Path: path}, func(context.Context, Pending) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	stage := filepath.Join(root, filepath.Dir(manager.transfers[id].entry.File.Stage))
	var writer *os.File
	cancelCalls := 0
	if err := manager.Arm(id, func(ctx context.Context) error {
		cancelCalls++
		return errors.Join(ctx.Err(), writer.Close(), manager.Drained(id, "", 4))
	}); err != nil {
		t.Fatal(err)
	}
	writer, err = manager.OpenWriter(id)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if _, err := writer.WriteString("real"); err != nil {
		t.Fatal(err)
	}
	if err := manager.Running(id); err != nil {
		t.Fatal(err)
	}
	if err := manager.Progress(id, 4, 0); err != nil {
		t.Fatal(err)
	}
	offline := ledger + "-offline"
	if err := os.Rename(ledger, offline); err != nil {
		t.Fatal(err)
	}
	saveFixture(t, ledger, "not a directory")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := manager.Cancel(ctx, id, true); !errors.Is(err, unix.ENOTDIR) || errors.Is(err, context.Canceled) || cancelCalls != 1 {
		t.Fatalf("cancel did not drain the writer after the journal failure: calls=%d (%v)", cancelCalls, err)
	}
	if err := manager.ReadyForShutdown(); !errors.Is(err, unix.ENOTDIR) {
		t.Fatalf("shutdown accepted an undurable cancel: %v", err)
	}
	for _, name := range []string{stage, path} {
		if _, err := os.Lstat(name); !os.IsNotExist(err) {
			t.Fatalf("unexpected path after cancel: %s (%v)", name, err)
		}
	}
	if err := os.Remove(ledger); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(offline, ledger); err != nil {
		t.Fatal(err)
	}
	if err := manager.ReadyForShutdown(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenManager(ledger, policy, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.Snapshots()[0]; got.State != "revoked" || got.CleanupRequired {
		t.Fatalf("revoked download was not durable: %#v", got)
	}
	if _, _, err := reopened.StartData(id); !errors.Is(err, ErrApprovalExpired) {
		t.Fatalf("a revoked grant became active on restart: %v", err)
	}
}

func TestMacDownloadCompleteKeepsPublishedCleanupPending(t *testing.T) {
	root := t.TempDir()
	ledger := filepath.Join(root, "ledger")
	policy := Policy{MaxBytes: 4096, MaxConcurrent: 1}
	manager, err := OpenManager(ledger, policy, nil)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "result.txt")
	saveFixture(t, path, "previous")
	pending := Pending{
		ID: rand.Text(), Generation: "browser", Owner: "tab", Profile: "profile",
		RequestID: "request", FrameID: "frame", Document: "document", Presentation: "visible",
		SourceOrigin: "https://fixture.invalid", SourceURL: "https://fixture.invalid/view",
		URL: "https://fixture.invalid/result.txt", Method: "GET", MIME: "text/plain",
	}
	if err := manager.Offer(pending); err != nil {
		t.Fatal(err)
	}
	prepared, err := PrepareDestination(path, true)
	if err != nil {
		t.Fatal(err)
	}
	id, err := manager.Approve(context.Background(), pending.ID, Selection{Path: path, Prepared: prepared, OverwriteConfirmed: true}, func(context.Context, Pending) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Arm(id, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	writer, err := manager.OpenWriter(id)
	if err != nil {
		t.Fatal(err)
	}
	_, err = writer.WriteString("new content")
	if err := errors.Join(err, writer.Close()); err != nil {
		t.Fatal(err)
	}
	if err := manager.Running(id); err != nil {
		t.Fatal(err)
	}
	stage := filepath.Join(root, filepath.Dir(manager.transfers[id].entry.File.Stage))
	blocker := filepath.Join(stage, "unrelated-file")
	saveFixture(t, blocker, "keep")

	if err := manager.Complete(id, nil); !errors.Is(err, unix.ENOTEMPTY) {
		t.Fatalf("completion did not report the blocked stage: %v", err)
	}
	expectFile(t, path, "new content")
	if err := manager.ReadyForShutdown(); !errors.Is(err, unix.ENOTEMPTY) {
		t.Fatalf("shutdown accepted a published download with pending cleanup: %v", err)
	}
	if got := manager.Snapshots()[0]; got.State != "complete" || !got.CleanupRequired || got.Error != downloadRecoveryRequired {
		t.Fatalf("published cleanup was not retained: %#v", got)
	}

	legacy := manager.transfers[id].entry
	legacy.Error = "DOWNLOAD_PUBLISH_FAILED"
	legacy.CleanupRequired = false
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ledger, id+".json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenManager(ledger, policy, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.Snapshots()[0]; got.State != "complete" || !got.CleanupRequired {
		t.Fatalf("startup skipped cleanup of a legacy publication: %#v", got)
	}
	expectFile(t, blocker, "keep")
	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	if err := reopened.ReadyForShutdown(); err != nil {
		t.Fatal(err)
	}
	if got := reopened.Snapshots()[0]; got.State != "complete" || got.CleanupRequired || got.Error != "" {
		t.Fatalf("settled download retained a cleanup error: %#v", got)
	}
	if _, err := os.Lstat(stage); !os.IsNotExist(err) {
		t.Fatalf("owned stage remains: %v", err)
	}
	expectFile(t, path, "new content")
}
