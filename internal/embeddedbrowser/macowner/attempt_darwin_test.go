//go:build darwin && cgo

package macowner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestUnsubmittedAttemptCleansPrivateFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	run := filepath.Join(root, "run")
	if err := os.Mkdir(run, 0700); err != nil {
		t.Fatal(err)
	}
	w := &Worker{directory: root, key: make([]byte, 32), run: run, attempt: &launchAttempt{Generation: "cef-NotSubmitted", Run: run}}
	for _, name := range []string{"launch.json", "owner.plist"} {
		if err := writePrivate(filepath.Join(run, name), []byte("private-test-value")); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.closeAttempt(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(run); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unsubmitted files remain: %v", err)
	}
}

func TestCancelledUncertainAttemptRetainsRetryableOwner(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	run, err := os.MkdirTemp("/tmp", "dsh-cef-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(run)
	key := make([]byte, 32)
	attempt := &launchAttempt{Generation: "cef-Uncertain", Job: "dev.dsh.cef.test", Run: run, Program: "/owned/guard", Submitted: true}
	w := &Worker{directory: root, key: key, run: run, job: attempt.Job, attempt: attempt}
	if err := writePrivate(filepath.Join(run, "launch.json"), []byte("private-test-value")); err != nil {
		t.Fatal(err)
	}
	if err := w.saveAttempt(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// CommandContext cannot submit any command under an already cancelled ctx.
	// Its failure must not be mistaken for evidence that a prior bootstrap failed.
	if err := w.closeAttempt(ctx); !errors.Is(err, ErrCleanupPending) {
		t.Fatalf("uncertain attempt accepted: %v", err)
	}
	if _, err := readPrivate(filepath.Join(run, "launch.json")); err != nil {
		t.Fatal("lost attempt config", err)
	}
	a := &Adapter{directory: root, publication: Publication{Owner: attempt.Program}}
	if err := a.recoverAttempts(ctx, key); !errors.Is(err, ErrCleanupPending) {
		t.Fatalf("reopen lost uncertain job: %v", err)
	}
	if a.worker == nil || a.worker.attempt == nil || a.worker.attempt.Generation != attempt.Generation {
		t.Fatal("retry owner not recovered")
	}
	name, _ := attemptName(attempt.Generation)
	if _, err := readSigned(root, name, "cef-attempt-v1", key); err != nil {
		t.Fatal("lost durable owner", err)
	}
}
