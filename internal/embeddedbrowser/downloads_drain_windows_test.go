//go:build windows

package embeddedbrowser

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/embeddedbrowser/downloads"
	win "golang.org/x/sys/windows"
)

func drainFixture(t *testing.T) (*windowsDownloads, downloads.Pending, string) {
	t.Helper()
	root := t.TempDir()
	m, err := downloads.OpenManager(filepath.Join(root, "ledger"), downloads.Policy{MaxBytes: 1 << 20, MaxConcurrent: 2}, nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	close(done)
	b := &Broker{generation: "owned-generation", done: done, err: ErrClosed, targets: map[string][]*OwnedTarget{"owned-tab": {{}}}}
	c := &windowsDownloads{broker: b, manager: m, profile: root, prompts: map[string]downloadPrompt{}, active: map[string]*liveDownload{}}
	p := downloads.Pending{ID: rand.Text(), Generation: b.generation, Owner: "owned-tab", Profile: root, RequestID: "fetch-request", FrameID: "frame", Document: "doc", Presentation: "presentation", SourceOrigin: "https://download-fixture.invalid", SourceURL: "https://download-fixture.invalid/page", URL: "https://download-fixture.invalid/bytes", Method: "GET", MIME: "text/plain", SuggestedName: "owned.txt", TotalBytes: -1}
	return c, p, root
}

func TestDownloadShutdownCancelsPendingBeforeLateSaveAnswer(t *testing.T) {
	c, p, root := drainFixture(t)
	if err := c.manager.Offer(p); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.prompts[p.ID] = downloadPrompt{p.Owner, cancel}
	c.tasks.Add(1)
	answered := make(chan struct{})
	entered := make(chan struct{})
	c.consent = func(ctx context.Context, _ DownloadConsentRequest) (DownloadConsentDecision, error) {
		close(entered)
		<-ctx.Done()
		// Deliberately late Host answer: a real prepared temp capability must
		// still be discarded, and must not create an approved grant.
		d, err := downloads.PrepareDestination(filepath.Join(root, "late.txt"), false)
		close(answered)
		return DownloadConsentDecision{Prepared: d, RiskConfirmed: true}, err
	}
	go c.offer(ctx, cancel, p, downloads.DialogOwner{})
	<-entered
	resume, err := c.quiesce()
	if err != nil {
		t.Fatal(err)
	}
	defer resume()
	if err = c.drain(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	<-answered
	c.tasks.Wait()
	if len(c.manager.Snapshots()) != 0 {
		t.Fatal("late Save answer created a grant")
	}
	if _, ok := c.manager.Pending(p.ID); ok {
		t.Fatal("pending survived drain")
	}
	files, _ := filepath.Glob(filepath.Join(root, ".dsh-download-*"))
	if len(files) != 0 {
		t.Fatal("late Save left owned stage", files)
	}
	// The admission barrier also rejects an offer received after the old
	// prompts joined, rather than accepting one between the check and Close.
	p.ID = rand.Text()
	raw, _ := json.Marshal(map[string]any{"kind": "download.pending", "generation": p.Generation, "owner": p.Owner, "pending": p.ID, "requestID": "fetch-2", "frameID": p.FrameID, "document": p.Document, "source": p.SourceURL, "sourceOrigin": p.SourceOrigin, "url": p.URL, "name": "late.txt", "total": "-1", "parent": "1", "started": "1"})
	if err := c.message(raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.manager.Pending(p.ID); ok {
		t.Fatal("offer bypassed shutdown admission")
	}
	resume()
	if err := c.manager.Offer(p); err != nil {
		t.Fatal("pending contract damaged on resume", err)
	}
	c.manager.Dismiss(p.ID)
}

func TestDownloadShutdownCancellationWaitsForWriterAndTransactionDrain(t *testing.T) {
	c, p, root := drainFixture(t)
	if err := c.manager.Offer(p); err != nil {
		t.Fatal(err)
	}
	id, err := c.manager.Approve(context.Background(), p.ID, downloads.Selection{Path: filepath.Join(root, "cancel.txt")}, func(context.Context, downloads.Pending) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	cancelIssued := make(chan struct{}, 1)
	if err = c.manager.Arm(id, func(context.Context) error { cancelIssued <- struct{}{}; return nil }); err != nil {
		t.Fatal(err)
	}
	h, err := c.manager.DuplicateWriter(id, win.CurrentProcess())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if h != 0 {
			win.CloseHandle(win.Handle(h))
		}
	}()
	var written uint32
	if err = win.WriteFile(win.Handle(h), []byte("owned actual bytes"), &written, nil); err != nil {
		t.Fatal(err)
	}
	if err = c.manager.Running(id); err != nil {
		t.Fatal(err)
	}
	resume, err := c.quiesce()
	if err != nil {
		t.Fatal(err)
	}
	defer resume()
	ctx, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	result := make(chan error, 1)
	go func() { result <- c.drain(ctx, true) }()
	<-cancelIssued
	select {
	case err := <-result:
		t.Fatal("cancel dispatch was mistaken for drain", err)
	case <-time.After(60 * time.Millisecond):
	}
	if c.manager.Snapshots()[0].State != "stopping" {
		t.Fatal("writer ownership was released early")
	}
	// This is a Go ownership seam, not a synthetic native-drain claim. The
	// fixture itself owns and closes the real local writer before notifying it.
	if err = win.CloseHandle(win.Handle(h)); err != nil {
		t.Fatal(err)
	}
	h = 0
	if err = c.manager.Drained(id, "", int64(written)); err != nil {
		t.Fatal(err)
	}
	if err = <-result; err != nil {
		t.Fatal(err)
	}
	if c.manager.Snapshots()[0].State != "canceled" {
		t.Fatal("cancellation transaction incomplete")
	}
	files, _ := os.ReadDir(root)
	for _, f := range files {
		if f.Name() != "ledger" {
			t.Fatal("owned partial/final remains", f.Name())
		}
	}
}
