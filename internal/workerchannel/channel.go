// Package workerchannel owns the authenticated IPC lifetime of each Worker.
package workerchannel

import (
	"context"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"sync"

	"github.com/local/dsh-work/internal/workeripc"
)

type Session interface {
	URL() string
	Generation() string
	Patch() string
	Client() *http.Client
	Activate(string)
	Close() error
}

type Adapter struct {
	mu       sync.RWMutex
	current  *session
	observer func(context.Context, string, http.RoundTripper)
}

func New() *Adapter { return &Adapter{} }
func (a *Adapter) SetWorkerObserver(fn func(context.Context, string, http.RoundTripper)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.observer = fn
}
func (a *Adapter) Current() Session {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.current == nil {
		return nil
	}
	return a.current
}
func (a *Adapter) Prepare(ctx context.Context, generation, dataDirectory, profile string) (Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	pipe, err := workeripc.New()
	if err != nil {
		return nil, err
	}
	root, err := os.MkdirTemp("", "dsh-work-channel-")
	if err != nil {
		pipe.Close()
		return nil, err
	}
	patch, err := pipe.Prepare(root, filepath.Join(dataDirectory, "profiles", profile), "")
	if err != nil {
		pipe.Close()
		os.RemoveAll(root)
		return nil, err
	}
	jar, _ := cookiejar.New(nil)
	s := &session{owner: a, ctx: ctx, pipe: pipe, root: root, patch: patch, generation: generation}
	s.client = &http.Client{Transport: pipe.HTTP, Jar: jar, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if r.URL.Scheme+"://"+r.URL.Host != workeripc.Origin || len(via) > 3 {
			return errors.New("Worker redirect escaped its authority")
		}
		return nil
	}}
	context.AfterFunc(ctx, func() { s.Close() })
	if err := ctx.Err(); err != nil {
		_ = s.Close()
		return nil, err
	}
	return s, nil
}

type session struct {
	owner                   *Adapter
	ctx                     context.Context
	pipe                    *workeripc.Transport
	root, patch, generation string
	client                  *http.Client
	once                    sync.Once
	closed                  bool
	err                     error
}

func (s *session) Generation() string   { return s.generation }
func (s *session) URL() string          { return "/?generation=" + url.QueryEscape(s.generation) }
func (s *session) Patch() string        { return s.patch }
func (s *session) Client() *http.Client { return s.client }
func (s *session) Activate(auth string) {
	s.owner.mu.Lock()
	if s.closed || s.ctx.Err() != nil {
		s.owner.mu.Unlock()
		return
	}
	s.owner.current = s
	observer := s.owner.observer
	s.owner.mu.Unlock()
	if observer != nil {
		observer(s.ctx, auth, s.pipe.HTTP)
	}
}
func (s *session) Close() error {
	s.once.Do(func() {
		s.owner.mu.Lock()
		s.closed = true
		if s.owner.current == s {
			s.owner.current = nil
		}
		s.owner.mu.Unlock()
		s.pipe.Close()
		s.err = os.RemoveAll(s.root)
	})
	return s.err
}
