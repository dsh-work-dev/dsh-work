//go:build darwin && cgo

package macowner

import (
	"context"
	"encoding/json"
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

func TestJobOwnershipUsesFileIdentity(t *testing.T) {
	root := t.TempDir()
	expected := filepath.Join(root, "owner.plist")
	foreign := filepath.Join(root, "foreign.plist")
	for _, path := range []string{expected, foreign} {
		if err := os.WriteFile(path, []byte("same contents"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	alias := filepath.Join(root, "alias.plist")
	if err := os.Symlink(expected, alias); err != nil {
		t.Fatal(err)
	}
	dev, ino, err := fileIdentity(expected)
	if err != nil {
		t.Fatal(err)
	}
	if !pinnedJobFile(alias, expected, dev, ino) || pinnedJobFile(alias, expected, dev, ino+1) {
		t.Fatal("submitted file instance binding lost")
	}
	if !sameJobFile(alias, expected) {
		t.Fatal("canonical alias to exact submitted file rejected")
	}
	if sameJobFile(foreign, expected) {
		t.Fatal("foreign same-content file accepted")
	}
	if sameJobFile(filepath.Join(root, "missing"), expected) || sameJobFile(root, root) || sameJobFile("owner.plist", expected) {
		t.Fatal("missing, directory or relative path accepted")
	}
	held := filepath.Join(root, "original.plist")
	if err := os.Link(expected, held); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(foreign, expected); err != nil {
		t.Fatal(err)
	}
	if pinnedJobFile(alias, expected, dev, ino) {
		t.Fatal("same-path replaced plist accepted")
	}
	run, err := os.MkdirTemp("/tmp", "dsh-cef-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(run)
	canonical, err := filepath.EvalSymlinks(run)
	if err != nil {
		t.Fatal(err)
	}
	if !ownedRunParent(run) || !ownedRunParent(canonical) {
		t.Fatal("original/canonical run root rejected")
	}
	if ownedRunParent(filepath.Join(root, "dsh-cef-foreign")) {
		t.Fatal("foreign run parent accepted")
	}
	for _, name := range []string{"launch.json", "owner.plist", "owner.sock.lock"} {
		if err := os.WriteFile(filepath.Join(canonical, name), []byte("owned"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	worker := &Worker{run: canonical}
	if err := worker.removeRun(); err != nil {
		t.Fatal("canonical run retirement failed", err)
	}
	if !worker.runRetired {
		t.Fatal("run retirement milestone missing")
	}
	if _, err := os.Stat(run); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("canonical run files remain", err)
	}
	if _, err := os.Stat(held); err != nil {
		t.Fatal("unrelated fixture file removed", err)
	}
}

func TestLegacyAttemptUpgradesBeforeRetirement(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	run := filepath.Join(root, "run")
	if err := os.Mkdir(run, 0700); err != nil {
		t.Fatal(err)
	}
	program := filepath.Join(root, "guard")
	plist := filepath.Join(run, "owner.plist")
	for _, path := range []string{program, plist} {
		if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	key := make([]byte, 32)
	old := &Worker{directory: root, key: key, run: run, attempt: &launchAttempt{Generation: "cef-Legacy", Job: "dev.dsh.cef.legacy", Run: run, Program: program, Submitted: true}}
	if err := old.saveAttempt(); err != nil {
		t.Fatal(err)
	}
	name, _ := attemptName(old.attempt.Generation)
	reopen := func() *Worker {
		t.Helper()
		body, err := readSigned(root, name, "cef-attempt-v1", key)
		if err != nil {
			t.Fatal(err)
		}
		var attempt launchAttempt
		if err = json.Unmarshal(body, &attempt); err != nil {
			t.Fatal(err)
		}
		return &Worker{directory: root, key: key, run: attempt.Run, job: attempt.Job, attempt: &attempt}
	}
	w := reopen()
	if w.attempt.FileIdentityVersion != 0 || w.attempt.PlistInode != 0 {
		t.Fatal("fixture is not an old record")
	}
	foreign := filepath.Join(root, "foreign.plist")
	if err := os.WriteFile(foreign, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	bad := []byte("path = " + foreign + "\nprogram = " + program + "\n")
	if _, err := w.verifyJobMetadata(bad); !errors.Is(err, ErrCleanupPending) {
		t.Fatal("legacy foreign job accepted", err)
	}
	if w.attempt.FileIdentityVersion != 0 {
		t.Fatal("failed validation upgraded record")
	}
	alias := filepath.Join(run, "alias.plist")
	if err := os.Symlink(plist, alias); err != nil {
		t.Fatal(err)
	}
	valid := []byte("path = " + alias + "\nprogram = " + program + "\nstate = not running\n")
	measure, err := w.verifyJobMetadata(valid)
	if err != nil || measure.Status != "present-owned" {
		t.Fatal("authenticated old record lost original contract", measure, err)
	}
	w = reopen()
	if w.attempt.FileIdentityVersion != 1 || w.attempt.PlistInode == 0 || w.attempt.ProgramInode == 0 {
		t.Fatal("upgrade not durable")
	}
	if _, err = w.verifyJobMetadata(valid); err != nil {
		t.Fatal("upgraded record did not reopen", err)
	}
	held := filepath.Join(run, "held-original.plist")
	if err = os.Link(plist, held); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(foreign, plist); err != nil {
		t.Fatal(err)
	}
	if _, err = w.verifyJobMetadata(valid); !errors.Is(err, ErrCleanupPending) {
		t.Fatal("upgraded record fell back after replacement", err)
	}
}
