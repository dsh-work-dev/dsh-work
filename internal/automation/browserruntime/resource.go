// Package browserruntime is the formal browser backend. Go owns the
// resource tree; the child only adapts the locked MCP SDK and CDP V2 model.
package browserruntime

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/automation/browsernative"
	"github.com/local/dsh-work/internal/supervisor"
	"github.com/local/dsh-work/internal/workeripc"
)

//go:embed adapter.mjs cdp-adapter.mjs refs.mjs inspect.mjs schemas.json
var sources embed.FS

type Runtime struct {
	Node, Dependencies string
	Environment        map[string]string
}
type Config struct {
	Service    *automation.Service
	Supervisor supervisor.Adapter
	Resolve    func(context.Context) (Runtime, error)
	Root       string
	OutputRoot string
	Observe    func(automation.Request, Observation)
}
type Observation struct {
	Tool                       string
	RawResult, ProjectedResult json.RawMessage
}

// ToolSchemas is a frozen projection of actual locked listTools output. The
// script-capable tools remain withheld until the step-5 gate defines access.
func ToolSchemas() []json.RawMessage {
	data, _ := sources.ReadFile("schemas.json")
	var all []json.RawMessage
	_ = json.Unmarshal(data, &all)
	var out []json.RawMessage
	for _, raw := range all {
		var tool struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(raw, &tool)
		if tool.Name != "browser_evaluate" && tool.Name != "browser_run_code_unsafe" {
			out = append(out, raw)
		}
	}
	return out
}

type envelope struct {
	Kind       string          `json:"kind"`
	ID         uint64          `json:"id,omitempty"`
	Payload    json.RawMessage `json:"payload,omitempty"`
	Result     json.RawMessage `json:"result,omitempty"`
	RawResult  json.RawMessage `json:"rawResult,omitempty"`
	AllSchemas json.RawMessage `json:"allSchemas,omitempty"`
	Error      string          `json:"error,omitempty"`
	Fatal      bool            `json:"fatal,omitempty"`
}
type resource struct {
	ctx          context.Context
	cancel       context.CancelFunc
	route        automation.BrowserRoute
	ipc          *workeripc.Transport
	worker       supervisor.Worker
	conn         net.Conn
	dir          string
	writeMu      sync.Mutex
	mu           sync.Mutex
	sequence     uint64
	pending      map[uint64]chan envelope
	ready        chan envelope
	adapterReady chan struct{}
	done         chan struct{}
	closing      sync.Once
	initMu       sync.Mutex
	signaled     sync.Once
	closeErr     error
	failed       error
	owner        automation.Request
	observe      func(automation.Request, Observation)
}

func NewFactory(c Config) automation.Factory {
	return func(parent context.Context, request automation.Request) (result automation.Resource, err error) {
		if c.Service == nil || c.Supervisor == nil || c.Resolve == nil {
			return nil, automation.ErrUnavailable
		}
		if request.WorkingDirectory != "" {
			info, statErr := os.Stat(request.WorkingDirectory)
			if !filepath.IsAbs(request.WorkingDirectory) || statErr != nil || !info.IsDir() {
				return nil, errors.New("BROWSER_WORKSPACE_UNAVAILABLE: reopen the session in an existing absolute working directory")
			}
		}
		ctx, cancel := context.WithCancel(parent)
		r := &resource{ctx: ctx, cancel: cancel, pending: map[uint64]chan envelope{}, ready: make(chan envelope, 1), adapterReady: make(chan struct{}), done: make(chan struct{})}
		r.owner = request
		r.observe = c.Observe
		defer func() {
			if err != nil {
				r.Close()
			}
		}()
		r.initMu.Lock()
		defer r.initMu.Unlock()
		lease, err := c.Service.SelectedBrowserConnection()
		if err != nil {
			return nil, err
		}
		runtime, err := c.Resolve(ctx)
		if err != nil {
			return nil, err
		}
		if !filepath.IsAbs(runtime.Node) || !filepath.IsAbs(runtime.Dependencies) {
			return nil, errors.New("BROWSER_RUNTIME_PATH_INVALID")
		}
		if err = os.MkdirAll(c.Root, 0700); err != nil {
			return nil, err
		}
		r.dir, err = os.MkdirTemp(c.Root, "session-")
		if err != nil {
			return nil, err
		}
		outputRoot := c.OutputRoot
		if outputRoot == "" {
			outputRoot = filepath.Join(c.Root, "artifacts")
		}
		output := filepath.Join(outputRoot, filepath.Base(r.dir))
		for _, name := range []string{"adapter.mjs", "cdp-adapter.mjs", "refs.mjs", "inspect.mjs"} {
			data, _ := sources.ReadFile(name)
			if err = os.WriteFile(filepath.Join(r.dir, name), data, 0600); err != nil {
				return nil, err
			}
		}
		r.ipc, err = workeripc.New()
		if err != nil {
			return nil, err
		}
		env := map[string]string{}
		for _, kv := range os.Environ() {
			k, v, ok := strings.Cut(kv, "=")
			upper := strings.ToUpper(k)
			if ok && !strings.Contains(upper, "TOKEN") && !strings.Contains(upper, "SECRET") && !strings.Contains(upper, "PASSWORD") && !strings.Contains(upper, "API_KEY") && !strings.HasPrefix(upper, "PLAYWRIGHT_") && upper != "NODE_OPTIONS" {
				env[k] = v
			}
		}
		for k, v := range runtime.Environment {
			env[k] = v
		}
		for k, v := range r.ipc.ChildEnvironment() {
			env[k] = v
		}
		r.worker, err = c.Supervisor.Start(ctx, supervisor.LaunchPlan{GenerationID: "browser-" + filepath.Base(r.dir), Executable: runtime.Node, Args: []string{filepath.Join(r.dir, "adapter.mjs")}, Env: env, WorkingDirectory: r.dir, ExpectedOrigin: workeripc.Origin}, nil)
		if err != nil {
			return nil, err
		}
		go func() {
			for range r.worker.Events() {
			}
		}()
		bounded, stop := context.WithTimeout(ctx, 25*time.Second)
		defer stop()
		r.conn, err = r.ipc.Connect(bounded)
		if err != nil {
			return nil, err
		}
		go r.receive()
		if err = r.write(map[string]any{"kind": "init", "dependencies": runtime.Dependencies, "output": output, "workspace": request.WorkingDirectory, "refScope": rand.Text()}); err != nil {
			return nil, err
		}
		select {
		case <-r.adapterReady:
		case <-r.done:
			return nil, r.failure()
		case <-bounded.Done():
			return nil, bounded.Err()
		}
		var route automation.BrowserRoute
		route, err = c.Service.OpenBrowserRoute(ctx, lease, request.Title)
		if err != nil {
			return nil, err
		}
		r.mu.Lock()
		r.route = route
		r.mu.Unlock()
		go r.forward(route)
		select {
		case message := <-r.ready:
			if message.Error != "" {
				return nil, errors.New(message.Error)
			}
			// Compare the actual listTools contract with this build's pinned capture.
			want, _ := sources.ReadFile("schemas.json")
			var a, b any
			if json.Unmarshal(want, &a) != nil || json.Unmarshal(message.AllSchemas, &b) != nil {
				return nil, errors.New("BROWSER_TOOL_CONTRACT_INVALID")
			}
			x, _ := json.Marshal(a)
			y, _ := json.Marshal(b)
			if string(x) != string(y) {
				return nil, errors.New("BROWSER_TOOL_CONTRACT_CHANGED")
			}
		case <-r.done:
			return nil, r.failure()
		case <-bounded.Done():
			return nil, bounded.Err()
		}
		go func() {
			select {
			case <-ctx.Done():
				r.Close()
			case <-r.worker.Exited():
				r.fail(errors.New("BROWSER_MCP_EXITED"))
			case <-r.done:
			}
		}()
		return r, nil
	}
}
func (r *resource) write(v any) error {
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	if r.conn == nil {
		return automation.ErrUnavailable
	}
	r.conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
	defer r.conn.SetWriteDeadline(time.Time{})
	return browsernative.WriteWireMessage(r.conn, v)
}
func (r *resource) receive() {
	for {
		var m envelope
		if err := browsernative.ReadWireMessage(r.conn, &m); err != nil {
			r.fail(err)
			return
		}
		switch m.Kind {
		case "adapter-ready":
			select {
			case <-r.adapterReady:
			default:
				close(r.adapterReady)
			}
		case "ready":
			select {
			case r.ready <- m:
			default:
				r.fail(errors.New("BROWSER_DUPLICATE_READY"))
				return
			}
		case "fatal":
			r.fail(errors.New(m.Error))
			return
		case "wire":
			r.mu.Lock()
			route := r.route
			r.mu.Unlock()
			if route == nil {
				r.fail(errors.New("BROWSER_ROUTE_NOT_READY"))
				return
			}
			if err := route.Send(r.ctx, m.Payload); err != nil {
				r.fail(err)
				return
			}
		case "result":
			r.mu.Lock()
			ch := r.pending[m.ID]
			r.mu.Unlock()
			if ch != nil {
				select {
				case ch <- m:
				default:
				}
			}
		default:
			r.fail(errors.New("BROWSER_ADAPTER_INVALID_MESSAGE"))
			return
		}
	}
}
func (r *resource) forward(route automation.BrowserRoute) {
	for {
		select {
		case m := <-route.Messages():
			if err := r.write(map[string]any{"kind": "wire", "payload": m}); err != nil {
				r.fail(err)
				return
			}
		case <-route.Done():
			r.fail(route.Err())
			return
		case <-r.done:
			return
		}
	}
}
func (r *resource) Execute(ctx context.Context, tool string, args map[string]any) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case <-r.done:
		return nil, r.failure()
	default:
	}
	data, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	if len(data) > 256<<10 {
		return nil, errors.New("BROWSER_INPUT_TOO_LARGE: reduce complete UTF-8 JSON arguments to 262144 bytes")
	}
	r.mu.Lock()
	r.sequence++
	id := r.sequence
	ch := make(chan envelope, 1)
	r.pending[id] = ch
	r.mu.Unlock()
	defer func() { r.mu.Lock(); delete(r.pending, id); r.mu.Unlock() }()
	if err := r.write(map[string]any{"kind": "call", "id": id, "tool": tool, "arguments": args}); err != nil {
		r.fail(err)
		return nil, err
	}
	// A single-call cancellation drains the bounded MCP action. A resource-root
	// cancellation closes the Job and route independently, interrupting this wait.
	timer := time.NewTimer(40 * time.Second)
	defer timer.Stop()
	for {
		select {
		case m := <-ch:
			if r.observe != nil && len(m.RawResult) > 0 {
				owner := r.owner
				owner.Tool = tool
				owner.Arguments = args
				r.observe(owner, Observation{Tool: tool, RawResult: append(json.RawMessage(nil), m.RawResult...), ProjectedResult: append(json.RawMessage(nil), m.Result...)})
			}
			if m.Fatal {
				r.fail(errors.New(m.Error))
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if m.Error != "" {
				return nil, errors.New(m.Error)
			}
			var result any
			if err := json.Unmarshal(m.Result, &result); err != nil {
				return nil, err
			}
			return result, nil
		case <-r.done:
			return nil, r.failure()
		case <-timer.C:
			r.fail(errors.New("BROWSER_ACTION_DRAIN_TIMEOUT"))
			return nil, r.failure()
		}
	}
}
func (r *resource) failure() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failed != nil {
		return r.failed
	}
	return errors.New("BROWSER_RESOURCE_CLOSED")
}
func (r *resource) fail(err error) {
	r.mu.Lock()
	if r.failed == nil {
		r.failed = err
	}
	r.mu.Unlock()
	r.cancel()
	r.signaled.Do(func() { close(r.done) })
	go r.Close()
}
func (r *resource) Close() error {
	r.mu.Lock()
	if r.failed == nil {
		r.failed = errors.New("BROWSER_RESOURCE_CLOSED")
	}
	r.mu.Unlock()
	r.cancel()
	r.signaled.Do(func() { close(r.done) })
	r.initMu.Lock()
	defer r.initMu.Unlock()
	r.closing.Do(func() {
		// Detach the native route even if process teardown fails, and conversely.
		if r.route != nil {
			_ = r.route.Close()
		}
		if r.conn != nil {
			_ = r.conn.Close()
		}
		if r.ipc != nil {
			_ = r.ipc.Close()
		}
		if r.worker != nil {
			ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			r.closeErr = errors.Join(r.worker.ForceStop(ctx), r.worker.WaitEmpty(ctx), r.worker.Close())
		}
		// Artifacts live outside this generated-code directory. Never remove a
		// tree while an owned process may still be using it.
		if r.closeErr == nil && r.dir != "" {
			r.closeErr = os.RemoveAll(r.dir)
		}
	})
	return r.closeErr
}

// AutomationTerminal distinguishes a closed process/route from a recoverable MCP
// tool error. The Service can end that owned lifetime without stranding retries.
func (r *resource) AutomationTerminal() bool {
	select {
	case <-r.done:
		return true
	default:
		return false
	}
}
