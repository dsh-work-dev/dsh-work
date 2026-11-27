//go:build windows

package embeddedbrowser_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/embeddedbrowser"
	"github.com/local/dsh-work/internal/embeddedbrowser/downloads"
	platform "github.com/local/dsh-work/internal/platform/windows"
)

func TestApprovedAttachmentURLRequestNative(t *testing.T) { runApprovedAttachmentNative(t, "direct") }
func TestApprovedDownloadRedirectAndBackpressureNative(t *testing.T) {
	for _, mode := range []string{"same-origin", "cross-origin", "cancel-redirect", "backpressure"} {
		t.Run(mode, func(t *testing.T) { runApprovedAttachmentNative(t, mode) })
	}
}
func runApprovedAttachmentNative(t *testing.T, mode string) {
	executable := os.Getenv("DSH_WORK_CEF_TEST_EXECUTABLE")
	if executable == "" {
		t.Skip("requires independent CEF156 candidate")
	}
	ctx, stop := context.WithTimeout(context.Background(), 75*time.Second)
	defer stop()
	root := t.TempDir()
	destination := filepath.Join(root, "e72-native.txt")
	payload := bytes.Repeat([]byte("E72 owned localhost download\n"), 4096)
	var auditMu sync.Mutex
	var cookies []string
	var requestCount int
	redirectReady := make(chan struct{}, 1)
	redirectRelease := make(chan struct{})
	var releaseOnce sync.Once
	releaseRedirect := func() { releaseOnce.Do(func() { close(redirectRelease) }) }
	defer releaseRedirect()
	var crossRequests atomic.Int32
	cross := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		crossRequests.Add(1)
		fmt.Fprint(w, "must not receive an unapproved cross-origin GET")
	}))
	defer cross.Close()
	if mode == "backpressure" {
		payload = bytes.Repeat([]byte("owned backpressure bytes\n"), 65536)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bytes" || r.URL.Path == "/redirected" {
			auditMu.Lock()
			cookies = append(cookies, r.Header.Get("Cookie"))
			requestCount++
			number := requestCount
			auditMu.Unlock()
			if number == 2 && mode != "direct" && mode != "backpressure" {
				if mode == "cancel-redirect" {
					redirectReady <- struct{}{}
					<-redirectRelease
				}
				location := "/redirected"
				if mode == "cross-origin" {
					location = cross.URL + "/must-not-start"
				}
				w.Header().Set("Location", location)
				w.WriteHeader(302)
				return
			}
			w.Header().Set("Content-Type", "text/plain")
			w.Header().Set("Content-Disposition", `attachment; filename="e72-native.txt"`)
			w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
			w.WriteHeader(200)
			for offset := 0; offset < len(payload); offset += 4096 {
				end := offset + 4096
				if end > len(payload) {
					end = len(payload)
				}
				if _, e := w.Write(payload[offset:end]); e != nil {
					return
				}
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
				time.Sleep(15 * time.Millisecond)
			}
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "E72Profile", Value: "owned", HttpOnly: true, Path: "/"})
		fmt.Fprint(w, `<!doctype html><title>E72 owned download</title><a id="download" href="/bytes">Download owned fixture</a><script>setTimeout(()=>document.getElementById('download').click(),2200)</script>`)
	}))
	defer func() { releaseRedirect(); server.Close() }()
	parent, peer, closeParent := policyParent(t)
	defer closeParent()
	approved := make(chan embeddedbrowser.DownloadConsentRequest, 1)
	adapter := &policyAdapter{Adapter: platform.NewJobObjectAdapter()}
	broker, e := embeddedbrowser.Start(ctx, embeddedbrowser.Config{
		Executable: executable, Profile: filepath.Join(root, "profile"), Supervisor: adapter,
		DownloadConsent: func(ctx context.Context, p embeddedbrowser.DownloadConsentRequest) (embeddedbrowser.DownloadConsentDecision, error) {
			// A trusted test-Host decision over its own temp capability, not a forged
			// website approval or a claim that native Save UI has been exercised.
			select {
			case approved <- p:
			default:
			}
			d, e := downloads.PrepareDestination(destination, false)
			return embeddedbrowser.DownloadConsentDecision{Prepared: d, RiskConfirmed: true}, e
		},
	})
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if e := broker.Close(); e != nil {
			t.Errorf("native close: %v", e)
		}
		if broker.CleanupPending() {
			t.Error("native/profile cleanup pending")
		}
	}()
	registry := embeddedbrowser.NewPresenterRegistry(broker)
	defer registry.Close()
	admission, e := registry.RegisterWindow(ctx, peer, "window", parent)
	if e != nil {
		t.Fatal(e)
	}
	view, e := registry.Update(ctx, peer, "window", parent, admission.Lease, "download/fixture", server.URL, embeddedbrowser.SlotBounds{Width: 700, Height: 450}, 1, true)
	if e != nil {
		t.Fatal(e)
	}
	t.Logf("initial managed view=%s visible=%t", view.State, view.Visible)
	select {
	case p := <-approved:
		if p.SourceOrigin != server.URL {
			t.Fatalf("source origin %q", p.SourceOrigin)
		}
	case <-ctx.Done():
		t.Fatalf("trusted native pending was not emitted: %v", ctx.Err())
	}
	var final embeddedbrowser.DownloadView
	if mode == "cancel-redirect" {
		select {
		case <-redirectReady:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if mode == "backpressure" || mode == "cancel-redirect" {
		for {
			states := broker.DownloadState()
			if len(states) > 0 && (states[0].Received > 0 || mode == "cancel-redirect") {
				final = states[0]
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(10 * time.Millisecond):
			}
		}
		if mode == "backpressure" {
			if e := embeddedbrowser.NativeBackpressureCancelForTest(ctx, broker, final.ID); e != nil {
				t.Fatal(e)
			}
		} else {
			if e := broker.CancelDownload(ctx, final.ID, false); e != nil {
				t.Fatal(e)
			}
			releaseRedirect()
		}
	}
	for {
		states := broker.DownloadState()
		if len(states) > 0 {
			final = states[0]
			if final.State == "complete" || ((mode == "backpressure" || mode == "cancel-redirect") && final.State == "canceled") || (mode == "cross-origin" && final.State == "failed") {
				break
			}
			if final.State == "failed" || final.State == "interrupted" || final.State == "canceled" || final.State == "revoked" {
				_, brokerFailure := broker.Select(context.Background(), automation.Request{Session: "owned-fixture", Lifetime: "owned-fixture"})
				t.Logf("broker terminal cause: %v", brokerFailure)
				state, stateError := registry.State(ctx, peer, "window", "download/fixture")
				t.Logf("native diagnostic=%#v view=%#v viewError=%v", adapter.worker.Diagnostics(), state, stateError)
				t.Fatalf("native transaction failed: %#v", final)
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("completion: %v state=%#v", ctx.Err(), states)
		case <-time.After(40 * time.Millisecond):
		}
	}
	if mode == "backpressure" || mode == "cancel-redirect" || mode == "cross-origin" {
		if _, e := os.Stat(destination); !os.IsNotExist(e) {
			t.Fatalf("unexpected final destination: %v", e)
		}
		leftovers, _ := filepath.Glob(filepath.Join(root, ".dsh-download-*"))
		if len(leftovers) != 0 {
			t.Fatalf("owned residual: %v", leftovers)
		}
		auditMu.Lock()
		count := requestCount
		auditMu.Unlock()
		if count != 2 || crossRequests.Load() != 0 {
			t.Fatalf("unexpected follow GET: count=%d cross=%d", count, crossRequests.Load())
		}
		if mode == "cross-origin" && final.Error != "DOWNLOAD_REDIRECT_NOT_APPROVED" {
			t.Fatalf("cross-origin failure: %#v", final)
		}
		t.Logf("%s: state=%s received=%d native writer closed/drained; exact owned partial removed, no subsequent GET", mode, final.State, final.Received)
		return
	}
	got, e := os.ReadFile(destination)
	if e != nil || !bytes.Equal(got, payload) {
		t.Fatalf("download contents size=%d want=%d error=%v", len(got), len(payload), e)
	}
	auditMu.Lock()
	observed := append([]string(nil), cookies...)
	auditMu.Unlock()
	wantRequests := 2
	if mode == "same-origin" {
		wantRequests = 3
	}
	if len(observed) != wantRequests {
		t.Fatalf("expected original paused GET + approved new GET, got %d", len(observed))
	}
	if observed[1] != "E72Profile=owned" {
		t.Fatalf("approved request did not use original profile cookie: %q", observed[1])
	}
	if final.Received != int64(len(payload)) {
		t.Fatalf("actual written progress=%d want=%d", final.Received, len(payload))
	}
	t.Logf("native attachment pending -> trusted Host temp capability -> dedicated CefURLRequest: %d exact bytes, profile cookie, native writer close/drain and final publication; localhost source AES result not claimed internet MOTW", len(got))
}
