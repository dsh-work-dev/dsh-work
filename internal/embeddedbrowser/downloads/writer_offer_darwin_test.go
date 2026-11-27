//go:build darwin && cgo

package downloads

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/localipc"
	"golang.org/x/sys/unix"
)

type writerOfferFixture struct {
	Endpoint string
	Host     localipc.Process
}

func TestMacDownloadWriterOfferChild(t *testing.T) {
	if os.Getenv("DSH_WORK_TEST_DOWNLOAD_WRITER_CHILD") != "1" {
		return
	}
	var input writerOfferFixture
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	file, err := localipc.ReceiveWriteOnlyFile(ctx, input.Endpoint, input.Host)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	flags, err := unix.FcntlInt(file.Fd(), unix.F_GETFD, 0)
	if err != nil || flags&unix.FD_CLOEXEC == 0 {
		t.Fatalf("writer inherited by exec: flags=%d, %v", flags, err)
	}
	if _, err = file.Read(make([]byte, 1)); err == nil {
		t.Fatal("received writer is readable")
	}
	if _, err = file.WriteString("native peer wrote this"); err != nil {
		t.Fatal(err)
	}
}

func beginMacWriterOffer(t *testing.T) (*Manager, string, string) {
	t.Helper()
	root := t.TempDir()
	m, err := OpenManager(filepath.Join(root, "ledger"), Policy{MaxBytes: 1 << 20, MaxConcurrent: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	const host = "https://fixture.invalid"
	p := Pending{ID: rand.Text(), Generation: "g1", Owner: "tab1", Profile: "profile", RequestID: "r1", FrameID: "main", Document: "d1", Presentation: "visible", SourceOrigin: host, SourceURL: host + "/", URL: host + "/result.txt", Method: "GET", MIME: "text/plain", SuggestedName: "result.txt", TotalBytes: -1}
	if err = m.Offer(p); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "result.txt")
	id, err := m.Approve(context.Background(), p.ID, Selection{Path: path}, func(context.Context, Pending) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	if err = m.Arm(id, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	return m, id, path
}

func fixtureProcess(t *testing.T, pid uint32) localipc.Process {
	t.Helper()
	started, err := localipc.ProcessStarted(pid)
	if err != nil {
		t.Fatal(err)
	}
	return localipc.Process{PID: pid, Started: started}
}

func TestMacDownloadWriterOfferTransfersOnlyToPinnedProcess(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	m, id, path := beginMacWriterOffer(t)
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestMacDownloadWriterOfferChild$", "-test.v")
	child.Env = append(os.Environ(), "DSH_WORK_TEST_DOWNLOAD_WRITER_CHILD=1")
	var log bytes.Buffer
	child.Stdout, child.Stderr = &log, &log
	stdin, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if child.ProcessState == nil {
			_ = stdin.Close()
			_ = child.Process.Kill()
			_ = child.Wait()
		}
	}()
	offer, err := m.OfferWriter(ctx, id, fixtureProcess(t, uint32(child.Process.Pid)))
	if err != nil {
		t.Fatal(err)
	}
	defer offer.Close()
	// A successful same-user Mach handshake from the wrong process does not
	// deliver anything or consume the pending capability.
	wrong, err := localipc.Dial(ctx, offer.Endpoint)
	if err == nil {
		_ = wrong.SetReadDeadline(time.Now().Add(time.Second))
		n, readErr := wrong.Read(make([]byte, 1))
		_ = wrong.Close()
		if n != 0 || readErr == nil {
			t.Fatalf("wrong process received %d bytes, %v", n, readErr)
		}
	}
	if err = json.NewEncoder(stdin).Encode(writerOfferFixture{Endpoint: offer.Endpoint, Host: fixtureProcess(t, uint32(os.Getpid()))}); err != nil {
		t.Fatal(err)
	}
	_ = stdin.Close()
	if err = child.Wait(); err != nil {
		t.Fatalf("native receiver failed: %v\n%s", err, log.String())
	}
	if err = offer.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(offer.Endpoint); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("writer endpoint still exists: %v", err)
	}
	if err = m.Running(id); err != nil {
		t.Fatal(err)
	}
	if err = m.Complete(id, nil); err != nil {
		t.Fatal(err)
	}
	expectFile(t, path, "native peer wrote this")
}

func TestMacDownloadWriterOfferCancelJoinsBeforeStageCleanup(t *testing.T) {
	m, id, path := beginMacWriterOffer(t)
	offer, err := m.OfferWriter(context.Background(), id, fixtureProcess(t, uint32(os.Getpid())))
	if err != nil {
		t.Fatal(err)
	}
	if err = offer.Close(); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled offer = %v", err)
	}
	if _, err = os.Stat(offer.Endpoint); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("writer endpoint still exists: %v", err)
	}
	if err = m.Cancel(context.Background(), id, true); err != nil {
		t.Fatal(err)
	}
	if err = m.Drained(id, "", 0); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled writer published a file: %v", err)
	}
}
