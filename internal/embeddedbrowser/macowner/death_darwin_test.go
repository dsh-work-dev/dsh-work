//go:build darwin && cgo && e67nativefixture

package macowner

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/embeddedbrowser"
	"github.com/local/dsh-work/internal/localipc"
)

type serviceDeathReady struct {
	Kind          string                          `json:"kind"`
	Stage         string                          `json:"stage"`
	CEFGeneration string                          `json:"cefGeneration"`
	MCPGeneration string                          `json:"mcpGeneration"`
	MCPResource   string                          `json:"mcpResource"`
	Document      automation.OwnedBrowserIdentity `json:"document"`
	Nonce         string                          `json:"nonce"`
	CEFGuard      Process                         `json:"cefGuard"`
	MCPGuard      Process                         `json:"mcpGuard"`
	CEFRoot       Process                         `json:"cefRoot"`
	Adapter       Process                         `json:"adapter"`
	Stdio         Process                         `json:"stdio"`
}

func connectServiceDeathController(t *testing.T, ctx context.Context) net.Conn {
	t.Helper()
	c, err := localipc.Dial(ctx, os.Getenv("DSH_WORK_MAC_DEATH_CONTROL"))
	if err != nil {
		t.Fatal(err)
	}
	peer, err := localipc.Peer(c)
	pid, _ := strconv.ParseUint(os.Getenv("DSH_WORK_MAC_DEATH_PARENT_PID"), 10, 32)
	started, _ := strconv.ParseUint(os.Getenv("DSH_WORK_MAC_DEATH_PARENT_STARTED"), 10, 64)
	if err != nil || peer.PID != uint32(pid) || peer.Started != started {
		c.Close()
		t.Fatal("death controller instance rejected", err)
	}
	return c
}
func holdServiceHostForDeath(t *testing.T, ctx context.Context, c net.Conn, native *Adapter, observed *observedMCP, document automation.OwnedBrowserIdentity, nonce string) {
	t.Helper()
	observed.mu.Lock()
	workers := append([]*Worker{}, observed.workers...)
	observed.mu.Unlock()
	if len(workers) != 1 {
		t.Fatal("death cut requires one actual MCP component")
	}
	native.worker.mu.Lock()
	cef := native.worker.state
	native.worker.mu.Unlock()
	workers[0].mu.Lock()
	mcpID := workers[0].state.Generation
	workers[0].mu.Unlock()
	mcp, err := loadGeneration(workers[0].directory, workers[0].key, mcpID)
	if err != nil {
		t.Fatal("MCP acknowledged inventory unavailable", err)
	}
	ready := serviceDeathReady{Kind: "ready", Stage: "same-document-mcp-complete-before-stop", CEFGeneration: cef.Generation, MCPGeneration: mcp.Generation, MCPResource: filepath.Dir(workers[0].directory), Document: document, Nonce: nonce, CEFGuard: cef.Owner, MCPGuard: mcp.Owner, CEFRoot: cef.Broker, Adapter: mcp.Broker}
	for _, owned := range mcp.Owned {
		if owned.Role == "mcp-stdio-child" {
			ready.Stdio = owned.Process
		}
	}
	if cef.Phase != "running" || mcp.Phase != "mcp-running" || mcp.StopRequested || ready.Stdio.PID == 0 || nonce == "" {
		t.Fatal("host not at active pre-Stop cut")
	}
	for _, p := range []Process{ready.CEFGuard, ready.MCPGuard, ready.CEFRoot, ready.Adapter, ready.Stdio} {
		current, err := instance(p)
		if err != nil || current.State != "live" {
			t.Fatal("death cut member not live", p.PID, err)
		}
	}
	if err := writeFrame(c, ready); err != nil {
		t.Fatal(err)
	}
	deadline, _ := ctx.Deadline()
	_ = c.SetReadDeadline(deadline)
	var message struct{ Kind string }
	if err := readFrame(c, &message); err != nil {
		t.Fatal("controller ended before intended SIGKILL", err)
	}
	t.Fatal("death controller must not request a graceful stop")
}

// The fixture host is its own current-user LaunchAgent. That gives the
// controller a safe early-failure retirement route before a Mach target exists.
// It does not turn this test entry point into production runDaemon or Wails.
type serviceHostJob struct {
	worker             *Worker
	submitted, retired bool
	process            Process
}

func newServiceHostJob(t *testing.T, ctx context.Context, pub Publication, root, evidence, control string, parent Process) *serviceHostJob {
	t.Helper()
	run, err := os.MkdirTemp(localipc.SocketTempRoot(), "dsh-cef-")
	if err != nil {
		t.Fatal(err)
	}
	var ownedJob *serviceHostJob
	t.Cleanup(func() {
		if ownedJob == nil || !ownedJob.submitted || ownedJob.retired {
			if e := os.Remove(filepath.Join(run, "owner.plist")); e != nil && !errors.Is(e, os.ErrNotExist) {
				t.Error(e)
			}
			if e := os.Remove(run); e != nil && !errors.Is(e, os.ErrNotExist) {
				t.Error(e)
			}
		}
	})
	run, err = filepath.EvalSymlinks(run)
	if err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		t.Fatal(err)
	}
	metadata := filepath.Join(root, "host-owner")
	if err = privateDir(metadata); err != nil {
		t.Fatal(err)
	}
	key, err := ownerKey(metadata)
	if err != nil {
		t.Fatal(err)
	}
	job := "dev.dsh.cef.fixture." + strings.ToLower(rand.Text())
	w := &Worker{directory: metadata, key: key, job: job, run: run, attempt: &launchAttempt{Generation: "cef-Host" + rand.Text(), Job: job, Run: run, Program: exe}}
	j := &serviceHostJob{worker: w}
	ownedJob = j
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if e := j.close(c); e != nil {
			t.Error("Service-host fixture job retirement", e)
		}
	})
	env := map[string]string{"PATH": os.Getenv("PATH"), "HOME": os.Getenv("HOME"), "USER": os.Getenv("USER"), "LOGNAME": os.Getenv("LOGNAME"), "TMPDIR": os.Getenv("TMPDIR"), "CI": os.Getenv("CI"), "DSH_WORK_CEF_MAC_PUBLICATION": os.Getenv("DSH_WORK_CEF_MAC_PUBLICATION"), "DSH_WORK_BROWSER_TEST_DEPENDENCIES": os.Getenv("DSH_WORK_BROWSER_TEST_DEPENDENCIES"), "DSH_WORK_CEF_TEST_EVIDENCE": evidence, "DSH_WORK_MAC_DEATH_ROOT": root, "DSH_WORK_MAC_DEATH_CONTROL": control, "DSH_WORK_MAC_DEATH_PARENT_PID": strconv.FormatUint(uint64(parent.PID), 10), "DSH_WORK_MAC_DEATH_PARENT_STARTED": strconv.FormatUint(parent.Started, 10)}
	var entries strings.Builder
	for key, value := range env {
		entries.WriteString("<key>" + escaped(key) + "</key><string>" + escaped(value) + "</string>")
	}
	plist := `<?xml version="1.0"?><plist version="1.0"><dict><key>Label</key><string>` + escaped(job) + `</string><key>Program</key><string>` + escaped(exe) + `</string><key>ProgramArguments</key><array><string>` + escaped(exe) + `</string><string>-test.run=^TestLiveMacEngineServiceMCP$</string><string>-test.count=1</string><string>-test.timeout=120s</string></array><key>EnvironmentVariables</key><dict>` + entries.String() + `</dict><key>RunAtLoad</key><true/><key>KeepAlive</key><false/><key>AbandonProcessGroup</key><false/><key>StandardOutPath</key><string>/dev/null</string><key>StandardErrorPath</key><string>/dev/null</string></dict></plist>`
	if err = writePrivate(filepath.Join(run, "owner.plist"), []byte(plist)); err != nil {
		t.Fatal(err)
	}
	if err = w.sealAttemptFiles(); err != nil {
		t.Fatal(err)
	}
	w.attempt.Submitted = true
	if err = w.saveAttempt(); err != nil {
		t.Fatal(err)
	}
	j.submitted = true
	if _, err = launchctl(ctx, "bootstrap", fmt.Sprintf("gui/%d", os.Geteuid()), filepath.Join(run, "owner.plist")); err != nil {
		t.Fatal("Service-host launch outcome retained", err)
	}
	return j
}
func (j *serviceHostJob) close(ctx context.Context) error {
	if j.retired {
		return nil
	}
	if j.submitted && !j.worker.jobRetired {
		status, err := j.worker.verifiedJob(ctx)
		if err != nil {
			return err
		}
		if text := jobField(status, "pid"); text != "" {
			pid, err := strconv.ParseUint(text, 10, 32)
			if err != nil {
				return err
			}
			j.process, err = process(uint32(pid))
			if err != nil {
				return err
			}
		}
		if _, err = launchctl(ctx, "bootout", service(j.worker.job)); err != nil {
			return err
		}
		j.worker.jobRetired = true
	}
	if j.process.PID != 0 {
		for {
			p, err := instance(j.process)
			if err != nil {
				return err
			}
			if p.State == "gone" || p.State == "reused" {
				break
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(25 * time.Millisecond):
			}
		}
	}
	if err := j.worker.removeRun(); err != nil {
		return err
	}
	if err := j.worker.clearAttempt(); err != nil {
		return err
	}
	j.retired = true
	return nil
}

func TestLiveMacServiceHostDeathAndProfileReopen(t *testing.T) {
	manifest, evidence := os.Getenv("DSH_WORK_CEF_MAC_PUBLICATION"), os.Getenv("DSH_WORK_CEF_TEST_EVIDENCE")
	if manifest == "" || evidence == "" || os.Getenv("DSH_WORK_BROWSER_TEST_DEPENDENCIES") == "" {
		t.Fatal("explicit formal Mac inputs required")
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
	root, err := os.MkdirTemp("/tmp", "dsh-mac-service-death-")
	if err != nil {
		t.Fatal(err)
	}
	complete := false
	t.Cleanup(func() {
		if complete {
			if err := os.RemoveAll(root); err != nil {
				t.Error(err)
			}
		} else {
			t.Log("private failed host-death recovery retained at", root)
		}
	})
	profile := filepath.Join(root, "profile")
	if err = privateDir(profile); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(profile, "fixture-profile-marker")
	if err = os.WriteFile(marker, []byte("same-undeleted-profile"), 0600); err != nil {
		t.Fatal(err)
	}
	profileBefore, err := os.Stat(profile)
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(root, "controller.sock")
	listener, err := localipc.Listen(socket, false)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	parent, err := process(uint32(os.Getpid()))
	if err != nil {
		t.Fatal(err)
	}
	host := newServiceHostJob(t, ctx, pub, root, evidence, socket, parent)
	accepted := make(chan net.Conn, 1)
	acceptErrors := make(chan error, 1)
	go func() {
		c, e := listener.Accept()
		if e != nil {
			acceptErrors <- e
		} else {
			accepted <- c
		}
	}()
	var control net.Conn
	select {
	case control = <-accepted:
	case err = <-acceptErrors:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	defer control.Close()
	target, err := localipc.TargetFromConn(control)
	if err != nil {
		t.Fatal(err)
	}
	status, err := host.worker.verifiedJob(ctx)
	if err != nil {
		t.Fatal(err)
	}
	jobPID, _ := strconv.ParseUint(jobField(status, "pid"), 10, 32)
	hostProcess, err := process(target.PID)
	if err != nil || target.PID != uint32(jobPID) || target.Started != hostProcess.Started || !sameJobFile(hostProcess.Path, host.worker.attempt.Program) {
		t.Fatal("actual Service-host instance rejected", err)
	}
	host.process = hostProcess
	deadline, _ := ctx.Deadline()
	_ = control.SetReadDeadline(deadline)
	var ready serviceDeathReady
	if err = readFrame(control, &ready); err != nil {
		t.Fatal("Service host did not reach real MCP cut", err)
	}
	if ready.Kind != "ready" || ready.Stage != "same-document-mcp-complete-before-stop" || ready.CEFGeneration == "" || ready.MCPGeneration == "" || "cef-"+ready.Document.BrokerGeneration != ready.CEFGeneration || ready.Document.ViewLease != "" || ready.Nonce == "" {
		t.Fatal("invalid ready cut")
	}
	timeline := []map[string]any{}
	mark := func(event string) {
		timeline = append(timeline, map[string]any{"event": event, "at": time.Now().UTC(), "host": hostProcess, "cefGeneration": ready.CEFGeneration, "mcpGeneration": ready.MCPGeneration, "profile": profile})
		data, _ := json.MarshalIndent(timeline, "", "  ")
		if err := os.WriteFile(filepath.Join(evidence, "service-host-death-timeline.json"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	data, _ := json.MarshalIndent(ready, "", "  ")
	if err = os.WriteFile(filepath.Join(evidence, "service-host-death-ready.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if free, e := profileLeaseAvailable(profile); e != nil || free {
		t.Fatal("active profile lock not held", free, e)
	}
	mark("ready-bound-active-profile-busy")
	// No Stop/Close message is sent. The kernel target is from this same live
	// authenticated connection, bound to the sealed current-user fixture job.
	if err = localipc.SignalTarget(target, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	mark("service-host-SIGKILL-sent")
	for {
		p, e := instance(hostProcess)
		if e != nil {
			t.Fatal(e)
		}
		if p.State == "gone" || p.State == "reused" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(25 * time.Millisecond):
		}
	}
	mark("service-host-original-instance-gone")
	killedStatus, e := host.worker.verifiedJob(ctx)
	if e != nil {
		t.Fatal("host job after original death", e)
	}
	signalProof, _ := json.MarshalIndent(map[string]any{"entry": "Go Service-host fixture LaunchAgent, not production runDaemon or Wails", "host": hostProcess, "injectedSignal": int(syscall.SIGKILL), "kernelAccepted": true, "originalInstanceGone": true, "lastExitCode": jobField(killedStatus, "last exit code"), "lastTerminatingSignal": jobField(killedStatus, "last terminating signal"), "launchdSignalFieldObserved": jobField(killedStatus, "last terminating signal") != "", "state": jobField(killedStatus, "state")}, "", "  ")
	if err = os.WriteFile(filepath.Join(evidence, "service-host-death-exit.json"), signalProof, 0600); err != nil {
		t.Fatal(err)
	}
	native, err := New(pub, profile)
	if err != nil {
		t.Fatal(err)
	}
	nativeKey, err := readPrivate(filepath.Join(native.directory, "journal.key"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = waitSpontaneousOwnerEmpty(ctx, native.directory, nativeKey, ready.CEFGeneration, ready.CEFGuard, ready.CEFRoot, filepath.Join(evidence, "death-cef-spontaneous-observations.json")); err != nil {
		t.Fatal("CEF did not spontaneously drain after host EOF", err)
	}
	mark("old-CEF-spontaneous-full-empty-observed-before-retirement")
	oldCEF, err := native.recoverOwnedAttempts(ctx, nativeKey)
	if err != nil {
		t.Fatal("CEF independent owner recovery", err)
	}
	if len(oldCEF) != 1 || oldCEF[0].state.Generation != ready.CEFGeneration || (!oldCEF[0].state.Exit.Started || oldCEF[0].state.Exit.Code != 0) {
		t.Fatal("CEF original generation/normal shutdown missing")
	}
	if err = writeTerminalEvidence(t, filepath.Join(evidence, "death-old-cef-terminal.json"), oldCEF[0]); err != nil {
		t.Fatal(err)
	}
	mark("old-CEF-full-empty-job-run-attempt-retired")
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	mcp, err := NewMCPAdapter(pub, node, os.Getenv("DSH_WORK_BROWSER_TEST_DEPENDENCIES"), filepath.Join(root, "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	mcpDirectory := filepath.Join(ready.MCPResource, ".dsh-mcp-owner")
	mcpKey, e := readPrivate(filepath.Join(mcpDirectory, "journal.key"))
	if e != nil {
		t.Fatal(e)
	}
	if _, err = waitSpontaneousOwnerEmpty(ctx, mcpDirectory, mcpKey, ready.MCPGeneration, ready.MCPGuard, ready.Adapter, filepath.Join(evidence, "death-mcp-spontaneous-observations.json")); err != nil {
		t.Fatal("MCP did not spontaneously drain after host EOF", err)
	}
	mark("old-MCP-spontaneous-full-empty-observed-before-retirement")
	oldMCP, err := mcp.recoverResource(ctx, ready.MCPResource)
	if err != nil {
		t.Fatal("MCP independent owner recovery", err)
	}
	if len(oldMCP) != 1 || oldMCP[0].state.Generation != ready.MCPGeneration {
		t.Fatal("MCP original generation missing")
	}
	if err = writeTerminalEvidence(t, filepath.Join(evidence, "death-old-mcp-terminal.json"), oldMCP[0]); err != nil {
		t.Fatal(err)
	}
	if err = mcp.RecoverResource(ctx, ready.MCPResource); err != nil {
		t.Fatal("public recovery not idempotent", err)
	}
	if err = host.close(ctx); err != nil {
		t.Fatal(err)
	}
	mark("old-MCP-full-empty-and-host-job-retired")
	if free, e := profileLeaseAvailable(profile); e != nil || !free {
		t.Fatal("fully retired profile unavailable", free, e)
	}
	profileAfter, err := os.Stat(profile)
	if err != nil || !os.SameFile(profileBefore, profileAfter) {
		t.Fatal("profile was replaced", err)
	}
	if body, e := os.ReadFile(marker); e != nil || string(body) != "same-undeleted-profile" {
		t.Fatal("profile data replaced", e)
	}
	mark("original-profile-free-observed-before-reopen")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<!doctype html><h1>Same profile after Service-host death</h1>"))
	}))
	defer server.Close()
	mark("same-profile-next-generation-start-requested")
	broker, err := embeddedbrowser.Start(ctx, embeddedbrowser.Config{Executable: pub.Executable, Profile: profile, Supervisor: native})
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	view, err := broker.OpenOwnedWebTarget(ctx, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := view.OwnedIdentity(ctx)
	if err != nil || identity.BrokerGeneration == ready.Document.BrokerGeneration || identity.URL != server.URL+"/" {
		t.Fatal("same-profile actual reopened document", identity, err)
	}
	if free, e := profileLeaseAvailable(profile); e != nil || free {
		t.Fatal("reopened profile not leased", free, e)
	}
	mark("same-profile-new-CEF-document-ready-and-leased")
	if err = view.CloseNative(ctx); err != nil {
		t.Fatal(err)
	}
	if err = broker.Close(); err != nil || broker.CleanupPending() {
		t.Fatal("reopened CEF normal cleanup", err)
	}
	if native.worker.ExitResult().Code != 0 {
		t.Fatal("reopened CEF did not exit normally")
	}
	if err = writeTerminalEvidence(t, filepath.Join(evidence, "death-reopened-cef-terminal.json"), native.worker); err != nil {
		t.Fatal(err)
	}
	mark("reopened-generation-normal-exit-full-retirement")
	if free, e := profileLeaseAvailable(profile); e != nil || !free {
		t.Fatal("reopened final profile still leased", free, e)
	}
	complete = true
}

// Observe the original authenticated generation without Stop/force or any
// recovery write. This separates the guards' own EOF response from retirement.
func waitSpontaneousOwnerEmpty(ctx context.Context, dir string, key []byte, generation string, expectedOwner, expectedRoot Process, path string) (journal, error) {
	type measured struct {
		Role            string `json:"role,omitempty"`
		PID             uint32 `json:"pid"`
		OriginalStarted uint64 `json:"originalStarted"`
		CurrentStarted  uint64 `json:"currentStarted"`
		PGID            uint32 `json:"pgid"`
		State           string `json:"state"`
		Error           string `json:"queryError,omitempty"`
	}
	type groupMeasure struct {
		PGID    uint32     `json:"pgid"`
		Members []measured `json:"members"`
		Error   string     `json:"queryError,omitempty"`
	}
	type sample struct {
		At              time.Time      `json:"at"`
		Phase           string         `json:"phase"`
		ExitStarted     bool           `json:"exitStarted"`
		ExitCode        int            `json:"exitCode"`
		ExitSignal      int            `json:"exitSignal"`
		Owner           measured       `json:"owner"`
		Owned           []measured     `json:"owned"`
		Groups          []groupMeasure `json:"groups"`
		Stages          []nativeEvent  `json:"nativeStages"`
		AllOriginalGone bool           `json:"allOriginalGone"`
		AllGroupsEmpty  bool           `json:"allGroupsEmpty"`
	}
	observations := struct {
		Generation string   `json:"generation"`
		Truncated  bool     `json:"truncated"`
		Samples    []sample `json:"samples"`
	}{Generation: generation, Samples: []sample{}}
	previous := ""
	for {
		state, err := loadGeneration(dir, key, generation)
		if err != nil {
			return state, errors.Join(ErrCleanupPending, err)
		}
		if state.Owner.PID != expectedOwner.PID || state.Owner.Started != expectedOwner.Started || state.Broker.PID != expectedRoot.PID || state.Broker.Started != expectedRoot.Started {
			return state, ErrCleanupPending
		}
		current := sample{Phase: state.Phase, ExitStarted: state.Exit.Started, ExitCode: state.Exit.Code, ExitSignal: state.ExitSignal, Owned: []measured{}, Groups: []groupMeasure{}, Stages: state.NativeEvents, AllOriginalGone: true, AllGroupsEmpty: true}
		measure := func(role string, original Process) measured {
			actual, e := instance(original)
			m := measured{Role: role, PID: original.PID, OriginalStarted: original.Started, CurrentStarted: actual.Started, PGID: actual.PGID, State: actual.State}
			if e != nil {
				m.Error = e.Error()
				current.AllOriginalGone = false
			} else if actual.State != "gone" && actual.State != "reused" {
				current.AllOriginalGone = false
			}
			return m
		}
		current.Owner = measure("owner", state.Owner)
		groups := map[uint32]bool{state.Owner.PGID: true}
		keys := []string{}
		for key := range state.Owned {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			owned := state.Owned[key]
			current.Owned = append(current.Owned, measure(owned.Role, owned.Process))
			groups[owned.Process.PGID] = true
		}
		ids := []uint32{}
		for id := range groups {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		for _, id := range ids {
			m := groupMeasure{PGID: id, Members: []measured{}}
			if id == 0 {
				m.Error = "missing-group-identity"
			} else {
				census, e := group(id)
				if e != nil {
					m.Error = e.Error()
				}
				for _, p := range census.Members {
					m.Members = append(m.Members, measured{PID: p.PID, OriginalStarted: p.Started, CurrentStarted: p.Started, PGID: p.PGID, State: p.State})
					if p.State != "gone" {
						current.AllGroupsEmpty = false
					}
				}
			}
			if m.Error != "" {
				current.AllGroupsEmpty = false
			}
			current.Groups = append(current.Groups, m)
		}
		signature, _ := json.Marshal(current)
		if string(signature) != previous {
			previous = string(signature)
			current.At = time.Now().UTC()
			if len(observations.Samples) < 128 {
				observations.Samples = append(observations.Samples, current)
			} else {
				observations.Truncated = true
				observations.Samples[len(observations.Samples)-1] = current
			}
			data, _ := json.MarshalIndent(observations, "", "  ")
			if err = os.WriteFile(path, data, 0600); err != nil {
				return state, err
			}
		}
		if state.Phase == "empty" && current.AllOriginalGone && current.AllGroupsEmpty {
			return state, nil
		}
		select {
		case <-ctx.Done():
			return state, errors.Join(ErrCleanupPending, ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
}
