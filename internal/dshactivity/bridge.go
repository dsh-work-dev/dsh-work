// Package dshactivity projects the current DSH Worker's structured activity into
// Host-owned pet state. The DSH plugin is a launch overlay, never a profile edit.
package dshactivity

import (
	"bytes"
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

//go:embed plugin/*
var plugin embed.FS

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
	Unread          bool         `json:"unread"`
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

type Bridge struct {
	lifecycleMu                     sync.Mutex
	mu                              sync.Mutex
	root, generation, token, origin string
	client                          *http.Client
	cancel                          context.CancelFunc
	done                            chan struct{}
	state                           Snapshot
}

func New(root string) *Bridge { return &Bridge{root: root} }

// Prepare writes our embedded package and returns an ephemeral CLI patch.
// The directory is Host-owned application data, separate from DSH profiles.
func (b *Bridge) Prepare(generation string) (string, error) {
	b.lifecycleMu.Lock()
	defer b.lifecycleMu.Unlock()
	if generation == "" {
		return "", errors.New("missing activity generation")
	}
	b.stop()
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := hex.EncodeToString(raw)
	root, err := filepath.Abs(b.root)
	if err != nil {
		return "", err
	}
	if err = os.MkdirAll(root, 0700); err != nil {
		return "", err
	}
	for _, name := range []string{"package.json", "host.js", "client.js"} {
		content, err := plugin.ReadFile("plugin/" + name)
		if err != nil {
			return "", err
		}
		if err = os.WriteFile(filepath.Join(root, name), content, 0600); err != nil {
			return "", err
		}
	}
	// JSON is valid YAML; the launcher consumes a patch-list overlay.
	patch := []any{map[string]any{"insert": []any{map[string]any{"id": "dsh-work-pet-activity", "name": moduleURL(filepath.Join(root, "host.js")), "config": map[string]string{"generation": generation, "token": token}}}}}
	data, err := json.Marshal(patch)
	if err != nil {
		return "", err
	}
	path := filepath.Join(root, "launch.patch.yml")
	if err = os.WriteFile(path, data, 0600); err != nil {
		return "", err
	}
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
	return path, nil
}

// Start follows the existing authenticated Worker. It never starts another DSH.
func (b *Bridge) Start(ctx context.Context, authURL string) {
	b.lifecycleMu.Lock()
	defer b.lifecycleMu.Unlock()
	u, err := url.Parse(authURL)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.User != nil {
		return
	}
	b.stop()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 3 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
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
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
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
				state, err := fetch(run, client, origin, token)
				if err == nil && state.Generation == generation {
					b.accept(generation, state)
				} else {
					b.disconnected(generation)
					authenticated = false
				}
			} else {
				b.disconnected(generation)
			}
			select {
			case <-run.Done():
				b.disconnected(generation)
				return
			case <-ticker.C:
			}
		}
	}()
}
func fetch(ctx context.Context, client *http.Client, origin, token string) (Snapshot, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, origin+"/__dshwork/activity", nil)
	req.Header.Set("X-DSH-Work-Token", token)
	req.Header.Set("Origin", origin)
	resp, err := client.Do(req)
	if err != nil {
		return Snapshot{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return Snapshot{}, fmt.Errorf("activity status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20+1))
	if err != nil || len(data) > 1<<20 {
		return Snapshot{}, errors.New("activity snapshot too large")
	}
	var state Snapshot
	if err = json.Unmarshal(data, &state); err != nil {
		return state, err
	}
	if state.SchemaVersion != 1 || len(state.Sessions) > 256 {
		return Snapshot{}, errors.New("invalid activity snapshot")
	}
	return state, nil
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
func (b *Bridge) Open(ctx context.Context, id string) error {
	b.mu.Lock()
	client, origin, token, connected := b.client, b.origin, b.token, b.state.Connected
	found := false
	for _, a := range b.state.Sessions {
		if a.SessionID == id {
			found = true
			break
		}
	}
	b.mu.Unlock()
	if !connected || client == nil || !found {
		return errors.New("conversation activity is unavailable")
	}
	data, _ := json.Marshal(map[string]string{"sessionId": id})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, origin+"/__dshwork/activity", bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("X-DSH-Work-Token", token)
	req.Header.Set("Origin", origin)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return errors.New("conversation activity is unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return errors.New("conversation is unavailable")
	}
	return nil
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
	b.mu.Unlock()
	if done != nil {
		<-done
	}
}

func moduleURL(path string) string {
	path = filepath.ToSlash(path)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return (&url.URL{Scheme: "file", Path: path}).String()
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
	if a.Unread && a.Outcome == "completed" {
		return "ready"
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
	case "ready":
		return 2
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
	case "ready":
		return "task.completed", key
	case "running":
		if a.WorkPhase == "thinking" {
			return "task.thinking", key + ":thinking"
		}
		if a.WorkPhase == "result" {
			return "task.result", key + ":result"
		}
		return "task.working", key
	}
	if a.Outcome == "aborted" {
		return "task.cancelled", key
	}
	return "host.ready", key
}
