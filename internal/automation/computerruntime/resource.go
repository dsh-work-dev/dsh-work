package computerruntime

import (
	"context"
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

//go:embed adapter.mjs auv.mjs cua-schemas.json
var sources embed.FS

type Runtime struct {
	Node, Dependencies, Driver string
	Environment                map[string]string
}
type Config struct {
	Supervisor supervisor.Adapter
	Root       string
	Resolve    func(context.Context, string) (Runtime, error)
}
type envelope struct {
	Kind   string          `json:"kind"`
	ID     uint64          `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  string          `json:"error"`
	Fatal  bool            `json:"fatal,omitempty"`
}
type resource struct {
	ctx         context.Context
	cancel      context.CancelFunc
	ipc         *workeripc.Transport
	conn        net.Conn
	worker      supervisor.Worker
	dir         string
	initMu      sync.Mutex
	writeMu, mu sync.Mutex
	sequence    uint64
	pending     map[uint64]chan envelope
	ready       chan envelope
	done        chan struct{}
	once        sync.Once
	closeOnce   sync.Once
	err         error
	closeErr    error
}

func NewFactory(c Config) automation.ComputerFactory {
	return func(parent context.Context, engine string) (_ automation.Resource, err error) {
		if c.Supervisor == nil || c.Resolve == nil {
			return nil, automation.ErrUnavailable
		}
		runtime, err := c.Resolve(parent, engine)
		if err != nil {
			return nil, err
		}
		if !filepath.IsAbs(runtime.Node) || !filepath.IsAbs(runtime.Dependencies) || engine != "cua-driver" && engine != "auv" || !filepath.IsAbs(runtime.Driver) {
			return nil, automation.ErrUnavailable
		}
		ctx, cancel := context.WithCancel(parent)
		r := &resource{ctx: ctx, cancel: cancel, pending: map[uint64]chan envelope{}, ready: make(chan envelope, 1), done: make(chan struct{})}
		r.initMu.Lock()
		defer func() {
			r.initMu.Unlock()
			if err != nil {
				r.Close()
			}
		}()
		if err = os.MkdirAll(c.Root, 0700); err != nil {
			return nil, err
		}
		r.dir, err = os.MkdirTemp(c.Root, "desktop-")
		if err != nil {
			return nil, err
		}
		source, _ := sources.ReadFile("adapter.mjs")
		if err = os.WriteFile(filepath.Join(r.dir, "adapter.mjs"), source, 0600); err != nil {
			return nil, err
		}
		auv, _ := sources.ReadFile("auv.mjs")
		if err = os.WriteFile(filepath.Join(r.dir, "auv.mjs"), auv, 0600); err != nil {
			return nil, err
		}
		r.ipc, err = workeripc.New()
		if err != nil {
			return nil, err
		}
		env := map[string]string{}
		for _, kv := range os.Environ() {
			k, v, ok := strings.Cut(kv, "=")
			u := strings.ToUpper(k)
			if ok && !strings.Contains(u, "TOKEN") && !strings.Contains(u, "SECRET") && !strings.Contains(u, "PASSWORD") && !strings.Contains(u, "API_KEY") && u != "NODE_OPTIONS" {
				env[k] = v
			}
		}
		for k, v := range runtime.Environment {
			env[k] = v
		}
		for k, v := range r.ipc.ChildEnvironment() {
			env[k] = v
		}
		r.worker, err = c.Supervisor.Start(ctx, supervisor.LaunchPlan{GenerationID: filepath.Base(r.dir), Executable: runtime.Node, Args: []string{filepath.Join(r.dir, "adapter.mjs")}, Env: env, WorkingDirectory: r.dir, ExpectedOrigin: workeripc.Origin}, nil)
		if err != nil {
			return nil, err
		}
		go func() {
			for range r.worker.Events() {
			}
		}()
		bounded, stop := context.WithTimeout(ctx, 20*time.Second)
		defer stop()
		r.conn, err = r.ipc.Connect(bounded)
		if err != nil {
			return nil, err
		}
		go r.receive()
		schema, _ := sources.ReadFile("cua-schemas.json")
		if err = r.write(map[string]any{"kind": "init", "engine": engine, "dependencies": runtime.Dependencies, "driver": runtime.Driver, "schemas": json.RawMessage(schema)}); err != nil {
			return nil, err
		}
		select {
		case m := <-r.ready:
			if m.Error != "" {
				return nil, errors.New(m.Error)
			}
		case <-bounded.Done():
			return nil, bounded.Err()
		case <-r.done:
			return nil, r.failure()
		}
		go func() {
			select {
			case <-ctx.Done():
				r.Close()
			case <-r.worker.Exited():
				r.fail(errors.New("COMPUTER_ENGINE_EXITED"))
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
		if e := browsernative.ReadWireMessage(r.conn, &m); e != nil {
			r.fail(e)
			return
		}
		switch m.Kind {
		case "ready":
			select {
			case r.ready <- m:
			default:
				r.fail(errors.New("COMPUTER_DUPLICATE_READY"))
				return
			}
		case "result":
			if m.Fatal {
				r.fail(errors.New(m.Error))
			}
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
			r.fail(errors.New("COMPUTER_ADAPTER_INVALID_MESSAGE"))
			return
		}
	}
}
func (r *resource) Execute(ctx context.Context, tool string, args map[string]any) (any, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	select {
	case <-r.done:
		return nil, r.failure()
	default:
	}
	r.mu.Lock()
	r.sequence++
	id := r.sequence
	ch := make(chan envelope, 1)
	r.pending[id] = ch
	r.mu.Unlock()
	defer func() { r.mu.Lock(); delete(r.pending, id); r.mu.Unlock() }()
	if e := r.write(map[string]any{"kind": "call", "id": id, "tool": tool, "arguments": args}); e != nil {
		r.fail(e)
		return nil, e
	}
	select {
	case m := <-ch:
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if m.Error != "" {
			return nil, errors.New(m.Error)
		}
		var v any
		e := json.Unmarshal(m.Result, &v)
		return v, e
	case <-ctx.Done():
		r.fail(ctx.Err())
		return nil, ctx.Err()
	case <-r.done:
		return nil, r.failure()
	case <-time.After(35 * time.Second):
		r.fail(errors.New("COMPUTER_ACTION_TIMEOUT"))
		return nil, r.failure()
	}
}
func (r *resource) failure() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	return errors.New("COMPUTER_ENGINE_CLOSED")
}
func (r *resource) fail(e error) {
	r.mu.Lock()
	if r.err == nil {
		r.err = e
	}
	r.mu.Unlock()
	r.cancel()
	r.once.Do(func() { close(r.done) })
	go r.Close()
}
func (r *resource) Close() error {
	r.cancel()
	r.once.Do(func() { close(r.done) })
	r.initMu.Lock()
	defer r.initMu.Unlock()
	r.closeOnce.Do(func() {
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
		if r.closeErr == nil && r.dir != "" {
			r.closeErr = os.RemoveAll(r.dir)
		}
	})
	return r.closeErr
}

func (r *resource) AutomationTerminal() bool {
	select {
	case <-r.done:
		return true
	default:
		return false
	}
}
