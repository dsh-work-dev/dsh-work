// Package dshactivity projects the current DSH Worker's structured activity into
// Host-owned pet state, through the @dsh-work/pet plugin (see hostplugins).
package dshactivity

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

type Interaction struct {
	Key   string `json:"key"`
	Kind  string `json:"kind"`
	Text  string `json:"text"`
	Count int    `json:"count"`
}
type Goal struct {
	Phase     string `json:"phase"`
	Objective string `json:"objective"`
	Reason    string `json:"reason"`
	Rounds    int    `json:"rounds"`
	MaxRounds int    `json:"maxRounds"`
}
type Job struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}
type Activity struct {
	WorkPhase       string       `json:"workPhase"`
	ToolActivity    string       `json:"toolActivity"`
	SessionID       string       `json:"sessionId"`
	ParentSessionID string       `json:"parentSessionId,omitempty"`
	Title           string       `json:"title"`
	Seq             int64        `json:"seq"`
	Turn            int          `json:"turn"`
	Running         bool         `json:"running"`
	Outcome         string       `json:"outcome"`
	Summary         string       `json:"summary"`
	UpdatedAt       int64        `json:"updatedAt"`
	Interaction     *Interaction `json:"interaction,omitempty"`
	Goal            *Goal        `json:"goal,omitempty"`
	Queued          int          `json:"queued"`
	Steering        int          `json:"steering"`
	Jobs            []Job        `json:"jobs"`
	State           string       `json:"state"`
}
type Snapshot struct {
	SchemaVersion   int        `json:"schemaVersion"`
	Generation      string     `json:"generation"`
	Revision        uint64     `json:"revision"`
	Sessions        []Activity `json:"sessions"`
	Connected       bool       `json:"connected"`
	NavigationError string     `json:"navigationError,omitempty"`
}

const (
	activitySilenceTimeout = 45 * time.Second
	activityRetryMinimum   = 500 * time.Millisecond
	activityRetryMaximum   = 5 * time.Second
	activityFrameLimit     = 1 << 20
	openSessionTimeout     = 15 * time.Second
)

type bridgeTimer interface {
	Chan() <-chan time.Time
	Stop()
	Reset(time.Duration)
}

type bridgeClock interface {
	NewTimer(time.Duration) bridgeTimer
}

type realBridgeClock struct{}

func (realBridgeClock) NewTimer(delay time.Duration) bridgeTimer {
	return realBridgeTimer{Timer: time.NewTimer(delay)}
}

type realBridgeTimer struct{ *time.Timer }

func (t realBridgeTimer) Chan() <-chan time.Time    { return t.C }
func (t realBridgeTimer) Reset(delay time.Duration) { t.Timer.Reset(delay) }
func (t realBridgeTimer) Stop()                     { t.Timer.Stop() }

type Bridge struct {
	lifecycleMu               sync.Mutex
	mu                        sync.Mutex
	generation, token, origin string
	client                    *http.Client
	cancel                    context.CancelFunc
	done                      chan struct{}
	state                     Snapshot
	clock                     bridgeClock
	openWindow                func(section, window string) (string, error)
	sendCommand               func(window, name string, ttl time.Duration, data any)
	navigationSequence        uint64
	navigationID              string
	navigationCancel          context.CancelFunc
}

func New() *Bridge { return newWithClock(realBridgeClock{}) }

func newWithClock(clock bridgeClock) *Bridge {
	if clock == nil {
		clock = realBridgeClock{}
	}
	return &Bridge{clock: clock}
}

// SetNavigation connects navigation to the UI window owner and the command
// stream. The UI callback returns the actual window selected after fallback.
func (b *Bridge) SetNavigation(openWindow func(section, window string) (string, error), sendCommand func(window, name string, ttl time.Duration, data any)) {
	b.mu.Lock()
	b.openWindow = openWindow
	b.sendCommand = sendCommand
	b.mu.Unlock()
}

// Begin resets the projection for a new Worker generation and returns the
// launch configuration of the @dsh-work/pet plugin: the generation and a fresh
// token that authenticates its routes.
func (b *Bridge) Begin(generation string) (map[string]string, error) {
	b.lifecycleMu.Lock()
	defer b.lifecycleMu.Unlock()
	if generation == "" {
		return nil, errors.New("missing activity generation")
	}
	b.stop()
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	token := hex.EncodeToString(raw)
	b.mu.Lock()
	if b.cancel != nil {
		b.cancel()
		b.cancel = nil
	}
	b.generation = generation
	b.token = token
	b.origin = ""
	b.client = nil
	b.state = Snapshot{SchemaVersion: 1, Generation: generation, Sessions: []Activity{}}
	b.mu.Unlock()
	return map[string]string{"generation": generation, "token": token}, nil
}

// Start observes the authenticated Worker through its owned transport.
func (b *Bridge) Start(ctx context.Context, authURL string, transport http.RoundTripper) {
	if transport == nil {
		return
	}
	b.lifecycleMu.Lock()
	defer b.lifecycleMu.Unlock()
	u, err := url.Parse(authURL)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.User != nil {
		return
	}
	b.stop()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Transport: transport, Jar: jar, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != u.Scheme || req.URL.Host != u.Host || len(via) > 3 {
			return errors.New("invalid Worker redirect")
		}
		return nil
	}}
	b.mu.Lock()
	if b.cancel != nil {
		b.cancel()
	}
	run, cancel := context.WithCancel(ctx)
	b.cancel = cancel
	done := make(chan struct{})
	b.done = done
	generation, token := b.generation, b.token
	origin := u.Scheme + "://" + u.Host
	b.client = client
	b.origin = origin
	b.mu.Unlock()
	go func() {
		defer close(done)
		defer b.disconnected(generation)
		defer client.CloseIdleConnections()
		authenticated := false
		retryDelay := activityRetryMinimum
		for {
			if run.Err() != nil {
				return
			}
			if !authenticated {
				req, _ := http.NewRequestWithContext(run, http.MethodGet, authURL, nil)
				resp, err := client.Do(req)
				if err == nil {
					io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
					resp.Body.Close()
					authenticated = resp.StatusCode == http.StatusOK
				}
			}
			if authenticated {
				streamCtx, stopStream := context.WithCancel(run)
				receivedFrame, _ := receiveActivity(streamCtx, client, origin, token, generation, b.clock, func(state Snapshot) {
					b.accept(generation, state)
				})
				stopStream()
				if run.Err() != nil {
					b.disconnected(generation)
					return
				}
				b.disconnected(generation)
				authenticated = false
				if receivedFrame {
					retryDelay = activityRetryMinimum
				}
			} else {
				b.disconnected(generation)
			}
			if !waitBridgeTimer(run, b.clock, retryDelay) {
				b.disconnected(generation)
				return
			}
			retryDelay = min(retryDelay*2, activityRetryMaximum)
		}
	}()
}

func waitBridgeTimer(ctx context.Context, clock bridgeClock, delay time.Duration) bool {
	timer := clock.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.Chan():
		return true
	}
}

type activityFrame struct {
	event string
	data  string
	ping  bool
	err   error
}

type activityResponse struct {
	response *http.Response
	err      error
}

func receiveActivity(ctx context.Context, client *http.Client, origin, token, generation string, clock bridgeClock, accept func(Snapshot)) (bool, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, origin+"/__dshwork/pet-activity", nil)
	req.Header.Set("X-DSH-Work-Token", token)
	req.Header.Set("Origin", origin)
	req.Header.Set("Accept", "text/event-stream")
	timer := clock.NewTimer(activitySilenceTimeout)
	defer timer.Stop()
	responses := make(chan activityResponse)
	go func() {
		resp, err := client.Do(req)
		select {
		case responses <- activityResponse{response: resp, err: err}:
		case <-ctx.Done():
			if resp != nil {
				_ = resp.Body.Close()
			}
		}
	}()
	var response activityResponse
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case <-timer.Chan():
		return false, errors.New("activity stream silent for 45 seconds")
	case response = <-responses:
		if response.err != nil {
			return false, response.err
		}
	}
	resp := response.response
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("activity status %d", resp.StatusCode)
	}
	frames := make(chan activityFrame, 1)
	readerCtx, cancelReader := context.WithCancel(ctx)
	defer cancelReader()
	go readActivityFrames(readerCtx, resp.Body, frames)
	receivedFrame := false
	for {
		select {
		case <-ctx.Done():
			return receivedFrame, ctx.Err()
		case <-timer.Chan():
			return receivedFrame, errors.New("activity stream silent for 45 seconds")
		case frame := <-frames:
			if frame.err != nil {
				return receivedFrame, frame.err
			}
			if frame.ping {
				timer.Reset(activitySilenceTimeout)
				continue
			}
			// Any complete SSE event keeps the transport alive. Only snapshot
			// events update projected activity.
			timer.Reset(activitySilenceTimeout)
			if frame.event != "snapshot" {
				continue
			}
			if len(frame.data) > activityFrameLimit {
				return receivedFrame, errors.New("activity snapshot too large")
			}
			var state Snapshot
			if err := json.Unmarshal([]byte(frame.data), &state); err != nil {
				return receivedFrame, err
			}
			if state.SchemaVersion != 1 || len(state.Sessions) > 256 || state.Generation != generation || !normalize(&state) {
				return receivedFrame, errors.New("invalid activity snapshot")
			}
			accept(state)
			receivedFrame = true
		}
	}
}

func readActivityFrames(ctx context.Context, body io.Reader, frames chan<- activityFrame) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 32<<10), activityFrameLimit)
	event := ""
	var data strings.Builder
	frameBytes := 0
	write := func(frame activityFrame) bool {
		select {
		case frames <- frame:
			return true
		case <-ctx.Done():
			return false
		}
	}
	for scanner.Scan() {
		line := scanner.Text()
		frameBytes += len(line) + 1
		if frameBytes > activityFrameLimit {
			write(activityFrame{err: errors.New("activity event too large")})
			return
		}
		if line == "" {
			if event != "" || data.Len() > 0 {
				payload := strings.TrimSuffix(data.String(), "\n")
				if !write(activityFrame{event: event, data: payload}) {
					return
				}
			}
			event = ""
			data.Reset()
			frameBytes = 0
			continue
		}
		if strings.HasPrefix(line, ":") {
			if strings.TrimSpace(strings.TrimPrefix(line, ":")) == "ping" && !write(activityFrame{ping: true}) {
				return
			}
			continue
		}
		field, value, found := strings.Cut(line, ":")
		if !found {
			field, value = line, ""
		} else {
			value = strings.TrimPrefix(value, " ")
		}
		switch field {
		case "event":
			event = value
		case "data":
			data.WriteString(value)
			data.WriteByte('\n')
		}
	}
	if err := scanner.Err(); err != nil {
		write(activityFrame{err: fmt.Errorf("read activity stream: %w", err)})
	} else if ctx.Err() == nil {
		write(activityFrame{err: io.EOF})
	}
}
func (b *Bridge) accept(generation string, state Snapshot) {
	if !normalize(&state) {
		b.disconnected(generation)
		return
	}
	b.mu.Lock()
	if b.generation != generation {
		b.mu.Unlock()
		return
	}
	state.Connected = true
	state.NavigationError = b.state.NavigationError
	b.state = state
	b.mu.Unlock()
}
func (b *Bridge) disconnected(generation string) {
	b.mu.Lock()
	if b.generation != generation || !b.state.Connected {
		b.mu.Unlock()
		return
	}
	b.state.Connected = false
	b.mu.Unlock()
}
func (b *Bridge) Snapshot() Snapshot { b.mu.Lock(); defer b.mu.Unlock(); return clone(b.state) }
func (b *Bridge) Open(ctx context.Context, id, window string) error {
	b.mu.Lock()
	connected := b.client != nil && b.state.Connected
	found := false
	parentSessionID := ""
	for _, a := range b.state.Sessions {
		if a.SessionID == id {
			found = true
			parentSessionID = a.ParentSessionID
			break
		}
	}
	openWindow, sendCommand := b.openWindow, b.sendCommand
	generation := b.generation
	b.mu.Unlock()
	if !connected || !found {
		return errors.New("conversation activity is unavailable")
	}
	if openWindow == nil || sendCommand == nil {
		return errors.New("conversation navigation is unavailable")
	}
	if window == "" {
		window = "workspace"
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	actualWindow, err := openWindow("", window)
	if err != nil {
		return err
	}
	if actualWindow == "" {
		return errors.New("desktop window is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	if b.navigationCancel != nil {
		b.navigationCancel()
	}
	b.navigationSequence++
	navigationID := fmt.Sprintf("%d", b.navigationSequence)
	navigationCtx, cancelNavigation := context.WithCancel(context.Background())
	b.navigationID = navigationID
	b.navigationCancel = cancelNavigation
	b.state.NavigationError = ""
	clock := b.clock
	if clock == nil {
		clock = realBridgeClock{}
	}
	b.mu.Unlock()
	timer := clock.NewTimer(openSessionTimeout)
	sendCommand(actualWindow, "dsh-open-session", openSessionTimeout, map[string]string{
		"id": navigationID, "sessionId": id, "parentSessionId": parentSessionID,
	})
	go func() {
		defer timer.Stop()
		select {
		case <-navigationCtx.Done():
		case <-timer.Chan():
			b.navigationTimedOut(generation, navigationID)
		}
	}()
	return nil
}

// ReportSessionOpened commits only the most recently requested navigation.
func (b *Bridge) ReportSessionOpened(id, reportedError string) {
	b.mu.Lock()
	if id == "" || b.navigationID != id {
		b.mu.Unlock()
		return
	}
	cancel := b.navigationCancel
	b.navigationCancel = nil
	b.navigationID = ""
	b.state.NavigationError = clean(reportedError, 160)
	b.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (b *Bridge) navigationTimedOut(generation, id string) {
	b.mu.Lock()
	if b.generation != generation || b.navigationID != id {
		b.mu.Unlock()
		return
	}
	b.navigationID = ""
	b.navigationCancel = nil
	b.state.NavigationError = "Conversation is unavailable"
	b.mu.Unlock()
}
func (b *Bridge) Close() {
	b.lifecycleMu.Lock()
	defer b.lifecycleMu.Unlock()
	b.stop()
}

// Caller serializes lifecycle operations; join before replacing the source.
func (b *Bridge) stop() {
	b.mu.Lock()
	if b.cancel != nil {
		b.cancel()
		b.cancel = nil
	}
	done := b.done
	b.done = nil
	navigationCancel := b.navigationCancel
	b.navigationCancel = nil
	b.navigationID = ""
	b.mu.Unlock()
	if navigationCancel != nil {
		navigationCancel()
	}
	if done != nil {
		<-done
	}
}

func clean(s string, n int) string {
	r := []rune(strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)))
	if len(r) > n {
		r = r[:n]
	}
	return string(r)
}
func normalize(s *Snapshot) bool {
	ids := map[string]bool{}
	for i := range s.Sessions {
		a := &s.Sessions[i]
		if a.SessionID == "" || len(a.SessionID) > 256 || ids[a.SessionID] || a.Seq < -1 {
			return false
		}
		ids[a.SessionID] = true
		a.Title = clean(a.Title, 120)
		a.Summary = clean(a.Summary, 240)
		switch a.WorkPhase {
		case "thinking", "working", "result":
		default:
			a.WorkPhase = ""
		}
		switch a.ToolActivity {
		case "searching", "editing", "testing", "commanding", "using-tool":
		default:
			a.ToolActivity = ""
		}
		a.ParentSessionID = clean(a.ParentSessionID, 256)
		a.Queued = min(max(a.Queued, 0), 10000)
		a.Steering = min(max(a.Steering, 0), 10000)
		if a.Interaction != nil {
			a.Interaction.Text = clean(a.Interaction.Text, 240)
			a.Interaction.Key = clean(a.Interaction.Key, 256)
			a.Interaction.Count = min(max(a.Interaction.Count, 0), 100)
			switch a.Interaction.Kind {
			case "approval", "question", "plan-review":
			default:
				a.Interaction = nil
			}
		}
		if a.Goal != nil {
			a.Goal.Objective = clean(a.Goal.Objective, 240)
			a.Goal.Reason = clean(a.Goal.Reason, 240)
			a.Goal.Phase = clean(a.Goal.Phase, 32)
		}
		if len(a.Jobs) > 32 {
			return false
		}
		for j := range a.Jobs {
			a.Jobs[j].Label = clean(a.Jobs[j].Label, 120)
			a.Jobs[j].Status = clean(a.Jobs[j].Status, 32)
			a.Jobs[j].Detail = clean(a.Jobs[j].Detail, 160)
		}
		a.State = activityState(*a)
	}
	sort.SliceStable(s.Sessions, func(i, j int) bool {
		a, b := s.Sessions[i], s.Sessions[j]
		if priority(a.State) != priority(b.State) {
			return priority(a.State) < priority(b.State)
		}
		if a.UpdatedAt != b.UpdatedAt {
			return a.UpdatedAt > b.UpdatedAt
		}
		return a.SessionID < b.SessionID
	})
	s.NavigationError = clean(s.NavigationError, 160)
	return true
}
func activityState(a Activity) string {
	if a.Interaction != nil {
		return "needs-input"
	}
	if a.Goal != nil && a.Goal.Phase == "blocked" {
		return "blocked"
	}
	switch a.Outcome {
	case "error", "blocked", "max-tokens", "interrupted":
		if !a.Running {
			return a.Outcome
		}
	}
	if a.Running {
		return "running"
	}
	for _, job := range a.Jobs {
		if job.Status == "failed" {
			return "error"
		}
	}
	for _, job := range a.Jobs {
		if job.Status == "running" || job.Status == "stopping" {
			return "running"
		}
	}
	return "idle"
}
func priority(s string) int {
	switch s {
	case "needs-input":
		return 0
	case "blocked", "error", "max-tokens", "interrupted":
		return 1
	case "running":
		return 3
	}
	return 4
}
func clone(s Snapshot) Snapshot {
	data, _ := json.Marshal(s)
	var copy Snapshot
	_ = json.Unmarshal(data, &copy)
	return copy
}

// AnimationIntent does not expose the originating asset's row or frame geometry.
func AnimationIntent(s Snapshot) (kind, key string) {
	if !s.Connected {
		return "host.offline", "disconnected"
	}
	if len(s.Sessions) == 0 {
		return "host.ready", "idle"
	}
	a := s.Sessions[0]
	key = fmt.Sprintf("%s:%d:%s:%s", a.SessionID, a.Turn, a.State, a.Outcome)
	switch a.State {
	case "needs-input":
		key += ":" + a.Interaction.Key
		if a.Interaction.Kind == "plan-review" {
			return "task.reviewing", key
		}
		return "task.waiting", key
	case "error":
		return "task.failed", key
	case "blocked":
		return "task.blocked", key
	case "max-tokens":
		return "task.limited", key
	case "interrupted":
		return "task.interrupted", key
	case "running":
		if a.WorkPhase == "thinking" {
			return "task.thinking", key + ":thinking"
		}
		if a.WorkPhase == "result" {
			return "task.result", key + ":result"
		}
		return "task.working", key
	}
	if a.State != "running" && a.Outcome == "completed" {
		return "task.completed", key
	}
	if a.Outcome == "aborted" {
		return "task.cancelled", key
	}
	return "host.ready", key
}
