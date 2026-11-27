package embeddedbrowser

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/automation/browserruntime"
)

var ErrArtifactRevoked = errors.New("CEF_ARTIFACT_REVOKED")

// Artifact owns one root lease and one never-reused native memory partition.
// Its ID/origin name the object; only the authenticated broker registry can
// admit a browser/frame to it. No file server or host control token is exposed.
type Artifact struct {
	broker                                *Broker
	id, partition, origin                 string
	root                                  *os.Root
	route                                 *route
	ctx                                   context.Context
	cancel                                context.CancelFunc
	mu                                    sync.Mutex
	revoked                               bool
	browser                               int
	frames                                map[string]bool
	reads                                 sync.WaitGroup
	active                                int
	ready                                 chan error
	closed                                chan error
	revokeMu                              sync.Mutex
	complete                              bool
	nativeClosed                          bool
	rootOnce                              sync.Once
	rootErr                               error
	stats                                 ArtifactStats
	controllerSession, controllerLifetime string
	claimed                               bool
}

type ArtifactStats struct {
	ReadRequests, ReadBytes         int64
	ClosedBrowsers, CanceledStreams int
	ClosedFrames, RevokedFrames     int
	ReadMarshaled                   int
	PartitionRetired                bool
}

func (a *Artifact) Stats() ArtifactStats { a.mu.Lock(); defer a.mu.Unlock(); return a.stats }
func (a *Artifact) closeRoot() error {
	a.rootOnce.Do(func() { a.rootErr = a.root.Close() })
	return a.rootErr
}

func (a *Artifact) Origin() string { return a.origin }
func (a *Artifact) Select(_ context.Context, r automation.Request) (browserruntime.RouteSelection, error) {
	if r.Session == "" || r.Lifetime == "" {
		return browserruntime.RouteSelection{}, automation.ErrRevoked
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.revoked {
		return browserruntime.RouteSelection{}, ErrArtifactRevoked
	}
	if a.controllerSession != "" && (a.controllerSession != r.Session || a.controllerLifetime != r.Lifetime) {
		return browserruntime.RouteSelection{}, errors.New("CEF_ARTIFACT_CONTROL_CONFLICT")
	}
	a.controllerSession = r.Session
	a.controllerLifetime = r.Lifetime
	return browserruntime.RouteSelection{Protocol: "owned-cef-v1", Open: func(ctx context.Context, _ string) (automation.BrowserRoute, error) {
		a.mu.Lock()
		defer a.mu.Unlock()
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if a.revoked || a.claimed {
			return nil, automation.ErrRevoked
		}
		a.claimed = true
		return a.route, nil
	}}, nil
}

func (b *Broker) OpenArtifact(ctx context.Context, directory, entry string) (*Artifact, error) {
	return b.openArtifact(ctx, directory, entry, false)
}

func (b *Broker) openArtifact(ctx context.Context, directory, entry string, managed bool) (*Artifact, error) {
	ctx, stop := context.WithTimeout(ctx, 15*time.Second)
	defer stop()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(directory) || !filepath.IsLocal(entry) {
		return nil, errors.New("CEF_ARTIFACT_ROOT_REQUIRED")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	f, err := root.Open(entry)
	if err != nil {
		root.Close()
		return nil, err
	}
	info, err := f.Stat()
	f.Close()
	if err != nil || !info.Mode().IsRegular() {
		root.Close()
		return nil, errors.New("CEF_ARTIFACT_NOT_FILE")
	}
	id := strings.ToLower(rand.Text())
	part := rand.Text()
	life, cancel := context.WithCancel(context.Background())
	a := &Artifact{broker: b, id: id, partition: part, origin: "http://" + id + ".artifact.localhost", root: root, ctx: life, cancel: cancel, frames: map[string]bool{}, ready: make(chan error, 1), closed: make(chan error, 1)}
	a.route = &route{broker: b, id: rand.Text(), messages: make(chan json.RawMessage, 256), done: make(chan struct{})}
	b.mu.Lock()
	select {
	case <-b.done:
		b.mu.Unlock()
		cancel()
		root.Close()
		return nil, b.failure()
	default:
	}
	b.artifacts[id] = a
	b.routes[a.route.id] = a.route
	b.mu.Unlock()
	path := (&url.URL{Path: "/" + filepath.ToSlash(entry)}).EscapedPath()
	if err = b.write(map[string]any{"kind": "artifact.open", "lease": id, "partition": part, "origin": a.origin, "route": a.route.id, "entry": path, "managed": managed}); err != nil {
		return nil, a.cleanupFailedOpen(err)
	}
	if b.observe != nil {
		b.observe("artifact", "open-dispatched")
	}
	if err = ctx.Err(); err != nil {
		return nil, a.cleanupFailedOpen(err)
	}
	select {
	case err = <-a.ready:
	case <-b.done:
		err = b.failure()
	case <-ctx.Done():
		err = ctx.Err()
	}
	if err != nil {
		return nil, a.cleanupFailedOpen(err)
	}
	return a, nil
}

// A failed initializer retains the lease until cleanup completes, independently
// of the canceled caller. A late native ACK is consumed by a fresh bounded
// attempt. If two attempts cannot confirm disposal, fail the broker explicitly
// and join its process-tree cleanup rather than orphaning the root/partition.
func (a *Artifact) cleanupFailedOpen(cause error) error {
	var cleanupErr error
	for range 2 {
		ctx, stop := context.WithTimeout(context.Background(), 15*time.Second)
		err := a.Revoke(ctx)
		stop()
		if err == nil {
			return errors.Join(cause, cleanupErr)
		}
		cleanupErr = errors.Join(cleanupErr, err)
	}
	terminal := errors.New("CEF_ARTIFACT_INITIALIZATION_CLEANUP_FAILED")
	a.broker.abort(terminal)
	return errors.Join(cause, cleanupErr, terminal, a.broker.Close())
}

func (b *Broker) ArtifactCount() int { b.mu.Lock(); defer b.mu.Unlock(); return len(b.artifacts) }

// Revoke is a completion barrier, not a cache-clear request: no new Go read is
// admitted, all admitted reads have finished/cancelled, and the native peer has
// acknowledged closing every old browser/client before the root is released.
func (a *Artifact) Revoke(ctx context.Context) error {
	ctx, stop := context.WithTimeout(ctx, 15*time.Second)
	defer stop()
	a.revokeMu.Lock()
	defer a.revokeMu.Unlock()
	a.mu.Lock()
	complete := a.complete
	a.mu.Unlock()
	if complete {
		return a.closeRoot()
	}
	a.mu.Lock()
	a.revoked = true
	a.cancel()
	a.mu.Unlock()
	a.route.finish(ErrArtifactRevoked)
	if a.broker.renderers != nil {
		a.broker.renderers.beginArtifactRetirement(a.route.id)
	}
	// Send the native dispatch/context invalidation immediately; waiting on a
	// canceled filesystem operation must not postpone the native control barrier.
	if !a.nativeClosed {
		if err := a.broker.write(map[string]any{"kind": "artifact.revoke", "lease": a.id, "partition": a.partition}); err != nil {
			return err
		}
	}
	joined := make(chan struct{})
	go func() { a.reads.Wait(); close(joined) }()
	select {
	case <-joined:
	case <-ctx.Done():
		return ctx.Err()
	}
	if !a.nativeClosed {
		select {
		case err := <-a.closed:
			if err != nil {
				return err
			}
		case <-ctx.Done():
			return ctx.Err()
		case <-a.broker.closed:
			return errors.Join(ErrClosed, a.broker.Close())
		}
		a.nativeClosed = true
		if a.broker.renderers != nil {
			a.broker.renderers.finishArtifactRetirement(a.route.id)
		}
	}
	if err := a.closeRoot(); err != nil {
		return err
	}
	a.mu.Lock()
	a.complete = true
	a.mu.Unlock()
	a.broker.mu.Lock()
	delete(a.broker.artifacts, a.id)
	delete(a.broker.routes, a.route.id)
	a.broker.mu.Unlock()
	return nil
}

type artifactMessage struct {
	Kind, Generation, Lease, Partition, Frame, Request, URL, Error string
	Browser                                                        int
	Offset                                                         int64 `json:"offset,string"`
	Limit                                                          int
	ClosedBrowsers, CanceledStreams                                int
	ClosedFrames, LiveFrames, RevokedFrames                        int
	ReadMarshaled                                                  int
	PartitionRetired                                               bool
}

func (b *Broker) artifactMessage(raw json.RawMessage) error {
	var m artifactMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return err
	}
	b.mu.Lock()
	a := b.artifacts[m.Lease]
	b.mu.Unlock()
	if a == nil {
		return errors.New("CEF_ARTIFACT_UNKNOWN_LEASE")
	}
	if m.Partition != a.partition {
		return errors.New("CEF_ARTIFACT_PARTITION_REJECTED")
	}
	switch m.Kind {
	case "artifact.failed":
		a.mu.Lock()
		a.revoked = true
		a.cancel()
		a.mu.Unlock()
		a.route.finish(ErrArtifactRevoked)
		err := errors.Join(errors.New("CEF_ARTIFACT_INITIALIZATION_FAILED"), a.closeRoot())
		a.mu.Lock()
		a.complete = true
		a.mu.Unlock()
		b.mu.Lock()
		delete(b.artifacts, a.id)
		delete(b.routes, a.route.id)
		b.mu.Unlock()
		a.ready <- err
		select {
		case a.closed <- err:
		default:
		}
	case "artifact.bound":
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.browser != 0 || m.Browser <= 0 {
			return errors.New("CEF_ARTIFACT_BROWSER_REJECTED")
		}
		a.browser = m.Browser
		a.ready <- nil
	case "artifact.frame":
		a.mu.Lock()
		defer a.mu.Unlock()
		if m.Browser != a.browser || m.Frame == "" {
			return errors.New("CEF_ARTIFACT_FRAME_REJECTED")
		}
		if !a.revoked {
			a.frames[m.Frame] = true
		}
	case "artifact.detached":
		a.mu.Lock()
		defer a.mu.Unlock()
		if m.Browser != a.browser {
			return errors.New("CEF_ARTIFACT_BROWSER_REJECTED")
		}
		delete(a.frames, m.Frame)
	case "artifact.revoked":
		var err error
		if m.Error != "" || !m.PartitionRetired || m.ClosedBrowsers != 1 || m.ClosedFrames < 1 || m.LiveFrames != 0 {
			err = errors.New("CEF_ARTIFACT_CLOSE_FAILED")
		}
		a.mu.Lock()
		a.stats.ClosedBrowsers = m.ClosedBrowsers
		a.stats.ClosedFrames = m.ClosedFrames
		a.stats.RevokedFrames = m.RevokedFrames
		a.stats.ReadMarshaled = m.ReadMarshaled
		a.stats.CanceledStreams = m.CanceledStreams
		a.stats.PartitionRetired = m.PartitionRetired
		a.mu.Unlock()
		select {
		case a.closed <- err:
		default:
		}
	case "resource":
		a.mu.Lock()
		if a.revoked || m.Browser != a.browser || !a.frames[m.Frame] || a.active >= 16 {
			a.mu.Unlock()
			return b.write(map[string]any{"kind": "resource.result", "request": m.Request, "error": "CEF_ARTIFACT_RESOURCE_DENIED"})
		}
		a.active++
		a.stats.ReadRequests++
		a.reads.Add(1)
		a.mu.Unlock()
		go a.read(m)
	default:
		return errors.New("CEF_ARTIFACT_PROTOCOL_REJECTED")
	}
	return nil
}
func (a *Artifact) read(m artifactMessage) {
	defer func() { a.mu.Lock(); a.active--; a.mu.Unlock(); a.reads.Done() }()
	response := map[string]any{"kind": "resource.result", "request": m.Request}
	err := func() error {
		if err := a.ctx.Err(); err != nil {
			return err
		}
		u, err := url.Parse(m.URL)
		if err != nil {
			return err
		}
		if u.Scheme != "http" || u.User != nil || u.Host != strings.TrimPrefix(a.origin, "http://") || m.Offset < 0 || m.Limit < 0 || m.Limit > 64<<10 {
			return errors.New("CEF_ARTIFACT_RESOURCE_DENIED")
		}
		rel := strings.TrimPrefix(u.Path, "/")
		if rel == "" || strings.Contains(rel, "\\") {
			return errors.New("CEF_ARTIFACT_PATH_REJECTED")
		}
		f, err := a.root.Open(filepath.FromSlash(rel))
		if err != nil {
			return err
		}
		defer f.Close()
		stop := context.AfterFunc(a.ctx, func() { f.Close() })
		defer stop()
		info, err := f.Stat()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("CEF_ARTIFACT_NOT_FILE")
		}
		response["size"] = strconv.FormatInt(info.Size(), 10)
		mediaType, _, _ := mime.ParseMediaType(mime.TypeByExtension(filepath.Ext(rel)))
		response["mime"] = mediaType
		if m.Limit > 0 {
			if a.broker.observe != nil {
				a.broker.observe("artifact", "read-chunk")
			}
			data := make([]byte, m.Limit)
			n, err := f.ReadAt(data, m.Offset)
			if err != nil && err != io.EOF {
				return err
			}
			response["data"] = data[:n]
			a.mu.Lock()
			a.stats.ReadBytes += int64(n)
			a.mu.Unlock()
		}
		return a.ctx.Err()
	}()
	if err != nil {
		response = map[string]any{"kind": "resource.result", "request": m.Request, "error": "CEF_ARTIFACT_RESOURCE_DENIED"}
	}
	_ = a.broker.write(response)
}
