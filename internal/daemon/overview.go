package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/local/dsh-work/internal/dshmanager"
	"github.com/local/dsh-work/internal/lifecycle"
	"github.com/local/dsh-work/internal/overviewpc"
	"github.com/local/dsh-work/internal/settings"
)

type OverviewEvent struct {
	ID         string `json:"id,omitempty"`
	Kind       string `json:"kind,omitempty"`
	At         string `json:"at"`
	State      string `json:"state"`
	Generation string `json:"generation"`
}

// RecordEvent receives one terminal operation event from trusted composition.
// It deliberately has no context argument and is not a daemon RPC method.
func (s *OverviewService) RecordEvent(event OverviewEvent) {
	if event.ID == "" || event.Kind == "" {
		return
	}
	if _, err := time.Parse(time.RFC3339Nano, event.At); err != nil {
		return
	}
	s.mu.Lock()
	s.loadJournalLocked()
	for _, known := range s.events {
		if known.ID == event.ID {
			s.mu.Unlock()
			return
		}
	}
	s.appendEventLocked(event)
	pending := s.journalPendingLocked()
	s.mu.Unlock()
	s.persistJournal(pending)
}

func (s *OverviewService) appendEventLocked(event OverviewEvent) {
	s.events = append(s.events, event)
	if len(s.events) > 2048 {
		s.events = s.events[len(s.events)-2048:]
	}
}

type OverviewSnapshot struct {
	DSHPayload            string               `json:"dshPayload"`
	StaleAt               string               `json:"staleAt,omitempty"`
	DSHError              string               `json:"dshError,omitempty"`
	ProfileKey            string               `json:"profileKey"`
	UptimeSeconds         *float64             `json:"uptimeSeconds,omitempty"`
	Events                []OverviewEvent      `json:"events"`
	Computer              *overviewpc.Snapshot `json:"computer,omitempty"`
	AutomaticRecoveries   uint64               `json:"automaticRecoveries"`
	RecoveryTrackingSince string               `json:"recoveryTrackingSince"`
	PersistenceErrors     map[string]string    `json:"persistenceErrors,omitempty"`
}
type overviewCache struct {
	At      time.Time       `json:"at"`
	Payload json.RawMessage `json:"payload"`
}

func readOverviewCache(path string) (map[string]overviewCache, error) {
	data, err := readOverviewFile(path, overviewCacheReadLimit)
	if err != nil {
		return nil, err
	}
	var cache map[string]overviewCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil, err
	}
	if cache == nil || len(cache) > overviewCacheEntries {
		return nil, errors.New("invalid overview cache")
	}
	for key, entry := range cache {
		// Earlier builds stored the payload as a JSON-encoded string.
		if len(entry.Payload) > 0 && entry.Payload[0] == '"' {
			var payload string
			if err := json.Unmarshal(entry.Payload, &payload); err != nil {
				return nil, err
			}
			entry.Payload = json.RawMessage(payload)
			cache[key] = entry
		}
		if entry.At.IsZero() || len(entry.Payload) > overviewPayloadLimit || !json.Valid(entry.Payload) {
			return nil, errors.New("invalid overview cache entry")
		}
	}
	return cache, nil
}

// OverviewService exposes bounded, read-only module data to trusted workbench windows.
// Authentication stays on the daemon's generation-owned transport.
type OverviewService struct {
	PC             *overviewpc.Collector
	ProfileKey     func(context.Context) (string, error)
	Root           string
	Replacer       settings.FileReplacer
	RecoverySource func(context.Context) (*dshmanager.SwitchAttempt, error)
	// mu guards lifecycle and journal state. It is released before disk writes and network I/O.
	mu sync.Mutex
	// fetchGate serializes Worker collection and the response cache, with cancellation while waiting.
	fetchOnce sync.Once
	fetchGate chan struct{}
	// writeMu serializes journal files; journalSeq orders snapshots so an older one never overwrites a newer one.
	writeMu               sync.Mutex
	journalSeq            uint64
	journalWritten        uint64
	worker                *overviewWorker
	cache                 map[string]overviewCache
	events                []OverviewEvent
	lastState, generation string
	readyAt               time.Time
	journalLoaded         bool
	journal               overviewJournal
	persistenceErrors     map[string]string
}

// overviewWorker is one Worker generation's authenticated channel. Composition replaces it without
// waiting for a collection in flight; that collection keeps its own pointer and is cancelled with run.
type overviewWorker struct {
	run           context.Context
	client        *http.Client
	auth, origin  string
	generation    string
	authenticated bool
}

// The response cache keeps the latest payload for a few (profile, period, locale, day) keys.
// Payloads are at most 2 MiB, so the file stays under overviewCacheReadLimit.
const (
	overviewPayloadLimit   = 2 << 20
	overviewCacheEntries   = 4
	overviewCacheReadLimit = overviewCacheEntries*overviewPayloadLimit + 1<<20
)

type overviewJournal struct {
	Events              []OverviewEvent `json:"events"`
	TrackingSince       string          `json:"trackingSince"`
	AutomaticRecoveries uint64          `json:"automaticRecoveries"`
	RecoveryIDs         map[string]bool `json:"recoveryIds"`
	RecoveryBaseline    bool            `json:"recoveryBaseline"`
}

// Initialize establishes the recovery baseline before the Host starts a Worker.
// A previously retained switch attempt is not a newly observed recovery.
func (s *OverviewService) Initialize() {
	s.mu.Lock()
	s.loadJournalLocked()
	s.mu.Unlock()
	s.refreshRecovery(context.Background())
}

func (s *OverviewService) refreshRecovery(ctx context.Context) {
	if s.RecoverySource == nil {
		s.ObserveRecovery(nil)
		return
	}
	attempt, err := s.RecoverySource(ctx)
	s.mu.Lock()
	s.persistenceErrorLocked("recoverySource", err)
	s.mu.Unlock()
	if err == nil {
		s.ObserveRecovery(attempt)
	}
}

// ObserveRecovery counts only completed automatic rollback restorations. Its
// completedAt identity survives daemon restarts; ordinary restarts do not count.
func (s *OverviewService) ObserveRecovery(attempt *dshmanager.SwitchAttempt) {
	s.mu.Lock()
	save := s.observeRecoveryLocked(attempt)
	var pending journalPending
	if save {
		pending = s.journalPendingLocked()
	}
	s.mu.Unlock()
	if save {
		s.persistJournal(pending)
	}
}

// observeRecoveryLocked updates the journal and reports whether it must be written.
func (s *OverviewService) observeRecoveryLocked(attempt *dshmanager.SwitchAttempt) bool {
	s.loadJournalLocked()
	baseline := !s.journal.RecoveryBaseline
	s.journal.RecoveryBaseline = true
	if attempt != nil && attempt.AutomaticRollback && (attempt.Rollback == dshmanager.RollbackRestored || attempt.Rollback == dshmanager.RollbackFailed || attempt.Rollback == dshmanager.RollbackUnavailable) {
		completed, err := time.Parse(time.RFC3339Nano, attempt.CompletedAt)
		if err != nil {
			s.persistenceErrorLocked("recoveryRecord", errors.New("automatic recovery completion time is invalid"))
			return false
		}
		s.persistenceErrorLocked("recoveryRecord", nil)
		id := completed.UTC().Format(time.RFC3339Nano)
		if !s.journal.RecoveryIDs[id] {
			s.journal.RecoveryIDs[id] = true
			started, _ := time.Parse(time.RFC3339Nano, s.journal.TrackingSince)
			if !baseline && !completed.Before(started) {
				if attempt.Rollback == dshmanager.RollbackRestored {
					s.journal.AutomaticRecoveries++
				}
				s.appendEventLocked(OverviewEvent{ID: "automatic-recovery:" + id, At: id, Kind: "automatic-recovery", State: string(attempt.Rollback)})
			}
			return true
		}
	}
	return baseline || s.persistenceErrors["journalSave"] != ""
}

func (s *OverviewService) persistenceErrorLocked(module string, err error) {
	if s.persistenceErrors == nil {
		s.persistenceErrors = map[string]string{}
	}
	if err == nil {
		delete(s.persistenceErrors, module)
	} else {
		s.persistenceErrors[module] = err.Error()
	}
}

func (s *OverviewService) loadJournalLocked() {
	if s.journalLoaded {
		return
	}
	s.journalLoaded = true
	s.journal = overviewJournal{TrackingSince: time.Now().UTC().Truncate(time.Second).Format(time.RFC3339), RecoveryIDs: map[string]bool{}}
	if s.Root == "" {
		return
	}
	data, err := readOverviewFile(filepath.Join(s.Root, "overview-journal.json"), 2<<20)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err == nil {
		var loaded overviewJournal
		err = json.Unmarshal(data, &loaded)
		if err == nil {
			_, err = time.Parse(time.RFC3339Nano, loaded.TrackingSince)
			if err == nil {
				s.journal = loaded
				if s.journal.RecoveryIDs == nil {
					s.journal.RecoveryIDs = map[string]bool{}
				}
				if len(s.journal.Events) > 2048 {
					s.journal.Events = s.journal.Events[len(s.journal.Events)-2048:]
				}
				s.events = append([]OverviewEvent{}, s.journal.Events...)
			}
		}
	}
	s.persistenceErrorLocked("journalLoad", err)
}

func readOverviewFile(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err == nil && int64(len(data)) > limit {
		err = errors.New("overview record exceeds size limit")
	}
	return data, err
}

func (s *OverviewService) atomicWrite(name string, value any) error {
	if s.Root == "" {
		return nil
	}
	if s.Replacer == nil {
		return errors.New("overview atomic file replacement unavailable")
	}
	if err := os.MkdirAll(s.Root, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(s.Root, ".overview-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	encoder := json.NewEncoder(f)
	encoder.SetEscapeHTML(false)
	if err = encoder.Encode(value); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return s.Replacer.Replace(f.Name(), filepath.Join(s.Root, name))
}

type journalPending struct {
	journal overviewJournal
	seq     uint64
}

// journalPendingLocked captures the journal to write once mu is released.
func (s *OverviewService) journalPendingLocked() journalPending {
	s.journalSeq++
	journal := s.journal
	journal.Events = append([]OverviewEvent{}, s.events...)
	journal.RecoveryIDs = make(map[string]bool, len(s.journal.RecoveryIDs))
	for id, seen := range s.journal.RecoveryIDs {
		journal.RecoveryIDs[id] = seen
	}
	return journalPending{journal: journal, seq: s.journalSeq}
}

func (s *OverviewService) persistJournal(p journalPending) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if p.seq <= s.journalWritten {
		return
	}
	err := s.atomicWrite("overview-journal.json", p.journal)
	s.mu.Lock()
	s.persistenceErrorLocked("journalSave", err)
	if err == nil {
		s.journalWritten = p.seq
	}
	s.mu.Unlock()
}

// SetOverviewWorker is called only by the trusted Worker channel composition.
// It lives outside the RPC service method set.
func SetOverviewWorker(s *OverviewService, ctx context.Context, generation, auth string, transport http.RoundTripper) {
	u, err := url.Parse(auth)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.User != nil || transport == nil || ctx.Err() != nil {
		return
	}
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Transport: transport, Jar: jar, Timeout: 30 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if r.URL.Scheme != u.Scheme || r.URL.Host != u.Host || len(via) > 3 {
			return errors.New("Worker redirect escaped its authority")
		}
		return nil
	}}
	s.mu.Lock()
	s.worker = &overviewWorker{run: ctx, client: client, auth: auth, origin: u.Scheme + "://" + u.Host, generation: generation}
	s.mu.Unlock()
}

func (s *OverviewService) workerReadyLocked(worker *overviewWorker) bool {
	return worker != nil && worker == s.worker && worker.run.Err() == nil &&
		s.lastState == string(lifecycle.StateReady) && s.generation == worker.generation
}

func overviewCacheKey(key, period, locale string, offset int, at time.Time) string {
	local := at.In(time.FixedZone("local", offset))
	return key + "|" + period + "|" + locale + "|" + local.Format("2006-01-02") + fmt.Sprint(offset)
}

func (s *OverviewService) Observe(status lifecycle.Status) {
	s.mu.Lock()
	s.loadJournalLocked()
	state := string(status.State)
	if state == s.lastState && status.GenerationID == s.generation {
		retry := s.persistenceErrors["journalSave"] != ""
		var pending journalPending
		if retry {
			pending = s.journalPendingLocked()
		}
		s.mu.Unlock()
		if retry {
			s.persistJournal(pending)
		}
		return
	}
	s.lastState, s.generation = state, status.GenerationID
	if status.State == lifecycle.StateReady {
		s.readyAt = time.Now()
	} else {
		s.readyAt = time.Time{}
	}
	s.appendEventLocked(OverviewEvent{At: time.Now().Format(time.RFC3339), Kind: "lifecycle", State: state, Generation: status.GenerationID})
	pending := s.journalPendingLocked()
	s.mu.Unlock()
	s.persistJournal(pending)
}

func (s *OverviewService) GetSnapshot(ctx context.Context, period, locale string, offset int) (out OverviewSnapshot, failure error) {
	if !isWorkspaceSurface(SurfaceFrom(ctx)) {
		return OverviewSnapshot{}, errors.New("workspace surface required")
	}
	if period != "today" && period != "week" && period != "month" {
		return OverviewSnapshot{}, errors.New("invalid period")
	}
	if locale != "zh-CN" && locale != "en" && locale != "ja-JP" {
		return OverviewSnapshot{}, errors.New("invalid locale")
	}
	if offset < -43200 || offset > 50400 {
		return OverviewSnapshot{}, errors.New("invalid timezone offset")
	}
	// Without a readable Run context only the DSH part is missing; PC and DSH Work data still apply.
	key, keyErr := s.ProfileKey(ctx)
	if keyErr != nil {
		key = ""
	}
	var liveWorker *overviewWorker
	var stale overviewCache
	defer func() {
		// A switch while collecting must not attach one Profile's DSH data to another.
		if current, err := s.ProfileKey(ctx); keyErr == nil && (err != nil || current != key) {
			out.DSHPayload, out.StaleAt, out.DSHError = "", "", "Run context changed"
			liveWorker = nil
		}
		s.mu.Lock()
		if liveWorker != nil && !s.workerReadyLocked(liveWorker) {
			out.DSHPayload, out.StaleAt, out.DSHError = "", "", "DSH generation ended"
			if len(stale.Payload) > 0 {
				out.DSHPayload, out.StaleAt = string(stale.Payload), stale.At.Format(time.RFC3339Nano)
			}
		}
		out.AutomaticRecoveries = s.journal.AutomaticRecoveries
		out.RecoveryTrackingSince = s.journal.TrackingSince
		out.PersistenceErrors = make(map[string]string, len(s.persistenceErrors))
		for k, v := range s.persistenceErrors {
			out.PersistenceErrors[k] = v
		}
		s.mu.Unlock()
	}()
	s.refreshRecovery(ctx)
	result := OverviewSnapshot{ProfileKey: key}
	s.mu.Lock()
	s.loadJournalLocked()
	result.Events = append([]OverviewEvent{}, s.events...)
	if !s.readyAt.IsZero() {
		seconds := time.Since(s.readyAt).Seconds()
		result.UptimeSeconds = &seconds
	}
	s.mu.Unlock()
	if s.PC != nil {
		if pc, e := s.PC.Snapshot(ctx); e == nil {
			result.Computer = &pc
		}
	}
	if key == "" {
		result.DSHError = "No run context"
		return result, nil
	}
	s.fetchOnce.Do(func() { s.fetchGate = make(chan struct{}, 1) })
	select {
	case <-ctx.Done():
		return result, ctx.Err()
	case s.fetchGate <- struct{}{}:
		defer func() { <-s.fetchGate }()
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if s.cache == nil {
		s.cache = map[string]overviewCache{}
		if s.Root != "" {
			loaded, e := readOverviewCache(filepath.Join(s.Root, "overview-cache.json"))
			if e == nil {
				s.cache = loaded
			}
			if errors.Is(e, os.ErrNotExist) {
				e = nil
			}
			s.mu.Lock()
			s.persistenceErrorLocked("cacheLoad", e)
			s.mu.Unlock()
		}
	}
	cacheKey := overviewCacheKey(key, period, locale, offset, time.Now())
	if cached, ok := s.cache[cacheKey]; ok {
		stale = cached
		result.DSHPayload, result.StaleAt = string(cached.Payload), cached.At.Format(time.RFC3339Nano)
	}
	s.mu.Lock()
	worker := s.worker
	ready := s.workerReadyLocked(worker)
	s.mu.Unlock()
	if !ready {
		result.DSHError = "DSH is not ready"
		return result, nil
	}
	// Tie requests to both the caller and the Worker generation.
	requestCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	stop := context.AfterFunc(worker.run, cancel)
	defer stop()
	if !worker.authenticated {
		request, _ := http.NewRequestWithContext(requestCtx, http.MethodGet, worker.auth, nil)
		response, e := worker.client.Do(request)
		if e != nil {
			result.DSHError = "DSH authentication unavailable"
			return result, nil
		}
		io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		response.Body.Close()
		worker.authenticated = response.StatusCode == http.StatusOK
	}
	if !worker.authenticated {
		result.DSHError = "DSH authentication unavailable"
		return result, nil
	}
	query := url.Values{"period": {period}, "locale": {locale}, "timezoneOffsetSeconds": {fmt.Sprint(offset)}}
	request, _ := http.NewRequestWithContext(requestCtx, http.MethodGet, worker.origin+"/__dshwork/overview?"+query.Encode(), nil)
	response, e := worker.client.Do(request)
	if e != nil {
		result.DSHError = "DSH data unavailable"
		return result, nil
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		if response.StatusCode == 401 || response.StatusCode == 403 {
			worker.authenticated = false
		}
		result.DSHError = "DSH data unavailable"
		return result, nil
	}
	data, e := io.ReadAll(io.LimitReader(response.Body, overviewPayloadLimit+1))
	var metadata struct {
		CollectedAt string `json:"collectedAt"`
	}
	if e != nil || len(data) > overviewPayloadLimit || json.Unmarshal(data, &metadata) != nil {
		result.DSHError = "Invalid DSH response"
		return result, nil
	}
	collectedAt := time.Now().UTC()
	if metadata.CollectedAt != "" {
		collectedAt, e = time.Parse(time.RFC3339Nano, metadata.CollectedAt)
		if e != nil {
			result.DSHError = "Invalid DSH response"
			return result, nil
		}
	}
	if current, e := s.ProfileKey(ctx); e != nil || current != key {
		result.DSHError = "Run context changed"
		return result, nil
	}
	s.mu.Lock()
	ready = s.workerReadyLocked(worker)
	s.mu.Unlock()
	if requestCtx.Err() != nil || !ready {
		result.DSHError = "DSH generation ended"
		return result, nil
	}
	result.DSHPayload, result.StaleAt = string(data), ""
	liveWorker = worker
	// The Worker may have started collecting after midnight even when the
	// workbench requested the snapshot before it.
	s.storeCache(overviewCacheKey(key, period, locale, offset, collectedAt), data, collectedAt)
	return result, nil
}

// storeCache keeps the newest payload per key and rewrites the file only when a payload changed
// or the previous write failed. Callers hold fetchGate.
func (s *OverviewService) storeCache(key string, data []byte, collectedAt time.Time) {
	var payload bytes.Buffer
	_ = json.Compact(&payload, data) // The response has already been decoded.
	previous, known := s.cache[key]
	s.cache[key] = overviewCache{At: collectedAt.UTC(), Payload: payload.Bytes()}
	for len(s.cache) > overviewCacheEntries {
		oldest := ""
		for k, v := range s.cache {
			if k != key && (oldest == "" || v.At.Before(s.cache[oldest].At)) {
				oldest = k
			}
		}
		delete(s.cache, oldest)
	}
	s.mu.Lock()
	retry := s.persistenceErrors["cacheSave"] != "" || s.persistenceErrors["cacheLoad"] != ""
	s.mu.Unlock()
	if known && !retry && sameOverviewPayload(previous.Payload, payload.Bytes()) {
		return
	}
	err := s.atomicWrite("overview-cache.json", s.cache)
	s.mu.Lock()
	s.persistenceErrorLocked("cacheSave", err)
	if err == nil {
		// A good write replaces whatever unreadable file was there before.
		s.persistenceErrorLocked("cacheLoad", nil)
	}
	s.mu.Unlock()
}

func sameOverviewPayload(a, b json.RawMessage) bool {
	if bytes.Equal(a, b) {
		return true
	}
	// Collection time is provenance, not a change to the dashboard data.
	var left, right map[string]json.RawMessage
	if json.Unmarshal(a, &left) != nil || json.Unmarshal(b, &right) != nil {
		return false
	}
	delete(left, "collectedAt")
	delete(right, "collectedAt")
	if !bytes.Equal(left["data"], right["data"]) {
		var leftData, rightData map[string]json.RawMessage
		if json.Unmarshal(left["data"], &leftData) != nil || json.Unmarshal(right["data"], &rightData) != nil || leftData == nil || rightData == nil {
			return false
		}
		delete(leftData, "now")
		delete(rightData, "now")
		if !sameOverviewFields(leftData, rightData) {
			return false
		}
		delete(left, "data")
		delete(right, "data")
	}
	return sameOverviewFields(left, right)
}

func sameOverviewFields(left, right map[string]json.RawMessage) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if !bytes.Equal(value, right[key]) {
			return false
		}
	}
	return true
}
