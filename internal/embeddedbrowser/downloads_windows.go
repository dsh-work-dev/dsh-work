//go:build windows

package embeddedbrowser

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/local/dsh-work/internal/embeddedbrowser/downloads"
	win "golang.org/x/sys/windows"
)

type liveDownload struct {
	owner       string
	writer      uintptr
	ready       chan struct{}
	events      *downloadMailbox
	received    int64
	dialogOwner downloads.DialogOwner
}
type downloadPrompt struct {
	owner  string
	cancel context.CancelFunc
}
type windowsDownloads struct {
	broker       *Broker
	manager      *downloads.Manager
	process      win.Handle
	profile      string
	consent      DownloadConsent
	mu           sync.Mutex
	prompts      map[string]downloadPrompt
	active       map[string]*liveDownload
	promptSerial sync.Mutex
	tasks        sync.WaitGroup
	closeWait    chan struct{}
	closeOnce    sync.Once
	closed       bool
	life         context.Context
	stop         context.CancelFunc
}

// Candidate budgets are explicit product proposals, not R85 user-approved
// limits. They bound accidental unending streams, not supported file types.
var proposedDownloadPolicy = downloads.Policy{MaxBytes: 2 << 30, MinFreeBytes: 512 << 20, MaxConcurrent: 2}

func newBrokerDownloads(b *Broker, c Config) (brokerDownloads, error) {
	process := b.worker.Diagnostics()
	h, e := win.OpenProcess(win.PROCESS_DUP_HANDLE|win.PROCESS_QUERY_LIMITED_INFORMATION|win.SYNCHRONIZE, false, uint32(process.PID))
	if e != nil {
		return nil, e
	}
	var created, exit, kernel, user win.Filetime
	if e = win.GetProcessTimes(h, &created, &exit, &kernel, &user); e != nil || uint64(created.HighDateTime)<<32|uint64(created.LowDateTime) != process.ProcessStarted {
		win.CloseHandle(h)
		return nil, errors.New("DOWNLOAD_BROKER_IDENTITY_REJECTED")
	}
	manager, e := downloads.OpenManager(filepath.Join(c.Profile, "dsh-downloads"), proposedDownloadPolicy, nil)
	if e != nil {
		win.CloseHandle(h)
		return nil, e
	}
	life, stop := context.WithCancel(context.Background())
	return &windowsDownloads{life: life, stop: stop, broker: b, manager: manager, process: h, profile: c.Profile, consent: c.DownloadConsent, prompts: map[string]downloadPrompt{}, active: map[string]*liveDownload{}, closeWait: make(chan struct{})}, nil
}
func (c *windowsDownloads) state() []DownloadView {
	snapshots := c.manager.Snapshots()
	out := make([]DownloadView, 0, len(snapshots))
	for _, s := range snapshots {
		out = append(out, DownloadView{owner: s.Owner, ID: s.ID, State: s.State, SourceOrigin: s.SourceOrigin, TargetOrigin: s.TargetOrigin, Filename: s.Filename, Received: s.Received, Total: s.Total, Error: s.Error, RecoveryPath: s.RecoveryPath, CleanupRequired: s.CleanupRequired})
	}
	return out
}
func (c *windowsDownloads) valid(ctx context.Context, p downloads.Pending) bool {
	if ctx.Err() != nil || p.Generation != c.broker.generation {
		return false
	}
	_, e := c.broker.controlCall(ctx, "target.download.pending", p.Owner, map[string]any{"pending": p.ID})
	return e == nil
}
func (c *windowsDownloads) dismiss(owner, pending string) {
	ctx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	_, _ = c.broker.controlCall(ctx, "target.download.dismiss", owner, map[string]any{"pending": pending})
}
func (c *windowsDownloads) message(raw json.RawMessage) error {
	var e struct {
		Kind, Generation, Owner, Pending, RequestID, FrameID, Document, Presentation, Source, SourceOrigin, URL, MIME, Name, Total, Parent, Started, ID, Received, Error string
		PID                                                                                                                                                              uint32
		Success                                                                                                                                                          bool
	}
	if json.Unmarshal(raw, &e) != nil || e.Generation != c.broker.generation {
		return errors.New("DOWNLOAD_PROTOCOL_REJECTED")
	}
	switch e.Kind {
	case "download.pending":
		parent, err := strconv.ParseUint(e.Parent, 10, 64)
		if err != nil {
			return errors.New("DOWNLOAD_OWNER_REJECTED")
		}
		started, err := strconv.ParseUint(e.Started, 10, 64)
		if err != nil {
			return errors.New("DOWNLOAD_OWNER_REJECTED")
		}
		total, err := strconv.ParseInt(e.Total, 10, 64)
		if err != nil {
			return errors.New("DOWNLOAD_PROTOCOL_REJECTED")
		}
		c.broker.mu.Lock()
		owned := len(c.broker.targets[e.Owner]) > 0
		c.broker.mu.Unlock()
		if !owned {
			return errors.New("DOWNLOAD_OWNER_REJECTED")
		}
		p := downloads.Pending{ID: e.Pending, Generation: e.Generation, Owner: e.Owner, Profile: c.profile, RequestID: e.RequestID, FrameID: e.FrameID, Document: e.Document, Presentation: e.Presentation, SourceOrigin: e.SourceOrigin, SourceURL: e.Source, URL: e.URL, Method: "GET", MIME: e.MIME, SuggestedName: e.Name, TotalBytes: total}
		if err = c.manager.Offer(p); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			cancel()
			return nil
		}
		c.prompts[p.ID] = downloadPrompt{p.Owner, cancel}
		c.tasks.Add(1)
		c.mu.Unlock()
		owner := downloads.DialogOwner{Window: uintptr(parent), PID: e.PID, Started: started}
		go c.offer(ctx, cancel, p, owner)
	case "download.invalidated":
		c.manager.Invalidate(e.Owner, "", "")
		c.mu.Lock()
		for _, p := range c.prompts {
			if p.owner == e.Owner {
				p.cancel()
			}
		}
		c.mu.Unlock()
	case "download.progress", "download.finished":
		received, err := strconv.ParseInt(e.Received, 10, 64)
		if err != nil || received < 0 {
			return errors.New("DOWNLOAD_PROGRESS_REJECTED")
		}
		total, err := strconv.ParseInt(e.Total, 10, 64)
		if err != nil || total < 0 {
			return errors.New("DOWNLOAD_PROGRESS_REJECTED")
		}
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			return nil
		}
		live := c.active[e.ID]
		if live != nil {
			live.received = received
		}
		if live == nil {
			c.mu.Unlock()
			return nil
		}
		c.mu.Unlock()
		return live.events.push(downloadEvent{received: received, total: total, finished: e.Kind == "download.finished", success: e.Success, failure: e.Error})

	default:
		return errors.New("DOWNLOAD_PROTOCOL_REJECTED")
	}
	return nil
}
func (c *windowsDownloads) offer(ctx context.Context, cancel context.CancelFunc, p downloads.Pending, owner downloads.DialogOwner) {
	defer c.tasks.Done()
	defer cancel()
	defer c.dismiss(p.Owner, p.ID)
	defer func() { c.mu.Lock(); delete(c.prompts, p.ID); c.mu.Unlock() }()
	c.promptSerial.Lock()
	defer c.promptSerial.Unlock()
	if ctx.Err() != nil {
		return
	}
	var choice downloads.Selection
	var e error
	if c.consent == nil {
		choice, e = downloads.ChooseDestination(ctx, owner, p, c.valid)
	} else {
		decision, err := c.consent(ctx, DownloadConsentRequest{ID: p.ID, SourceOrigin: p.SourceOrigin, URL: p.URL, MIME: p.MIME, SuggestedName: p.SuggestedName})
		e = err
		if prepared, ok := decision.Prepared.(*downloads.Destination); ok {
			r := prepared.Record()
			choice = downloads.Selection{Path: filepath.Join(r.Parent, r.Name), Prepared: prepared, OverwriteConfirmed: decision.OverwriteConfirmed, RiskConfirmed: decision.RiskConfirmed}
		} else if err == nil {
			return
		}
	}
	if e != nil || choice.Path == "" {
		return
	}
	grant, e := c.manager.Approve(ctx, p.ID, choice, c.valid)
	if e != nil {
		if choice.Prepared != nil {
			_ = choice.Prepared.Abort()
			_ = choice.Prepared.Close()
		}
		return
	}
	live := &liveDownload{owner: p.Owner, ready: make(chan struct{}), events: newDownloadMailbox(), dialogOwner: owner}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		_ = c.manager.Cancel(context.Background(), grant, false)
		return
	}
	c.active[grant] = live
	c.tasks.Add(1)
	c.mu.Unlock()
	go func() {
		defer c.tasks.Done()
		live.events.run(c.life.Done(), live.ready, func(e downloadEvent) { c.consume(grant, live, e) })
	}()
	defer close(live.ready)
	if e = c.manager.Arm(grant, func(stopCtx context.Context) error { return c.stopNative(stopCtx, grant) }); e != nil {
		_ = c.manager.Cancel(context.Background(), grant, false)
		return
	}
	writer, e := c.manager.DuplicateWriter(grant, c.process)
	if e != nil {
		_ = c.manager.Cancel(context.Background(), grant, false)
		return
	}
	c.mu.Lock()
	live.writer = writer
	c.mu.Unlock()
	// Native consumes the one-shot pending only if its current source document,
	// visible presentation, owner kernel identity and generation still match.
	r := choice.Prepared.Record()
	_, e = c.broker.controlCall(ctx, "target.download.start", p.Owner, map[string]any{"id": grant, "pending": p.ID, "writer": strconv.FormatUint(uint64(writer), 10), "maxBytes": strconv.FormatInt(proposedDownloadPolicy.MaxBytes, 10), "minFreeBytes": strconv.FormatUint(proposedDownloadPolicy.MinFreeBytes, 10), "volume": r.Parent})
	if e != nil {
		_ = c.manager.Cancel(context.Background(), grant, false)
		return
	}
	if e = c.manager.Running(grant); e != nil {
		_ = c.manager.Cancel(context.Background(), grant, false)
	}
}
func (c *windowsDownloads) stopNative(ctx context.Context, id string) error {
	c.mu.Lock()
	live := c.active[id]
	var writer uintptr
	var received int64
	if live != nil {
		writer = live.writer
		received = live.received
	}
	c.mu.Unlock()
	raw, e := c.broker.controlCall(ctx, "download.cancel", "", map[string]any{"id": id, "writer": strconv.FormatUint(uint64(writer), 10)})
	if e != nil {
		return e
	}
	var state struct{ Drained bool }
	if json.Unmarshal(raw, &state) != nil {
		return errors.New("DOWNLOAD_CANCEL_REJECTED")
	}
	if state.Drained {
		return c.manager.Drained(id, "", received)
	}
	return nil
}
func (c *windowsDownloads) cancel(ctx context.Context, id string, revoke bool) error {
	return c.manager.Cancel(ctx, id, revoke)
}
func (c *windowsDownloads) afterJoin() error {
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.closed = true
		c.stop()
		for _, p := range c.prompts {
			p.cancel()
		}
		c.mu.Unlock()
		go func() { c.tasks.Wait(); close(c.closeWait) }()
	})
	select {
	case <-c.closeWait:
		if e := c.manager.BrokerFailed(c.broker.generation); e != nil {
			return e
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.process != 0 {
			e := win.CloseHandle(c.process)
			c.process = 0
			return e
		}
		return nil
	case <-time.After(5 * time.Second):
		return errors.New("DOWNLOAD_UI_DRAIN_TIMEOUT")
	}
}
func (c *windowsDownloads) pending() bool {
	c.mu.Lock()
	closed := c.closed
	process := c.process
	c.mu.Unlock()
	return !closed || process != 0
}

func (c *windowsDownloads) consume(id string, live *liveDownload, e downloadEvent) {
	if !e.finished {
		if err := c.manager.Progress(id, e.received, e.total); err != nil && !errors.Is(err, downloads.ErrApprovalExpired) {
			_ = c.cancel(context.Background(), id, false)
		}
		return
	}
	if !e.success {
		_ = c.manager.Drained(id, e.failure, e.received)
		return
	}
	if err := c.manager.Progress(id, e.received, e.total); err != nil {
		if !errors.Is(err, downloads.ErrApprovalExpired) {
			_ = c.cancel(context.Background(), id, false)
		}
		_ = c.manager.Drained(id, "", e.received)
		return
	}
	err := c.manager.Complete(id, func(s downloads.Snapshot) bool { return downloads.ConfirmContentRisk(c.life, live.dialogOwner, s) })
	if errors.Is(err, downloads.ErrApprovalExpired) {
		_ = c.manager.Drained(id, "", e.received)
	} else if err != nil {
		_ = c.cancel(context.Background(), id, false)
	}
}
