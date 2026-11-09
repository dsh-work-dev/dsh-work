//go:build darwin && cgo && e67nativefixture

package macowner

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/sys/unix"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/embeddedbrowser"
)

// First production checkpoint: official bundle, real LaunchAgent/profile
// owner, broker's own Mach+HMAC socket and actual DevTools document/screenshot.
// It does not qualify UI presentation, managed admission, Service or MCP yet.
func TestLiveMacPrivateCEF(t *testing.T) {
	manifest, evidence := os.Getenv("DSH_WORK_CEF_MAC_PUBLICATION"), os.Getenv("DSH_WORK_CEF_TEST_EVIDENCE")
	if manifest == "" || evidence == "" {
		t.Fatal("explicit formal Mac publication and evidence directory required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pub, err := OpenPublication(ctx, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(evidence, 0700); err != nil {
		t.Fatal(err)
	}
	root, e := os.MkdirTemp("/tmp", "dsh-mac-formal-")
	if e != nil {
		t.Fatal(e)
	}
	complete := false
	t.Cleanup(func() {
		if complete {
			if e := os.RemoveAll(root); e != nil {
				t.Error(e)
			}
		} else {
			t.Log("private failed-generation recovery evidence retained at", root)
		}
	})
	profile := filepath.Join(root, "profile")
	ready := make(chan struct{}, 4)
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ready" {
			select {
			case ready <- struct{}{}:
			default:
			}
			w.WriteHeader(204)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><style>body{font:24px system-ui;background:#dff8ed;padding:48px}input{font:inherit}</style><h1>Formal Mac private CEF</h1><input value="unsubmitted"><script>window.fixtureNonce=crypto.randomUUID();window.fixtureDirty='unsubmitted';fetch('/ready');</script>`))
	}))
	defer fixture.Close()
	started := time.Now()
	timeline := []map[string]any{}
	mark := func(generation int, event string) {
		timeline = append(timeline, map[string]any{"generation": generation, "event": event, "at": time.Now().UTC(), "elapsedNanos": time.Since(started).Nanoseconds(), "profile": profile})
		data, err := json.MarshalIndent(timeline, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(evidence, "profile-timeline.json"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	for generation := 1; generation <= 2; generation++ {
		a, e := New(pub, profile)
		if e != nil {
			t.Fatal(e)
		}
		mark(generation, "start-requested")
		b, e := embeddedbrowser.Start(ctx, embeddedbrowser.Config{Executable: pub.Executable, Profile: profile, Supervisor: a})
		if e != nil {
			t.Fatal(e)
		}
		mark(generation, "runtime-ready-returned")
		if free, err := profileLeaseAvailable(profile); err != nil || free {
			t.Fatalf("active profile lease: free=%v err=%v", free, err)
		}
		mark(generation, "active-profile-lease-busy-observed")
		defer b.Close()
		selection, e := b.Select(ctx, automation.Request{Session: "formal-mac-private", Lifetime: "generation"})
		if e != nil {
			t.Fatal(e)
		}
		r, e := selection.Open(ctx, "Mac checkpoint")
		if e != nil {
			t.Fatal(e)
		}
		tab := 0
		for tab == 0 {
			select {
			case raw, ok := <-r.Messages():
				if !ok {
					t.Fatal("private route closed", r.Err())
				}
				var m struct {
					Method string
					Params []struct{ ID int }
				}
				if json.Unmarshal(raw, &m) == nil && m.Method == "tab.created" && len(m.Params) > 0 {
					tab = m.Params[0].ID
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		}
		serial := 0
		call := func(method string, params map[string]any) json.RawMessage {
			t.Helper()
			serial++
			data, _ := json.Marshal(map[string]any{"id": serial, "method": "devtools.dispatch", "params": []any{map[string]any{"tabId": tab, "sessionId": ""}, method, params}})
			if e = r.Send(ctx, data); e != nil {
				t.Fatal(e)
			}
			for {
				select {
				case raw, ok := <-r.Messages():
					if !ok {
						t.Fatal("private route closed", r.Err())
					}
					var m struct {
						ID     int
						Error  string
						Result json.RawMessage
					}
					if json.Unmarshal(raw, &m) != nil || m.ID != serial {
						continue
					}
					if m.Error != "" {
						t.Fatalf("%s: %s", method, m.Error)
					}
					return m.Result
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
		}
		call("Page.navigate", map[string]any{"url": fixture.URL})
		select {
		case <-ready:
		case <-ctx.Done():
			t.Fatal("fixture document did not execute", ctx.Err())
		}
		evaluate := func(expression string) string {
			raw := call("Runtime.evaluate", map[string]any{"expression": expression, "returnByValue": true})
			var result struct{ Result struct{ Value string } }
			if json.Unmarshal(raw, &result) != nil {
				t.Fatal("invalid Runtime result")
			}
			return result.Result.Value
		}
		nonce := ""
		for nonce == "" {
			nonce = evaluate("window.fixtureNonce || ''")
			if nonce == "" {
				select {
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-time.After(25 * time.Millisecond):
				}
			}
		}
		frame := call("Page.getFrameTree", map[string]any{})
		value := evaluate("window.fixtureDirty='private-edited';document.querySelector('input').value=window.fixtureDirty;window.fixtureNonce")
		if value != nonce {
			t.Fatal("private action redirected to another document")
		}
		after := call("Page.getFrameTree", map[string]any{})
		var beforeTree, afterTree struct {
			FrameTree struct{ Frame struct{ ID, LoaderID string } }
		}
		_ = json.Unmarshal(frame, &beforeTree)
		_ = json.Unmarshal(after, &afterTree)
		if beforeTree.FrameTree.Frame.ID == "" || beforeTree != afterTree {
			t.Fatal("same-document identity changed")
		}
		raw := call("Page.captureScreenshot", map[string]any{"format": "png", "fromSurface": true})
		var shot struct{ Data string }
		if json.Unmarshal(raw, &shot) != nil {
			t.Fatal("invalid screenshot reply")
		}
		pixels, e := base64.StdEncoding.DecodeString(shot.Data)
		if e != nil {
			t.Fatal(e)
		}
		image, e := png.Decode(bytes.NewReader(pixels))
		if e != nil || image.Bounds().Dx() < 100 || image.Bounds().Dy() < 100 {
			t.Fatal("real CPU OSR screenshot unavailable:", e)
		}
		red, green, blue, _ := image.At(10, 10).RGBA()
		if red>>8 < 0xd7 || red>>8 > 0xe7 || green>>8 < 0xf0 || blue>>8 < 0xe5 || blue>>8 > 0xf5 {
			t.Fatal("screenshot does not contain the actual fixture background")
		}
		name := filepath.Join(evidence, "generation-"+string(rune('0'+generation)))
		if e = os.WriteFile(name+".png", pixels, 0600); e != nil {
			t.Fatal(e)
		}
		a.worker.mu.Lock()
		runtimeVersion, ownerGeneration := a.worker.state.RuntimeVersion, a.worker.state.Generation
		a.worker.mu.Unlock()
		proof, _ := json.Marshal(map[string]any{"generation": generation, "profile": profile, "documentNonce": nonce, "frame": beforeTree, "dirty": "private-edited", "runtimeVersion": runtimeVersion, "broker": a.worker.Diagnostics()})
		if e = os.WriteFile(name+".json", proof, 0600); e != nil {
			t.Fatal(e)
		}
		// Always export an allowlisted terminal measurement, including when
		// shutdown fails. Neither the private journal nor audit targets escape.
		terminalWritten := false
		t.Cleanup(func() {
			if !terminalWritten {
				writeTerminalEvidence(t, name+"-terminal.json", a.worker)
			}
		})
		mark(generation, "normal-close-requested")
		if e = r.Close(); e != nil {
			t.Fatal(e)
		}
		if e = b.Close(); e != nil {
			t.Fatal(e)
		}
		mark(generation, "close-returned")
		if b.CleanupPending() {
			t.Fatal("owner incomplete after normal shutdown")
		}
		if e = a.worker.WaitEmpty(ctx); e != nil {
			t.Fatal(e)
		}
		mark(generation, "wait-empty-returned")
		terminalErr := writeTerminalEvidence(t, name+"-terminal.json", a.worker)
		terminalWritten = true
		if terminalErr != nil {
			t.Fatal(terminalErr)
		}
		if free, err := profileLeaseAvailable(profile); err != nil || !free {
			t.Fatalf("completed profile lease: free=%v err=%v", free, err)
		}
		mark(generation, "profile-lease-free-observed")
		if a.worker.ExitResult().Code != 0 {
			t.Fatal("CEF normal shutdown failed")
		}
		state, e := loadGeneration(a.directory, a.worker.key, ownerGeneration)
		if e != nil || state.Phase != "empty" {
			t.Fatal("generation terminal record missing:", e)
		}
		if len(state.Owned) < 2 || !strings.Contains(state.RuntimeVersion, "156.0.15") {
			t.Fatal("native inventory/runtime evidence incomplete")
		}
		t.Logf("generation %d: actual broker %d, %d owned instances, empty; same profile retained", generation, state.Broker.PID, len(state.Owned))
	}
	complete = true
}

// This query observes lock availability at fixture boundaries, not the exact
// internal release instant. The query closes its own fd on every path.
func profileLeaseAvailable(profile string) (bool, error) {
	fd, err := unix.Open(filepath.Join(profile, ".dsh-cef-profile.lock"), unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return false, err
	}
	defer unix.Close(fd)
	err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, unix.Flock(fd, unix.LOCK_UN)
}

func writeTerminalEvidence(t *testing.T, path string, w *Worker) error {
	t.Helper()
	// Explicit safe projection: Process has only kernel identity/path/state.
	// owned.Target, signed body/MAC, key, environment and launch config are omitted.
	type measured struct {
		Role       string  `json:"role"`
		Original   Process `json:"original"`
		Observed   Process `json:"observed"`
		QueryError string  `json:"queryError,omitempty"`
	}
	type groupMeasurement struct {
		PGID       uint32    `json:"pgid"`
		Members    []Process `json:"members"`
		QueryError string    `json:"queryError,omitempty"`
	}
	var proof struct {
		Generation                 string             `json:"generation"`
		ObservedAt                 time.Time          `json:"observedAt"`
		Phase                      string             `json:"authenticatedTerminalPhase"`
		RuntimeVersion             string             `json:"runtimeVersion"`
		ExitCode                   int                `json:"brokerExitCode"`
		ExitStarted                bool               `json:"brokerStarted"`
		ExitErrorPresent           bool               `json:"brokerExitErrorPresent"`
		Owner                      measured           `json:"owner"`
		Owned                      []measured         `json:"owned"`
		Groups                     []groupMeasurement `json:"groups"`
		Errors                     []string           `json:"measurementErrors"`
		ExactCefShutdownReturnAt   *time.Time         `json:"exactCefShutdownReturnAt"`
		ExactProfileLeaseReleaseAt *time.Time         `json:"exactProfileLeaseReleaseAt"`
	}
	proof.ObservedAt = time.Now().UTC()
	proof.Errors = []string{}
	proof.Owned = []measured{}
	proof.Groups = []groupMeasurement{}
	w.mu.Lock()
	id := w.state.Generation
	w.mu.Unlock()
	proof.Generation = id
	state, err := loadGeneration(w.directory, w.key, id)
	if err != nil {
		proof.Errors = append(proof.Errors, "terminal-record-unavailable")
	} else {
		proof.Phase = state.Phase
		proof.RuntimeVersion = state.RuntimeVersion
		proof.ExitCode = state.Exit.Code
		proof.ExitStarted = state.Exit.Started
		proof.ExitErrorPresent = state.Exit.Err != ""
		measure := func(role string, p Process) measured {
			current, e := instance(p)
			m := measured{Role: role, Original: p, Observed: current}
			if e != nil {
				m.QueryError = e.Error()
				proof.Errors = append(proof.Errors, "instance-query: "+e.Error())
			} else if current.State != "gone" && current.State != "reused" {
				proof.Errors = append(proof.Errors, "original-instance-not-gone")
			}
			return m
		}
		proof.Owner = measure("owner", state.Owner)
		groups := map[uint32]bool{state.Owner.PGID: true}
		keys := make([]string, 0, len(state.Owned))
		for key := range state.Owned {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			r := state.Owned[key]
			proof.Owned = append(proof.Owned, measure(r.Role, r.Process))
			groups[r.Process.PGID] = true
		}
		ids := make([]uint32, 0, len(groups))
		for id := range groups {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		for _, id := range ids {
			m := groupMeasurement{PGID: id, Members: []Process{}}
			if id == 0 {
				m.QueryError = "missing-group-identity"
			} else {
				census, e := group(id)
				m.Members = census.Members
				if e != nil {
					m.QueryError = e.Error()
				}
				for _, p := range census.Members {
					if p.State != "gone" {
						proof.Errors = append(proof.Errors, "group-not-empty")
					}
				}
			}
			if m.QueryError != "" {
				proof.Errors = append(proof.Errors, "group-query: "+m.QueryError)
			}
			proof.Groups = append(proof.Groups, m)
		}
		if state.Phase != "empty" {
			proof.Errors = append(proof.Errors, "terminal-phase-not-empty")
		}
	}
	data, e := json.MarshalIndent(proof, "", "  ")
	if e == nil {
		e = os.WriteFile(path, data, 0600)
	}
	if e != nil {
		t.Error(e)
		return e
	}
	if len(proof.Errors) != 0 {
		return errors.New("terminal evidence measurement failed")
	}
	return nil
}
