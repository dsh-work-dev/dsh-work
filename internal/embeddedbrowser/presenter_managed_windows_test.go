//go:build windows

package embeddedbrowser

import (
	"context"
	"errors"
	"github.com/local/dsh-work/internal/automation"
	win "github.com/local/dsh-work/internal/platform/windows"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLiveManagedTargetRequiresVerifiedPresent(t *testing.T) {
	exe := os.Getenv("DSH_WORK_CEF_TEST_EXECUTABLE")
	if exe == "" {
		t.Skip("requires actual CEF")
	}
	ctx, stop := context.WithTimeout(context.Background(), 20*time.Second)
	defer stop()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<input value="unchanged">`))
	}))
	defer server.Close()
	b, err := Start(ctx, Config{Executable: exe, Profile: filepath.Join(t.TempDir(), "profile"), Supervisor: win.NewJobObjectAdapter()})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	target, err := b.openManagedWebTarget(ctx, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := target.OwnedIdentity(ctx)
	if err != nil || identity.ViewVisible || identity.ViewLease != "" {
		t.Fatal("managed target usable before Present", identity, err)
	}
	if _, err = target.BeginOwnedCall(ctx, automation.OwnedBrowserCall{Identity: identity, Token: "before-present"}); err == nil {
		t.Fatal("managed target admitted before Present")
	}
	if _, err = b.controlCall(ctx, "target.hide", target.owner, map[string]any{"epoch": "1"}); err != nil {
		t.Fatal(err)
	}
	hidden, err := target.OwnedIdentity(ctx)
	if err != nil || hidden.ViewVisible {
		t.Fatal("pre-Present Hide ignored", hidden, err)
	}
	if _, err = b.controlCall(ctx, "target.present", target.owner, map[string]any{"parent": "0", "pid": 0, "started": "0", "view": "must-not-commit", "epoch": "2", "bounds": PixelBounds{X: 0, Y: 40, Width: 500, Height: 300}, "visible": true}); err == nil || !strings.Contains(err.Error(), "CEF_PRESENT_") {
		t.Fatal("invalid native presentation acknowledged", err)
	}
	failed, err := target.OwnedIdentity(ctx)
	if err != nil || failed.ViewLease != "" || failed.PresentationEpoch != "" || failed.ViewVisible || failed.Document != identity.Document {
		t.Fatal("failed Present committed identity or destroyed document", failed, err)
	}
	if err = target.CloseNative(ctx); err != nil {
		t.Fatal("retained owner could not finish close barrier", err)
	}
	if _, err = target.OwnedIdentity(ctx); !errors.Is(err, ErrClosed) {
		t.Fatal("native close/frame barrier not observed", err)
	}
	t.Log("actual CEF managed target stayed hidden/uncontrollable; pre-Present Hide effective; rejected Present preserved identity/document and retryable close owner")
}
