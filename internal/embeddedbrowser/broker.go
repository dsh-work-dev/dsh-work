// Package embeddedbrowser owns the private CEF runtime boundary. It is not
// enabled by settings or exported as a UI/Worker API until M0 admission passes.
package embeddedbrowser

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/automation/browsernative"
	"github.com/local/dsh-work/internal/automation/browserruntime"
	"github.com/local/dsh-work/internal/supervisor"
	"github.com/local/dsh-work/internal/workeripc"
)

var ErrClosed = errors.New("CEF_BROKER_CLOSED")
var ErrControlPaused = errors.New("CEF_BROKER_CONTROL_PAUSED")

// RendererTermination identifies the reporting callback, not the complete
// affected set or an OS renderer process. Engine error text is never retained.
type RendererTermination struct {
	ReportedBrowser   int    `json:"reportedBrowser"`
	ReportedTab       int    `json:"reportedTab"`
	ReportedMainFrame string `json:"reportedMainFrame"`
	TerminationStatus int    `json:"terminationStatus"`
	ErrorCode         int    `json:"errorCode"`
	ErrorTextOmitted  bool   `json:"errorTextOmitted"`
}

type Config struct {
	Executable, Profile string
	Supervisor          supervisor.Adapter
	// ObserveProtocol receives method names only, never page values or CDP data.
	ObserveProtocol func(direction, method string)
	ObserveCommand  func(ProtocolObservation)
}

type Broker struct {
	mu, writeMu           sync.Mutex
	worker                supervisor.Worker
	ipc                   *workeripc.Transport
	conn                  net.Conn
	generation            string
	routes                map[string]*route
	ready                 chan error
	done                  chan struct{}
	closed                chan struct{}
	once                  sync.Once
	closeMu               sync.Mutex
	closeAttempted        bool
	workerNeedsClose      bool
	treeNeedsJoin         bool
	closeErr              error
	err                   error
	observe               func(string, string)
	commands              *commandObserver
	artifacts             map[string]*Artifact
	controls              map[string]chan controlResult
	targets               map[string][]*OwnedTarget
	termination           *RendererTermination
	frames                *frameGraph
	renderers             *rendererTracker
	rendererNeedsClose    bool
	framePauseOnce        sync.Once
	framePauseDone        chan struct{}
	controlPauseConfirmed bool
	rendererJoinReason    string
}

func (b *Broker) RendererTermination() (RendererTermination, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.termination == nil {
		return RendererTermination{}, false
	}
	return *b.termination, true
}
func (b *Broker) ControlPaused() bool {
	if b == nil {
		return false
	}
	select {
	case <-b.done:
	default:
		return false
	}
	select {
	case <-b.closed:
		return false
	default:
		return true
	}
}

// Start launches only an explicit component path under the existing supervisor.
// The per-launch HMAC credential is removed by the broker before CEF creates
// helpers; ConnectProcess checks the named-pipe peer against the actual child.
func Start(ctx context.Context, c Config) (*Broker, error) {
	if c.Supervisor == nil || !filepath.IsAbs(c.Executable) || !filepath.IsAbs(c.Profile) {
		return nil, automation.ErrUnavailable
	}
	if err := os.MkdirAll(c.Profile, 0700); err != nil {
		return nil, err
	}
	ipc, err := workeripc.New()
	if err != nil {
		return nil, err
	}
	b := &Broker{ipc: ipc, generation: rand.Text(), routes: map[string]*route{}, ready: make(chan error, 1), done: make(chan struct{}), closed: make(chan struct{}), observe: c.ObserveProtocol, commands: newCommandObserver(c.ObserveCommand), artifacts: map[string]*Artifact{}}
	env := map[string]string{}
	for _, kv := range os.Environ() {
		k, v, _ := strings.Cut(kv, "=")
		upper := strings.ToUpper(k)
		if strings.Contains(upper, "TOKEN") || strings.Contains(upper, "SECRET") || strings.Contains(upper, "PASSWORD") || strings.Contains(upper, "API_KEY") || strings.HasPrefix(upper, "BROWSER_LAB_") {
			continue
		}
		env[k] = v
	}
	for k, v := range ipc.ChildEnvironment() {
		env[k] = v
	}
	env["DSH_WORK_CEF_GENERATION"] = b.generation
	env["DSH_WORK_CEF_PROFILE"] = c.Profile
	if err = nativeEnvironment(env); err != nil {
		ipc.Close()
		return nil, err
	}
	b.worker, err = c.Supervisor.Start(ctx, supervisor.LaunchPlan{GenerationID: "cef-" + b.generation, Executable: c.Executable, WorkingDirectory: filepath.Dir(c.Executable), Env: env, ExpectedOrigin: workeripc.Origin}, nil)
	if err != nil {
		ipc.Close()
		return nil, err
	}
	go func() {
		for range b.worker.Events() {
		}
	}()
	bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	process := b.worker.Diagnostics()
	if process.ProcessStartedError != "" {
		return nil, errors.Join(fmt.Errorf("CEF_PROCESS_INSTANCE_UNAVAILABLE: %s", process.ProcessStartedError), b.Close())
	}
	b.conn, err = ipc.ConnectProcessInstance(bounded, process.PID, process.ProcessStarted)
	if err != nil {
		return nil, errors.Join(err, b.Close())
	}
	b.renderers = newRendererTracker(b, c.Executable)
	b.rendererNeedsClose = b.renderers != nil
	go b.receive()
	go func() {
		select {
		case <-b.worker.Exited():
			b.abort(errors.New("CEF_BROKER_EXITED"))
		case <-b.closed:
		}
	}()
	select {
	case err = <-b.ready:
	case <-b.done:
		err = b.failure()
	case <-bounded.Done():
		err = bounded.Err()
	}
	if err != nil {
		return nil, errors.Join(err, b.Close())
	}
	// The launch context owns the whole running broker, not just startup. A
	// renderer pause keeps this owner alive; cancellation joins bounded cleanup.
	go func() {
		select {
		case <-ctx.Done():
			b.abort(ctx.Err())
		case <-b.closed:
		}
	}()
	return b, nil
}

// Select is a trusted in-process dependency for browserruntime, not a provider
// choice accepted from tool JSON. Product Service dispatch remains closed until
// its separate embedded admission/permission scope has been integrated.
func (b *Broker) Select(_ context.Context, r automation.Request) (browserruntime.RouteSelection, error) {
	if r.Session == "" || r.Lifetime == "" {
		return browserruntime.RouteSelection{}, automation.ErrRevoked
	}
	select {
	case <-b.done:
		return browserruntime.RouteSelection{}, b.failure()
	default:
	}
	return browserruntime.RouteSelection{Protocol: "owned-cef-v1", Open: func(ctx context.Context, _ string) (automation.BrowserRoute, error) { return b.open(ctx) }}, nil
}

func (b *Broker) open(ctx context.Context) (automation.BrowserRoute, error) {
	return b.openURL(ctx, "about:blank")
}
func (b *Broker) openURL(ctx context.Context, address string, managed ...bool) (automation.BrowserRoute, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r := &route{broker: b, id: rand.Text(), messages: make(chan json.RawMessage, 256), done: make(chan struct{})}
	b.mu.Lock()
	select {
	case <-b.done:
		b.mu.Unlock()
		return nil, b.failure()
	default:
	}
	b.routes[r.id] = r
	b.mu.Unlock()
	managedTarget := len(managed) > 0 && managed[0]
	if err := b.write(map[string]any{"kind": "open", "route": r.id, "url": address, "managed": managedTarget}); err != nil {
		r.Close()
		return nil, err
	}
	return r, nil
}
func (b *Broker) write(m map[string]any) error {
	b.writeMu.Lock()
	defer b.writeMu.Unlock()
	select {
	case <-b.done:
		if m["kind"] != "control.pause" && m["kind"] != "release" && m["kind"] != "close" && m["kind"] != "shutdown" && m["kind"] != "artifact.revoke" && m["kind"] != "resource.result" && m["kind"] != "target.revoke" && m["kind"] != "target.end" && m["kind"] != "target.close" && m["kind"] != "target.hide" && m["kind"] != "target.state" && m["kind"] != "target.navigation" {
			return b.failure()
		}
	default:
	}
	m["generation"] = b.generation
	b.conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
	defer b.conn.SetWriteDeadline(time.Time{})
	return browsernative.WriteWireMessage(b.conn, m)
}
func (b *Broker) receive() {
	gotReady := false
	for {
		var raw json.RawMessage
		if err := browsernative.ReadWireMessage(b.conn, &raw); err != nil {
			b.abort(err)
			return
		}
		var m struct {
			Kind, Generation, Route, Scope, Method, Owner string
			Protocol                                      int
			ID                                            int
			Payload                                       json.RawMessage
		}
		if err := json.Unmarshal(raw, &m); err != nil {
			b.abort(err)
			return
		}
		if m.Generation != b.generation {
			b.abort(errors.New("CEF_GENERATION_REJECTED"))
			return
		}
		if m.Kind == "frame.lifecycle" {
			b.mu.Lock()
			if b.frames == nil {
				b.frames = &frameGraph{}
			}
			if b.frames.invalidReason != "" {
				b.mu.Unlock()
				continue
			}
			err := b.frames.apply(raw)
			b.mu.Unlock()
			if err != nil {
				b.pauseFrameGraph(err)
				continue
			}
			if b.renderers != nil {
				b.renderers.notify()
			}
			continue
		}

		if m.Kind == "target.closed" {
			b.mu.Lock()
			targets := b.targets[m.Owner]
			delete(b.targets, m.Owner)
			b.mu.Unlock()
			for _, target := range targets {
				target.nativeClosedOnce.Do(func() { close(target.nativeClosed) })
			}
			continue
		}
		if m.Kind == "dispatch.observed" {
			b.commands.accepted(m.Route, m.ID, m.Method)
			if b.observe != nil {
				b.observe("dispatch", m.Method)
			}
			continue
		}
		if m.Kind == "policy.denied" {
			if m.Owner == "" || (m.Method != "CEF_DIALOG_UNSUPPORTED" && m.Method != "CEF_DOWNLOAD_UNSUPPORTED" && m.Method != "CEF_DIALOG_PAYLOAD_TOO_LARGE" && m.Method != "CEF_FILE_RESOURCE_REQUIRED") {
				b.abort(errors.New("CEF_POLICY_EVENT_REJECTED"))
				return
			}
			if b.observe != nil {
				b.observe("policy", m.Method)
			}
			continue
		}
		if m.Kind == "target.result" {
			var result controlResult
			if json.Unmarshal(raw, &result) != nil {
				b.abort(errors.New("CEF_TARGET_PROTOCOL_REJECTED"))
				return
			}
			b.mu.Lock()
			ch := b.controls[result.Request]
			b.mu.Unlock()
			if ch != nil {
				select {
				case ch <- result:
				default:
				}
			}
			continue
		}
		if strings.HasPrefix(m.Kind, "artifact.") || m.Kind == "resource" {
			if err := b.artifactMessage(raw); err != nil {
				b.abort(err)
				return
			}
			continue
		}
		if m.Kind == "ready" {
			if m.Protocol != 1 || gotReady {
				b.abort(errors.New("CEF_PROTOCOL_REJECTED"))
				return
			}
			gotReady = true
			select {
			case b.ready <- nil:
			default:
				b.abort(errors.New("CEF_DUPLICATE_READY"))
				return
			}
			continue
		}
		if m.Kind == "fault" {
			if m.Scope != "broker" {
				b.abort(errors.New("CEF_FAULT_SCOPE_REJECTED"))
				return
			}
			var termination RendererTermination
			if err := json.Unmarshal(raw, &termination); err != nil {
				b.abort(err)
				return
			}
			b.mu.Lock()
			var graphFault struct {
				Revision string `json:"graphRevision"`
				Epoch    string `json:"faultEpoch"`
			}
			_ = json.Unmarshal(raw, &graphFault)
			if (b.frames == nil || b.frames.invalidReason == "") && (graphFault.Revision != "" || graphFault.Epoch != "") {
				revision, revisionErr := graphSequence(graphFault.Revision)
				epoch, epochErr := graphSequence(graphFault.Epoch)
				if b.frames == nil {
					b.frames = &frameGraph{}
				}
				if revisionErr != nil || epochErr != nil || revision != b.frames.revision || epoch != b.frames.fault+1 {
					b.frames.invalidReason = "CEF_GRAPH_FAULT_FENCE_REJECTED"
					b.termination = &termination
					b.controlPauseConfirmed = true
					b.mu.Unlock()
					b.invalidate(errors.New("CEF_GRAPH_FAULT_FENCE_REJECTED"))
					continue
				}
				b.frames.fault = epoch
			}
			b.termination = &termination
			b.controlPauseConfirmed = true
			b.mu.Unlock()
			if b.renderers != nil {
				b.renderers.fence(b.frameGraphOnly(), termination)
				b.renderers.notify()
			}
			b.invalidate(errors.New("CEF_RENDERER_SCOPE_UNCERTAIN"))
			// Preserve broker-owned documents and the cleanup owner. Control
			// cannot resume in this candidate; Close/parent cancellation disposes.
			continue
		}
		if m.Kind != "" || m.Route == "" || len(m.Payload) == 0 || !json.Valid(m.Payload) {
			b.abort(errors.New("CEF_PROTOCOL_REJECTED"))
			return
		}
		b.mu.Lock()
		r := b.routes[m.Route]
		b.mu.Unlock()
		if b.observe != nil {
			var p struct {
				Method string
				ID     int
				Error  string
			}
			_ = json.Unmarshal(m.Payload, &p)
			if p.Method != "" {
				b.observe("event", p.Method)
			} else if p.Error != "" {
				b.observe("result", "rejected")
			}
		}
		b.commands.response(m.Route, m.Payload)
		if r == nil {
			continue
		}
		select {
		case r.messages <- m.Payload:
		case <-r.done:
		default:
			b.abort(errors.New("CEF_ROUTE_BACKPRESSURE"))
			return
		}
	}
}
func (b *Broker) failure() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.err != nil {
		return b.err
	}
	return ErrClosed
}
func (b *Broker) abort(err error) { b.invalidate(err); go b.Close() }
func (b *Broker) invalidate(err error) {
	b.once.Do(func() {
		b.mu.Lock()
		b.err = err
		rs := make([]*route, 0, len(b.routes))
		for _, r := range b.routes {
			rs = append(rs, r)
		}
		close(b.done)
		b.mu.Unlock()
		for _, r := range rs {
			r.finish(err)
		}
	})
}
func (b *Broker) Close() error {
	b.closeMu.Lock()
	defer b.closeMu.Unlock()
	if !b.closeAttempted {
		b.closeErr = b.close()
		b.closeAttempted = true
		close(b.closed)
	} else if b.workerNeedsClose || b.treeNeedsJoin || b.rendererNeedsClose {
		b.closeErr = errors.Join(b.closeErr, b.joinWorker())
	}

	return b.closeErr
}
func (b *Broker) close() error {
	var result error
	b.mu.Lock()
	leases := make([]*Artifact, 0, len(b.artifacts))
	for _, a := range b.artifacts {
		leases = append(leases, a)
	}
	b.mu.Unlock()
	for _, a := range leases {
		a.mu.Lock()
		a.revoked = true
		a.cancel()
		a.mu.Unlock()
	}
	if b.conn != nil {
		if err := b.write(map[string]any{"kind": "shutdown"}); err != nil {
			result = errors.Join(result, fmt.Errorf("CEF shutdown dispatch: %w", err))
		}
		if err := b.conn.Close(); err != nil {
			result = errors.Join(result, fmt.Errorf("CEF pipe close: %w", err))
		}
	}
	b.invalidate(ErrClosed)
	if b.worker != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		select {
		case <-b.worker.Exited():
		case <-ctx.Done():
			forceCtx, stopForce := context.WithTimeout(context.Background(), 5*time.Second)
			result = errors.Join(result, errors.New("CEF_SHUTDOWN_TIMEOUT"), b.worker.ForceStop(forceCtx))
			stopForce()
		}
		if exit := b.worker.ExitResult(); exit.Code != 0 || exit.Err != "" {
			result = errors.Join(result, fmt.Errorf("CEF_SHUTDOWN_FAILED: exit=%d error=%s", exit.Code, exit.Err))
		}
		b.treeNeedsJoin = true
		b.workerNeedsClose = true
		result = errors.Join(result, b.joinWorker())
	}
	if b.ipc != nil {
		result = errors.Join(result, b.ipc.Close())
	}
	for _, a := range leases {
		joined := make(chan struct{})
		go func() { a.reads.Wait(); close(joined) }()
		select {
		case <-joined:
		case <-time.After(5 * time.Second):
			result = errors.Join(result, errors.New("CEF_ARTIFACT_READ_SHUTDOWN_TIMEOUT"))
		}
		result = errors.Join(result, a.closeRoot())
	}
	return result
}

// Closed joins the lifetime-owner shutdown attempt. Close reports any incomplete
// cleanup and can retry a retained supervisor join. Route.Done only invalidates
// Agent control; it does not say the document was destroyed.
func (b *Broker) Closed() <-chan struct{} { return b.closed }

type route struct {
	broker   *Broker
	id       string
	messages chan json.RawMessage
	done     chan struct{}
	once     sync.Once
	mu       sync.Mutex
	err      error
}

func (r *route) Send(ctx context.Context, p json.RawMessage) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-r.done:
		return r.err
	default:
	}
	if r.broker.observe != nil {
		var m struct {
			Method string
			Params []json.RawMessage
		}
		_ = json.Unmarshal(p, &m)
		method := m.Method
		if method == "devtools.dispatch" && len(m.Params) > 1 {
			var command string
			_ = json.Unmarshal(m.Params[1], &command)
			method += "/" + command
		}
		r.broker.observe("command", method)
	}
	r.broker.commands.command(r.id, p)
	return r.broker.write(map[string]any{"kind": "wire", "route": r.id, "payload": p})
}
func (r *route) Messages() <-chan json.RawMessage { return r.messages }
func (r *route) Done() <-chan struct{}            { return r.done }
func (r *route) Err() error                       { r.mu.Lock(); defer r.mu.Unlock(); return r.err }
func (r *route) finish(err error) {
	r.once.Do(func() { r.broker.commands.retire(r.id); r.mu.Lock(); r.err = err; close(r.done); r.mu.Unlock() })
}
func (r *route) Close() error {
	r.finish(automation.ErrRevoked)
	r.broker.mu.Lock()
	delete(r.broker.routes, r.id)
	r.broker.mu.Unlock()
	return r.broker.write(map[string]any{"kind": "release", "route": r.id})
}

// CleanupPending distinguishes retained supervisor join ownership from a
// historical shutdown error. Callers must retain the profile lease until false.
func (b *Broker) CleanupPending() bool {
	b.closeMu.Lock()
	defer b.closeMu.Unlock()
	return !b.closeAttempted || b.workerNeedsClose || b.treeNeedsJoin || b.rendererNeedsClose
}

// joinWorker retains the real job/process handles until an explicit successful
// tree-empty observation. Historical errors stay in Close's result; pending
// ownership is independent and retryable.
func (b *Broker) joinWorker() error {
	var result error
	if b.rendererNeedsClose {
		complete, err := b.renderers.close()
		result = errors.Join(result, err)
		b.rendererNeedsClose = !complete
	}
	if b.treeNeedsJoin {
		wait, stop := context.WithTimeout(context.Background(), 5*time.Second)
		err := b.worker.WaitEmpty(wait)
		stop()
		if err != nil {
			result = errors.Join(result, fmt.Errorf("CEF process tree: %w", err))
			force, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			result = errors.Join(result, b.worker.ForceStop(force))
			cancel()
			wait, stop = context.WithTimeout(context.Background(), 5*time.Second)
			err = b.worker.WaitEmpty(wait)
			stop()
			if err != nil {
				return errors.Join(result, fmt.Errorf("CEF process tree after force: %w", err))
			}
		}
		b.treeNeedsJoin = false
	}
	if b.workerNeedsClose {
		if err := b.worker.Close(); err != nil {
			return errors.Join(result, fmt.Errorf("CEF worker close: %w", err))
		}
		b.workerNeedsClose = false
	}
	return result
}

// A missing/contradictory graph is an observation failure, not proof that any
// healthy document was destroyed. Establish the native final-dispatch pause;
// only inability to confirm this safety barrier requires terminal cleanup.
func (b *Broker) pauseFrameGraph(cause error)    { b.pauseGraph(cause, true) }
func (b *Broker) pauseRendererGraph(cause error) { b.pauseGraph(cause, false) }
func (b *Broker) pauseGraph(cause error, invalidSequence bool) {
	b.framePauseOnce.Do(func() {
		b.mu.Lock()
		if b.frames == nil {
			b.frames = &frameGraph{}
		}
		if invalidSequence {
			b.frames.invalidReason = cause.Error()
		} else {
			b.rendererJoinReason = cause.Error()
		}
		b.framePauseDone = make(chan struct{})
		done := b.framePauseDone
		b.mu.Unlock()
		b.invalidate(cause)
		if b.renderers != nil {
			b.renderers.notify()
		}
		go func() {
			defer close(done)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_, err := b.controlCall(ctx, "control.pause", "", nil)
			if err != nil {
				b.abort(errors.Join(errors.New("CEF_GRAPH_PAUSE_UNCONFIRMED"), err))
				return
			}
			b.mu.Lock()
			b.controlPauseConfirmed = true
			b.mu.Unlock()
		}()
	})
}
