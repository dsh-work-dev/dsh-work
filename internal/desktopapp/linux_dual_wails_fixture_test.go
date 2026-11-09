//go:build linux && cgo && e67presenter && e67nativefixture && !server

package desktopapp

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/daemon"
	"github.com/local/dsh-work/internal/embeddedbrowser"
	"github.com/local/dsh-work/internal/lifecycle"
	"github.com/local/dsh-work/internal/localipc"
	"github.com/local/dsh-work/internal/supervisor"
	"golang.org/x/sys/unix"
)

type linuxFixtureReport struct {
	Nonce, Input, Marker, StorageBefore, CookieBefore string
	Tick                                              int
	Dirty                                             bool
}

// This is an extension of the original Wails fixture, not a second host. Both
// real UI processes use the production candidate instance admission and daemon.
func exerciseLinuxDualLifecycle(t *testing.T, ctx context.Context, repo, root, out, node, native, observer, address string, environment map[string]string, client *daemon.Client, generation string, bUI supervisor.Worker, bSlot linuxUISlot, bGuest x11FixtureWindow, bBaseline linuxFixtureReport, readReports func(string) (linuxFixtureReport, map[string]linuxFixtureReport), start func(string, []string, map[string]string) supervisor.Worker) (supervisor.Worker, func()) {
	t.Helper()
	if bBaseline.Marker == "" || !bBaseline.Dirty || bBaseline.StorageBefore != "" || bBaseline.CookieBefore != "" {
		t.Fatal("B isolation baseline not established", bBaseline)
	}
	save := func(dir, name string, value any) {
		t.Helper()
		data, err := json.MarshalIndent(value, "", "  ")
		if err == nil {
			err = os.WriteFile(filepath.Join(dir, name+".json"), append(data, '\n'), 0600)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	wait := func(label string, check func() bool) {
		t.Helper()
		for !check() {
			select {
			case <-ctx.Done():
				t.Fatal(label, ctx.Err())
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
	signal := func(dir, name string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name+".request"), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	windows := func(w supervisor.Worker) []x11FixtureWindow {
		t.Helper()
		d := w.Diagnostics()
		data, err := exec.CommandContext(ctx, observer, strconv.FormatUint(uint64(d.PID), 10), strconv.FormatUint(d.ProcessStarted, 10), native).Output()
		if err != nil {
			t.Fatal(err)
		}
		var rows []x11FixtureWindow
		if err = json.Unmarshal(data, &rows); err != nil {
			t.Fatal(err)
		}
		return rows
	}
	guestOf := func(w supervisor.Worker, dir string) x11FixtureWindow {
		t.Helper()
		rows := windows(w)
		save(dir, "windows", rows)
		var guest x11FixtureWindow
		for _, g := range rows {
			if !g.Native || !g.Visible || g.Width < 100 || g.Height < 100 {
				continue
			}
			for _, p := range rows {
				if p.ID == g.Parent && p.PID == w.Diagnostics().PID && p.Started == w.Diagnostics().ProcessStarted {
					if guest.ID != 0 {
						t.Fatal("ambiguous native guests", rows)
					}
					guest = g
				}
			}
		}
		if guest.ID == 0 {
			t.Fatal("native guest not bound to this held UI parent", rows)
		}
		if output, err := exec.CommandContext(ctx, "import", "-window", strconv.FormatUint(uint64(guest.Parent), 10), filepath.Join(dir, "actual-ui.png")).CombinedOutput(); err != nil {
			t.Fatal("capture own verified UI parent", err, string(output))
		}
		return guest
	}
	type heldNative struct {
		fd    int
		group string
		guest x11FixtureWindow
	}
	hold := func(g x11FixtureWindow) heldNative {
		t.Helper()
		fd, err := unix.PidfdOpen(int(g.PID), 0)
		if err != nil {
			t.Fatal(err)
		}
		if birth, err := localipc.ProcessStarted(g.PID); err != nil || birth != g.Started {
			unix.Close(fd)
			t.Fatal("native instance changed", err)
		}
		data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cgroup", g.PID))
		if err != nil {
			unix.Close(fd)
			t.Fatal(err)
		}
		group := ""
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "0::/") {
				group = filepath.Join("/sys/fs/cgroup", strings.TrimPrefix(line, "0::"))
			}
		}
		if group == "" {
			unix.Close(fd)
			t.Fatal("native cgroup unavailable")
		}
		t.Cleanup(func() { unix.Close(fd) })
		return heldNative{fd, group, g}
	}
	joined := func(h heldNative) {
		t.Helper()
		wait("native pidfd and complete cgroup empty", func() bool {
			poll := []unix.PollFd{{Fd: int32(h.fd), Events: unix.POLLIN}}
			n, err := unix.Poll(poll, 0)
			data, e := os.ReadFile(filepath.Join(h.group, "cgroup.events"))
			return err == nil && n == 1 && (os.IsNotExist(e) || e == nil && strings.Contains(string(data), "populated 0"))
		})
	}
	verifyB := func(label string, previous linuxFixtureReport) linuxFixtureReport {
		t.Helper()
		boundary, _ := readReports(bBaseline.Nonce)
		var current linuxFixtureReport
		wait("B heartbeat after "+label, func() bool {
			current, _ = readReports(bBaseline.Nonce)
			return current.Tick > previous.Tick && current.Tick > boundary.Tick
		})
		if current.Nonce != bBaseline.Nonce || current.Input != bBaseline.Input || !current.Dirty || current.Marker != bBaseline.Marker {
			t.Fatal("A lifecycle changed B page", current)
		}
		select {
		case <-bUI.Exited():
			t.Fatal("B UI exited", bUI.ExitResult())
		default:
		}
		rows := windows(bUI)
		found := false
		for _, g := range rows {
			if g.ID == bGuest.ID && g.PID == bGuest.PID && g.Started == bGuest.Started && g.Parent == bGuest.Parent && g.Visible {
				found = true
			}
		}
		if !found {
			t.Fatal("B original guest lost", rows)
		}
		var state daemon.Snapshot
		if err := client.JSON(ctx, "/snapshot", nil, &state); err != nil || state.Status.GenerationID != generation || state.Status.State != lifecycle.StateReady {
			t.Fatal("daemon generation/state changed", state.Status, err)
		}
		save(out, "dual-b-"+label, current)
		return current
	}
	var selectedWorkspace struct{ Row string }
	selectedData, selectedErr := os.ReadFile(filepath.Join(out, "workspace-selected.json"))
	if selectedErr != nil || json.Unmarshal(selectedData, &selectedWorkspace) != nil || !strings.HasPrefix(selectedWorkspace.Row, "workspace:") {
		t.Fatal("B actual workspace readback unavailable", selectedErr)
	}
	var capturePeers []func()
	defer func() {
		for _, capture := range capturePeers {
			capture()
		}
	}()
	type peer struct {
		ui     supervisor.Worker
		dir    string
		joined chan struct{}
		err    error
		log    *os.File
	}
	aSession := ""
	launch := func(label, mode string) *peer {
		t.Helper()
		dir := filepath.Join(out, label)
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := listener.Addr().(*net.TCPAddr).Port
		listener.Close()
		env := map[string]string{}
		for k, v := range environment {
			env[k] = v
		}
		env["DSH_WORK_EMBEDDED_UI_INSTANCE"] = "2"
		env["WEBKIT_INSPECTOR_HTTP_SERVER"] = "127.0.0.1:" + strconv.Itoa(port)
		save(dir, "session", map[string]string{"key": aSession, "bKey": "session:" + strings.TrimSuffix(bSlot.Slot, "/"+filepath.Base(bSlot.Slot)), "workspace": selectedWorkspace.Row})
		w := start(label, nil, env)
		deferFailureCapture := func() {
			if !t.Failed() {
				return
			}
			captureCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			identity := w.Diagnostics()
			data, err := exec.CommandContext(captureCtx, observer, strconv.FormatUint(uint64(identity.PID), 10), strconv.FormatUint(identity.ProcessStarted, 10), native).Output()
			var rows []x11FixtureWindow
			if err == nil {
				err = json.Unmarshal(data, &rows)
			}
			var parent x11FixtureWindow
			for _, row := range rows {
				if row.PID == identity.PID && row.Started == identity.ProcessStarted && row.Visible && row.Width*row.Height > parent.Width*parent.Height {
					parent = row
				}
			}
			if err == nil && parent.ID != 0 {
				data, err = exec.CommandContext(captureCtx, "import", "-window", strconv.FormatUint(uint64(parent.ID), 10), filepath.Join(dir, "failure-ui.png")).CombinedOutput()
			} else if err == nil {
				err = fmt.Errorf("held UI has no mapped parent")
			}
			save(dir, "failure-capture", map[string]any{"pid": identity.PID, "started": identity.ProcessStarted, "parent": parent, "error": fmt.Sprint(err)})
		}
		// This helper defer runs before the outer fixture drains owned processes.
		capturePeers = append(capturePeers, deferFailureCapture)
		p := &peer{ui: w, dir: dir, joined: make(chan struct{})}
		p.log, err = os.Create(filepath.Join(dir, "driver.log"))
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(ctx, node, filepath.Join(repo, "scripts", "probe-linux-dsh-slot.mjs"), "http://127.0.0.1:"+strconv.Itoa(port), address, dir, mode)
		cmd.Dir = repo
		cmd.Stdout = p.log
		cmd.Stderr = p.log
		if err = cmd.Start(); err != nil {
			p.log.Close()
			t.Fatal(err)
		}
		go func() { p.err = cmd.Wait(); p.log.Close(); close(p.joined) }()
		t.Cleanup(func() {
			select {
			case <-p.joined:
			default:
				_ = cmd.Process.Kill()
				<-p.joined
			}
		})
		return p
	}
	read := func(p *peer, name string, dst any) {
		t.Helper()
		wait(name, func() bool {
			if data, err := os.ReadFile(filepath.Join(p.dir, "failure.json")); err == nil {
				t.Fatalf("first peer inspector failure: %s", data)
			}
			data, err := os.ReadFile(filepath.Join(p.dir, name+".json"))
			if err == nil && json.Unmarshal(data, dst) == nil {
				return true
			}
			select {
			case <-p.joined:
				t.Fatal("peer driver exited before", name, p.err)
			default:
			}
			select {
			case <-p.ui.Exited():
				t.Fatal("peer UI exited before", name, p.ui.ExitResult())
			default:
			}
			return false
		})
	}
	driverJoin := func(p *peer) {
		t.Helper()
		select {
		case <-p.joined:
			if p.err != nil {
				t.Fatal("peer inspector", p.err)
			}
		case <-ctx.Done():
			t.Fatal("peer driver join", ctx.Err())
		}
	}
	_, known := readReports(bBaseline.Nonce)
	a := launch("dual-a-initial", "--dual-peer")
	var aSlot linuxUISlot
	read(a, "visible", &aSlot)
	var aSelected struct {
		Key, Label string
		BlankLabel bool
	}
	read(a, "session-selected", &aSelected)
	aSession = "session:" + strings.TrimSuffix(aSlot.Slot, "/"+filepath.Base(aSlot.Slot))
	if aSelected.Key != aSession || !strings.HasPrefix(aSession, "session:session-") {
		t.Fatal("A slot/session selection mismatch", aSelected, aSlot)
	}
	if aSlot.State.Instance == "" || aSlot.State.Instance == bSlot.State.Instance || aSlot.State.BrokerGeneration == bSlot.State.BrokerGeneration || aSlot.State.Lease == bSlot.State.Lease {
		t.Fatal("instances/brokers/view leases not distinct", aSlot, bSlot)
	}
	var aAdmission, bAdmission embeddedbrowser.WindowAdmission
	read(a, "window-admission", &aAdmission)
	dataB, errB := os.ReadFile(filepath.Join(out, "window-admission.json"))
	if errB != nil || json.Unmarshal(dataB, &bAdmission) != nil || aAdmission.Lease == "" || aAdmission.Lease == bAdmission.Lease || aAdmission.Surface != "workspace-2" || bAdmission.Surface != "workspace" {
		t.Fatal("window admissions not independently bound", aAdmission, bAdmission, errB)
	}
	aGuest := guestOf(a.ui, a.dir)
	if aGuest.PID == bGuest.PID || aGuest.Parent == bGuest.Parent {
		t.Fatal("A and B share broker/parent", aGuest, bGuest)
	}
	aNative := hold(aGuest)
	var first linuxFixtureReport
	wait("A same-origin isolated page report", func() bool {
		_, all := readReports(bBaseline.Nonce)
		for nonce, r := range all {
			if _, old := known[nonce]; !old && r.Tick > 0 {
				if first.Nonce != "" && first.Nonce != nonce {
					t.Fatal("unexpected additional document", all)
				}
				first = r
			}
		}
		return first.Nonce != ""
	})
	if !first.Dirty || first.StorageBefore != "" || first.CookieBefore != "" || first.Marker == "" || first.Marker == bBaseline.Marker {
		t.Fatal("A profile not isolated from B at same origin", first, bBaseline)
	}
	// Read the trusted persistent store, never a webpage-supplied profile label.
	store := filepath.Join(root, "app", "embedded-browser", "instances")
	data, err := os.ReadFile(filepath.Join(store, "instances.json"))
	if err != nil {
		t.Fatal(err)
	}
	var catalog struct {
		Instances map[string]string `json:"instances"`
	}
	if err = json.Unmarshal(data, &catalog); err != nil {
		t.Fatal(err)
	}
	if catalog.Instances["candidate-2"] != aSlot.State.Instance || catalog.Instances["default"] != bSlot.State.Instance {
		t.Fatal("trusted catalog differs from real admitted instance", catalog)
	}
	profiles := map[string]string{}
	for _, id := range []string{aSlot.State.Instance, bSlot.State.Instance} {
		path := filepath.Join(store, id, "profile")
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			t.Fatal("persistent profile not present", path, err)
		}
		profiles[id] = path
	}
	save(out, "dual-scope", map[string]any{"a": map[string]any{"pid": a.ui.Diagnostics().PID, "started": a.ui.Diagnostics().ProcessStarted}, "b": map[string]any{"pid": bUI.Diagnostics().PID, "started": bUI.Diagnostics().ProcessStarted}, "aSlot": aSlot, "bSlot": bSlot, "aSessionSelection": aSelected, "aGuest": aGuest, "bGuest": bGuest, "profiles": profiles, "sameOrigin": address, "aWindowAdmission": aAdmission, "bWindowAdmission": bAdmission})
	save(a.dir, "document", first)
	fault := os.Getenv("DSH_WORK_LINUX_DUAL_FAULT") == "1"
	before := verifyB("before-a-exit", bBaseline)
	if fault {
		// Freeze the actual launched UI instance in a kernel pidfd, then disconnect
		// only its read-only inspector. No GUI close/detach/stop hook runs.
		identity := a.ui.Diagnostics()
		uiFD, e := unix.PidfdOpen(int(identity.PID), 0)
		if e != nil {
			t.Fatal(e)
		}
		defer unix.Close(uiFD)
		verifyHeldUI := func() {
			birth, e := localipc.ProcessStarted(identity.PID)
			actual, exeErr := os.Stat(fmt.Sprintf("/proc/%d/exe", identity.PID))
			expected, pathErr := os.Stat(os.Getenv("DSH_WORK_LINUX_WAILS_EXECUTABLE"))
			if e != nil || birth != identity.ProcessStarted || exeErr != nil || pathErr != nil || !os.SameFile(actual, expected) || unix.PidfdSendSignal(uiFD, 0, nil, 0) != nil {
				t.Fatal("held A UI no longer matches own launched instance", e, exeErr, pathErr)
			}
		}
		verifyHeldUI()
		signal(a.dir, "fault")
		read(a, "fault-armed", &map[string]any{})
		driverJoin(a)
		// Prove inspection disconnect did not itself close the original guest.
		disconnected, _ := readReports(first.Nonce)
		wait("A live after read-only inspector disconnect", func() bool {
			r, _ := readReports(first.Nonce)
			return r.Nonce == first.Nonce && r.Input == first.Input && r.Tick > disconnected.Tick
		})
		original := guestOf(a.ui, a.dir)
		if original.ID != aGuest.ID || original.PID != aGuest.PID || original.Started != aGuest.Started || original.Parent != aGuest.Parent {
			t.Fatal("A changed before root-only fault", original, aGuest)
		}
		verifyHeldUI()
		save(out, "dual-a-fault-injection", map[string]any{"pid": identity.PID, "started": identity.ProcessStarted, "signal": int(unix.SIGKILL), "kernelPidfd": true, "uiShutdownCalled": false, "nativeGuest": original})
		if e = unix.PidfdSendSignal(uiFD, unix.SIGKILL, nil, 0); e != nil {
			t.Fatal("signal held A UI only", e)
		}
	} else {
		signal(a.dir, "exit")
		read(a, "exit-requested", &map[string]any{})
	}
	select {
	case <-a.ui.Exited():
	case <-ctx.Done():
		t.Fatal("A exit deadline", ctx.Err())
	}
	expectedExit := 0
	if fault {
		expectedExit = 128 + int(unix.SIGKILL)
	}
	if r := a.ui.ExitResult(); r.Code != expectedExit || r.Err != "" {
		t.Fatal("A actual root exit differs from injected lifecycle", r)
	}
	if err = a.ui.WaitEmpty(ctx); err != nil {
		t.Fatal(err)
	}
	if err = a.ui.Close(); err != nil {
		t.Fatal(err)
	}
	joined(aNative)
	driverJoin(a)
	closedEvidence := "dual-a-normal-close"
	if fault {
		closedEvidence = "dual-a-fault-close"
	}
	save(out, closedEvidence, map[string]any{"faultInjected": fault, "ui": a.ui.ExitResult(), "uiTreeEmpty": true, "nativePidfdExited": true, "nativeCgroupEmpty": true})
	before = verifyB("after-a-exit", before)
	// Reopen exactly the same trusted logical instance/profile. The driver must
	// observe NeedsReload before the only explicit Reload DOM action.
	_, known = readReports(bBaseline.Nonce)
	reopened := launch("dual-a-reopened", "--dual-reopen")
	var needs linuxUISlot
	read(reopened, "needs-reload", &needs)
	if needs.Slot != aSlot.Slot || needs.State.Instance != aSlot.State.Instance || needs.State.Lease == aSlot.State.Lease || needs.State.Document != "" || needs.State.Target != "" {
		t.Fatal("reopen revived old live identity", needs, aSlot)
	}
	_, all := readReports(bBaseline.Nonce)
	if len(all) != len(known) {
		t.Fatal("A reopened document before explicit Reload", all)
	}
	beforeReload := windows(reopened.ui)
	save(reopened.dir, "needs-reload-windows", beforeReload)
	byID := map[uint32]x11FixtureWindow{}
	var parent x11FixtureWindow
	for _, w := range beforeReload {
		byID[w.ID] = w
		if w.PID == reopened.ui.Diagnostics().PID && w.Started == reopened.ui.Diagnostics().ProcessStarted && w.Visible && w.Width*w.Height > parent.Width*parent.Height {
			parent = w
		}
	}
	// The observer also returns child XIDs inside B's admitted CEF guest. Bind
	// its anchor to the held process instance, then follow actual XRes ancestry;
	// comparing only each child's direct parent misidentifies B as a new A page.
	bAnchor, ok := byID[bGuest.ID]
	if !ok || bAnchor.PID != bGuest.PID || bAnchor.Started != bGuest.Started || bAnchor.Parent != bGuest.Parent {
		t.Fatal("B guest anchor changed before A Reload", bAnchor)
	}
	inB := func(w x11FixtureWindow) bool {
		seen := map[uint32]bool{}
		for w.ID != 0 && !seen[w.ID] {
			if w.ID == bGuest.ID {
				return true
			}
			seen[w.ID] = true
			next, ok := byID[w.Parent]
			if !ok {
				return false
			}
			w = next
		}
		return false
	}
	for _, g := range beforeReload {
		if g.Native && g.Visible && !inB(g) {
			t.Fatal("unexpected native page outside held B subtree before Reload", g)
		}
	}
	if parent.ID == 0 {
		t.Fatal("reopened A has no verified mapped UI parent")
	}
	if output, e := exec.CommandContext(ctx, "import", "-window", strconv.FormatUint(uint64(parent.ID), 10), filepath.Join(reopened.dir, "needs-reload-ui.png")).CombinedOutput(); e != nil {
		t.Fatal("capture A descriptor before explicit restore", e, string(output))
	}
	signal(reopened.dir, "reload")
	var recovered linuxUISlot
	read(reopened, "visible", &recovered)
	var recoveredAdmission embeddedbrowser.WindowAdmission
	read(reopened, "window-admission", &recoveredAdmission)
	if recoveredAdmission.Lease == aAdmission.Lease || recoveredAdmission.Surface != aAdmission.Surface {
		t.Fatal("window admission revived across process", recoveredAdmission, aAdmission)
	}
	if recovered.Slot != aSlot.Slot || recovered.State.Instance != aSlot.State.Instance || recovered.State.Lease == needs.State.Lease || recovered.State.BrokerGeneration == aSlot.State.BrokerGeneration || recovered.State.Document == "" {
		t.Fatal("recovered identity not fresh", recovered)
	}
	recoveredGuest := guestOf(reopened.ui, reopened.dir)
	if recoveredGuest.PID == aGuest.PID && recoveredGuest.Started == aGuest.Started {
		t.Fatal("native process instance revived", recoveredGuest)
	}
	recoveredNative := hold(recoveredGuest)
	var recoveredReport linuxFixtureReport
	wait("A persistent profile restored after explicit Reload", func() bool {
		_, all := readReports(bBaseline.Nonce)
		for nonce, r := range all {
			if _, old := known[nonce]; !old {
				recoveredReport = r
				return r.Tick > 0
			}
		}
		return false
	})
	if recoveredReport.Marker != first.Marker || recoveredReport.StorageBefore != first.Marker || !strings.Contains(recoveredReport.CookieBefore, "e67="+first.Marker) || recoveredReport.Nonce == first.Nonce || recoveredReport.Input == first.Input || !recoveredReport.Dirty {
		t.Fatal("A persistent storage/clean document recovery failed", first, recoveredReport)
	}
	save(reopened.dir, "document", recoveredReport)
	verifyB("after-a-reopen", before)
	signal(reopened.dir, "finish")
	driverJoin(reopened)
	signal(out, "dual-complete")
	return reopened.ui, func() {
		joined(recoveredNative)
		save(out, "dual-final-native-cleanup", map[string]any{"pid": recoveredGuest.PID, "started": recoveredGuest.Started, "nativePidfdExited": true, "nativeCgroupEmpty": true})
	}
}
