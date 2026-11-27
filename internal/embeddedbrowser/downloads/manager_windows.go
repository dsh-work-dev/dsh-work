//go:build windows

package downloads

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	win "golang.org/x/sys/windows"
)

type Policy struct {
	MaxBytes      int64
	MinFreeBytes  uint64
	MaxConcurrent int
}
type Pending struct {
	ID, Generation, Owner, Profile, RequestID, FrameID, Document, Presentation string
	SourceOrigin, SourceURL, URL, Method, MIME, SuggestedName                  string
	TotalBytes                                                                 int64
	HasBody                                                                    bool
}
type Selection struct {
	Path                              string
	OverwriteConfirmed, RiskConfirmed bool
	// Prepared is captured before the Host's object-specific overwrite decision.
	Prepared *Destination
}
type Snapshot struct {
	Owner           string `json:"-"`
	ID              string `json:"id"`
	State           string `json:"state"`
	SourceOrigin    string `json:"sourceOrigin"`
	TargetOrigin    string `json:"targetOrigin"`
	Filename        string `json:"filename"`
	Received        int64  `json:"received"`
	Total           int64  `json:"total"`
	Error           string `json:"error,omitempty"`
	RecoveryPath    string `json:"recoveryPath,omitempty"`
	CleanupRequired bool   `json:"cleanupRequired,omitempty"`
}
type entry struct {
	Version         int        `json:"version"`
	ID              string     `json:"id"`
	State           string     `json:"state"`
	SourceOrigin    string     `json:"sourceOrigin"`
	TargetOrigin    string     `json:"targetOrigin"`
	File            FileRecord `json:"file"`
	Received, Total int64
	Error           string    `json:"error,omitempty"`
	CleanupRequired bool      `json:"cleanupRequired,omitempty"`
	Updated         time.Time `json:"updated"`
}
type transfer struct {
	dirty         bool
	entry         entry
	pending       Pending // Full URLs are in memory only; never replayed from journal.
	destination   *Destination
	cancel        func(context.Context) error
	riskConfirmed bool
	maxBytes      int64
	writerIssued  bool
}
type Manager struct {
	mu        sync.Mutex
	directory string
	policy    Policy
	pending   map[string]Pending
	transfers map[string]*transfer
	notify    func(Snapshot)
}

// OpenManager requires the caller's existing exclusive profile lease and a
// drained previous broker. It uses the app's profile ownership, not a new lock
// framework. Startup recovery never starts a request or restores an old grant.
func OpenManager(directory string, policy Policy, notify func(Snapshot)) (*Manager, error) {
	if !filepath.IsAbs(directory) || policy.MaxBytes <= 0 || policy.MaxConcurrent <= 0 {
		return nil, errors.New("DOWNLOAD_CONFIG_REQUIRED")
	}
	if e := os.MkdirAll(directory, 0700); e != nil {
		return nil, e
	}
	m := &Manager{directory: directory, policy: policy, pending: map[string]Pending{}, transfers: map[string]*transfer{}, notify: notify}
	files, e := os.ReadDir(directory)
	if e != nil {
		return nil, e
	}
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(f.Name(), ".json")
		if !validID(id) {
			return nil, errors.New("DOWNLOAD_LEDGER_INVALID")
		}
		data, e := os.ReadFile(filepath.Join(directory, f.Name()))
		if e != nil {
			return nil, e
		}
		var r entry
		if json.Unmarshal(data, &r) != nil || r.Version != 1 || r.ID != id {
			return nil, errors.New("DOWNLOAD_LEDGER_INVALID")
		}
		t := &transfer{entry: r}
		m.transfers[id] = t
		// Terminal records without recovery need no filesystem access.
		if terminal(r.State) && r.File.Recovery == "" && (r.File.Published || r.File.Phase == "discarded") {
			continue
		}
		recovered, re := RecoverFile(r.File)
		t.entry.File = recovered
		t.entry.CleanupRequired = re != nil
		if recovered.Published {
			t.entry.State = "complete"
			if re != nil {
				t.entry.Error = "DOWNLOAD_RECOVERY_REQUIRED"
			}
		} else {
			t.entry.State = "interrupted"
			if re != nil {
				t.entry.Error = "DOWNLOAD_RECOVERY_REQUIRED"
			}
		}
		if e = m.save(t); e != nil {
			return nil, e
		}
		if re == nil && recovered.Published && recovered.Recovery != "" {
			cleaned, ce := CleanupPublishedRecovery(recovered)
			t.entry.File = cleaned
			if ce != nil {
				t.entry.Error = "DOWNLOAD_RECOVERY_REQUIRED"
			}
			if e = m.save(t); e != nil {
				return nil, e
			}
		}
	}
	return m, nil
}
func validID(id string) bool {
	if len(id) != 26 {
		return false
	}
	for _, r := range id {
		if !(r >= 'A' && r <= 'Z' || r >= '2' && r <= '7') {
			return false
		}
	}
	return true
}
func origin(address string) (string, error) {
	u, e := url.Parse(address)
	if e != nil || u.User != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", ErrApprovalExpired
	}
	return strings.ToLower(u.Scheme + "://" + u.Host), nil
}
func terminal(s string) bool {
	return s == "complete" || s == "failed" || s == "canceled" || s == "revoked" || s == "interrupted"
}
func (m *Manager) Offer(p Pending) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p.ID == "" || p.Generation == "" || p.Owner == "" || p.RequestID == "" || p.FrameID == "" || p.Document == "" || p.Profile == "" || p.Method != "GET" || p.HasBody {
		return ErrApprovalExpired
	}
	if _, e := origin(p.URL); e != nil {
		return e
	}
	if o, e := origin(p.SourceURL); e != nil || o != p.SourceOrigin {
		return ErrApprovalExpired
	}
	if _, exists := m.pending[p.ID]; exists {
		return ErrApprovalExpired
	}
	p.SuggestedName = SuggestedName(p.SuggestedName)
	m.pending[p.ID] = p
	return nil
}
func (m *Manager) Pending(id string) (Pending, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.pending[id]
	return p, ok
}
func (m *Manager) Dismiss(id string) {
	m.mu.Lock()
	delete(m.pending, id)
	m.mu.Unlock()
}
func (m *Manager) Invalidate(owner, document, presentation string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, p := range m.pending {
		if p.Owner == owner && (p.Document != document || p.Presentation != presentation) {
			delete(m.pending, id)
		}
	}
}

// Approve is Host-only. stillCurrent must query/verify the native pending,
// generation and document again; event strings alone are not authority.
func (m *Manager) Approve(ctx context.Context, id string, choice Selection, stillCurrent func(context.Context, Pending) bool) (string, error) {
	m.mu.Lock()
	p, ok := m.pending[id]
	m.mu.Unlock()
	if !ok || stillCurrent == nil || ctx.Err() != nil || !stillCurrent(ctx, p) {
		return "", ErrApprovalExpired
	}
	d := choice.Prepared
	var e error
	if d == nil {
		d, e = PrepareDestination(choice.Path, false)
	}
	if e != nil {
		return "", e
	}
	accepted := false
	defer func() {
		if !accepted {
			_ = d.Abort()
			_ = d.Close()
		}
	}()
	selected := d.Record()
	if selected.Previous != nil && !choice.OverwriteConfirmed {
		return "", ErrApprovalExpired
	}
	if RiskyName(selected.Name, p.MIME) && !choice.RiskConfirmed {
		return "", ErrRisk
	}
	if e = checkFree(d.Record().Parent, m.policy.MinFreeBytes, p.TotalBytes); e != nil {
		return "", e
	}
	if p.TotalBytes > m.policy.MaxBytes {
		return "", ErrBudget
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current, ok := m.pending[id]
	if !ok || current != p || ctx.Err() != nil {
		return "", ErrApprovalExpired
	}
	count := 0
	for _, t := range m.transfers {
		if !terminal(t.entry.State) {
			count++
		}
	}
	if count >= m.policy.MaxConcurrent {
		return "", errors.New("DOWNLOAD_CONCURRENCY_LIMIT")
	}
	grant := rand.Text()
	target, _ := origin(p.URL)
	t := &transfer{entry: entry{Version: 1, ID: grant, State: "approved", SourceOrigin: p.SourceOrigin, TargetOrigin: target, File: d.Record(), Total: p.TotalBytes}, pending: p, destination: d, riskConfirmed: choice.RiskConfirmed, maxBytes: m.policy.MaxBytes}
	if e = m.save(t); e != nil {
		return "", e
	}
	delete(m.pending, id)
	m.transfers[grant] = t
	accepted = true
	return grant, nil
}
func (m *Manager) StartData(id string) (Pending, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.transfers[id]
	if t == nil || t.entry.State != "approved" {
		return Pending{}, 0, ErrApprovalExpired
	}
	return t.pending, t.maxBytes, nil
}
func (m *Manager) DuplicateWriter(id string, brokerProcess win.Handle) (uintptr, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.transfers[id]
	if t == nil || t.entry.State != "starting" || t.destination == nil || t.writerIssued {
		return 0, ErrApprovalExpired
	}
	h, err := t.destination.DuplicateWriter(brokerProcess)
	if err == nil {
		t.writerIssued = true
	}
	return h, err
}

// Arm installs cancellation before any remote writer capability can exist.
func (m *Manager) Arm(id string, cancel func(context.Context) error) error {
	defer m.emit(id)
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.transfers[id]
	if t == nil || t.entry.State != "approved" || cancel == nil {
		return ErrApprovalExpired
	}
	t.cancel = cancel
	t.entry.State = "starting"
	return m.save(t)
}
func (m *Manager) Running(id string) error {
	defer m.emit(id)
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.transfers[id]
	if t == nil || t.entry.State != "starting" || !t.writerIssued {
		return ErrApprovalExpired
	}
	t.entry.State = "transferring"
	return m.save(t)
}
func (m *Manager) Progress(id string, received, total int64) error {
	m.mu.Lock()
	t := m.transfers[id]
	if t == nil || t.entry.State != "transferring" {
		m.mu.Unlock()
		return ErrApprovalExpired
	}
	if received < t.entry.Received || total < 0 {
		m.mu.Unlock()
		return errors.New("DOWNLOAD_PROGRESS_INVALID")
	}
	if received > t.maxBytes {
		m.mu.Unlock()
		return ErrBudget
	}
	t.entry.Received = received
	t.entry.Total = total
	s := snapshot(t)
	notify := m.notify
	m.mu.Unlock()
	if notify != nil {
		notify(s)
	}
	return nil
}
func snapshot(t *transfer) Snapshot {
	s := Snapshot{Owner: t.pending.Owner, ID: t.entry.ID, State: t.entry.State, SourceOrigin: t.entry.SourceOrigin, TargetOrigin: t.entry.TargetOrigin, Filename: t.entry.File.Name, Received: t.entry.Received, Total: t.entry.Total, Error: t.entry.Error}
	s.CleanupRequired = t.entry.CleanupRequired
	if t.entry.File.Recovery != "" {
		s.RecoveryPath = filepath.Join(t.entry.File.Parent, t.entry.File.Recovery)
	}
	return s
}
func (m *Manager) Snapshots() []Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Snapshot, 0, len(m.transfers))
	for _, t := range m.transfers {
		out = append(out, snapshot(t))
	}
	return out
}

// RequestCancel invalidates final publication before the caller dismisses a
// risk dialog and joins its finalizer. It does not close a file still in use.
// Cancel must follow once that owner has joined to stop/drain the writer.
func (m *Manager) RequestCancel(id string, revoke bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.transfers[id]
	if t == nil {
		return ErrApprovalExpired
	}
	if terminal(t.entry.State) {
		return nil
	}
	t.entry.State = "stopping"
	t.entry.Error = "canceled"
	if revoke {
		t.entry.Error = "revoked"
	}
	return m.save(t)
}
func (m *Manager) Cancel(ctx context.Context, id string, revoke bool) error {
	defer m.emit(id)
	m.mu.Lock()
	t := m.transfers[id]
	if t == nil {
		m.mu.Unlock()
		return ErrApprovalExpired
	}
	if terminal(t.entry.State) {
		m.mu.Unlock()
		return nil
	}
	wanted := "canceled"
	if revoke {
		wanted = "revoked"
	}
	t.entry.State = "stopping"
	t.entry.Error = wanted
	persistErr := m.save(t)
	cancel := t.cancel
	issued := t.writerIssued
	m.mu.Unlock()
	if cancel != nil && issued {
		stopContext, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer stop()
		return errors.Join(persistErr, cancel(stopContext))
	}
	// No native request was started. The caller must not start from a stopping grant.
	return errors.Join(persistErr, m.Drained(id, "", 0))
}

// Drained is called only after the authenticated broker confirms all request
// and writer callbacks/handles stopped, or its whole process tree has joined.
func (m *Manager) Drained(id string, failure string, received int64) error {
	defer m.emit(id)
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.transfers[id]
	if t == nil {
		return ErrApprovalExpired
	}
	if terminal(t.entry.State) {
		return nil
	}
	if received < t.entry.Received {
		return errors.New("DOWNLOAD_PROGRESS_INVALID")
	}
	t.entry.Received = received
	if t.entry.State == "stopping" {
		t.entry.State = t.entry.Error
		t.entry.Error = ""
	} else {
		t.entry.State = "failed"
		t.entry.Error = failure
		if failure == "" {
			t.entry.Error = "DOWNLOAD_FAILED"
		}
	}
	if t.destination != nil {
		e := t.destination.Abort()
		ce := t.destination.Close()
		t.destination = nil
		if e != nil || ce != nil {
			t.entry.Error = "DOWNLOAD_CLEANUP_REQUIRED"
			t.entry.CleanupRequired = true
			_ = m.save(t)
			return errors.Join(e, ce)
		}
		t.entry.File.Phase = "discarded"
	}
	return m.save(t)
}

// Complete is invoked after successful CEF completion and writer drain. The
// callback may request an additional content-based risk confirmation.
func (m *Manager) Complete(id string, confirmUnexpectedRisk func(Snapshot) bool) error {
	defer m.emit(id)
	m.mu.Lock()
	t := m.transfers[id]
	if t == nil || t.entry.State != "transferring" || t.destination == nil {
		m.mu.Unlock()
		return ErrApprovalExpired
	}
	t.entry.State = "finalizing"
	if e := m.save(t); e != nil {
		m.mu.Unlock()
		return e
	}
	destination, pending, riskConfirmed, s := t.destination, t.pending, t.riskConfirmed, snapshot(t)
	m.mu.Unlock()
	// No manager mutex is held across native UI or Attachment Services. A
	// cancellation can invalidate finalizing while those operations drain.
	var finalizationError error
	if !riskConfirmed {
		mime, e := destination.ContentType()
		if e != nil {
			finalizationError = e
		}
		if e == nil && RiskyName(s.Filename, mime) && (confirmUnexpectedRisk == nil || !confirmUnexpectedRisk(s)) {
			finalizationError = errors.New("DOWNLOAD_RISK_DECLINED")
		}
	}
	if finalizationError == nil {
		finalizationError = destination.CheckRisk(pending.URL, pending.SourceURL)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if t.entry.State != "finalizing" || t.destination != destination {
		return ErrApprovalExpired
	}
	if finalizationError != nil {
		return errors.Join(finalizationError, m.failLocked(t, "DOWNLOAD_FINALIZATION_FAILED"))
	}
	e := t.destination.Publish(func(r FileRecord) error { t.entry.File = r; return m.save(t) })
	t.entry.File = t.destination.Record()
	if t.entry.File.Published {
		t.entry.State = "complete"
	} else {
		t.entry.State = "failed"
	}
	if e != nil {
		t.entry.Error = "DOWNLOAD_PUBLISH_FAILED"
		switch {
		case errors.Is(e, ErrDestinationChanged):
			t.entry.Error = ErrDestinationChanged.Error()
		case errors.Is(e, ErrDestinationBusy):
			t.entry.Error = ErrDestinationBusy.Error()
		case errors.Is(e, ErrOwnedFileChanged):
			t.entry.Error = ErrOwnedFileChanged.Error()
		}
		if t.entry.File.Recovery != "" {
			t.entry.Error = "DOWNLOAD_RECOVERY_REQUIRED"
		}
	}
	var ae error
	if !t.entry.File.Published {
		ae = t.destination.Abort()
	}
	ce := t.destination.Close()
	t.destination = nil
	if !t.entry.File.Published {
		t.entry.CleanupRequired = ae != nil || ce != nil
	}
	if !t.entry.File.Published && ae == nil && ce == nil {
		t.entry.File.Phase = "discarded"
	}
	return errors.Join(e, ae, ce, m.save(t))
}
func (m *Manager) failLocked(t *transfer, code string) error {
	t.entry.State = "failed"
	t.entry.Error = code
	e := t.destination.Abort()
	ce := t.destination.Close()
	t.destination = nil
	t.entry.CleanupRequired = e != nil || ce != nil
	if e == nil && ce == nil {
		t.entry.File.Phase = "discarded"
	}
	return errors.Join(errors.New(code), e, ce, m.save(t))
}

func (m *Manager) emit(id string) {
	m.mu.Lock()
	t, notify := m.transfers[id], m.notify
	if t == nil || notify == nil {
		m.mu.Unlock()
		return
	}
	s := snapshot(t)
	m.mu.Unlock()
	notify(s)
}

// BrokerFailed is called after the existing supervisor confirms drain/join.
// It leaves successful files intact and never starts a retry.
func (m *Manager) BrokerFailed(generation string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var errs []error
	for _, t := range m.transfers {
		if t.pending.Generation != generation || terminal(t.entry.State) {
			continue
		}
		t.entry.State = "interrupted"
		t.entry.Error = "DOWNLOAD_BROKER_INTERRUPTED"
		if t.destination != nil {
			ae, ce := t.destination.Abort(), t.destination.Close()
			errs = append(errs, ae, ce)
			if ae == nil && ce == nil {
				t.entry.File.Phase = "discarded"
			}
			t.destination = nil
		}
		errs = append(errs, m.save(t))
	}
	for id, p := range m.pending {
		if p.Generation == generation {
			delete(m.pending, id)
		}
	}
	return errors.Join(errs...)
}

// ReadyForShutdown never treats a terminal in-memory state as a durable
// transaction. Retry a failed terminal journal write before releasing owners.
func (m *Manager) ReadyForShutdown() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.pending) != 0 {
		return errors.New("DOWNLOAD_PENDING")
	}
	for _, t := range m.transfers {
		if !terminal(t.entry.State) || t.destination != nil {
			return errors.New("DOWNLOAD_ACTIVE")
		}
		if t.dirty {
			if err := m.save(t); err != nil {
				return err
			}
		}
		if t.entry.CleanupRequired {
			// This terminal transaction has no writer/finalizer/destination owner.
			// Reuse the same identity-checked recovery as startup under our still
			// held profile lease, rather than requiring a broker restart to retry.
			recovered, err := RecoverFile(t.entry.File)
			t.entry.File = recovered
			t.entry.CleanupRequired = err != nil
			if saveErr := m.save(t); err != nil || saveErr != nil {
				return errors.Join(err, saveErr)
			}
			if recovered.Published && recovered.Recovery != "" {
				cleaned, err := CleanupPublishedRecovery(recovered)
				t.entry.File = cleaned
				t.entry.CleanupRequired = err != nil
				if saveErr := m.save(t); err != nil || saveErr != nil {
					return errors.Join(err, saveErr)
				}
			}
		}
	}
	return nil
}
func (m *Manager) save(t *transfer) (err error) {
	defer func() { t.dirty = err != nil }()
	t.entry.Updated = time.Now().UTC()
	data, e := json.Marshal(t.entry)
	if e != nil {
		return e
	}
	file, e := os.CreateTemp(m.directory, ".download-journal-*.tmp")
	if e != nil {
		return e
	}
	name := file.Name()
	defer os.Remove(name)
	if _, e = file.Write(data); e == nil {
		e = file.Sync()
	}
	e = errors.Join(e, file.Close())
	if e != nil {
		return e
	}
	from, e := win.UTF16PtrFromString(name)
	if e != nil {
		return e
	}
	to, e := win.UTF16PtrFromString(filepath.Join(m.directory, t.entry.ID+".json"))
	if e != nil {
		return e
	}
	return win.MoveFileEx(from, to, win.MOVEFILE_REPLACE_EXISTING|win.MOVEFILE_WRITE_THROUGH)
}
func checkFree(parent string, reserve uint64, total int64) error {
	p, e := win.UTF16PtrFromString(parent)
	if e != nil {
		return e
	}
	var free uint64
	if e = win.GetDiskFreeSpaceEx(p, &free, nil, nil); e != nil {
		return e
	}
	need := reserve
	if total > 0 {
		if uint64(total) > ^uint64(0)-need {
			return ErrBudget
		}
		need += uint64(total)
	}
	if free < need {
		return fmt.Errorf("DOWNLOAD_DISK_SPACE_REQUIRED: %d", need)
	}
	return nil
}
