// Isolated BROWSER_LAB adapter: real automation.Service policy/permits, lab-only admission.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/local/dsh-work/internal/automation"
)

const backendURL = "http://127.0.0.1:19483"
const fixtureInstance = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const fixtureName = "BROWSER_LAB CEF lab admission fixture"

type backendClient struct {
	token string
	http  *http.Client
}

func (b *backendClient) call(ctx context.Context, method, path string, body any) (any, error) {
	var input io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		input = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, backendURL+path, input)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Browser-Lab-Token", b.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := b.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	var value any
	if err := json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(&value); err != nil {
		return nil, fmt.Errorf("backend response: %w", err)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		if m, ok := value.(map[string]any); ok {
			if message, ok := m["error"].(string); ok {
				return nil, errors.New(message)
			}
		}
		return nil, fmt.Errorf("backend HTTP %d", res.StatusCode)
	}
	return value, nil
}

type labPreparer struct {
	backend *backendClient
	ready   atomic.Bool
}

func (p *labPreparer) Ready(c automation.Capability) bool {
	return c == automation.Browser && p.ready.Load()
}
func (p *labPreparer) Prepare(ctx context.Context, c automation.Capability, report func(automation.Progress)) error {
	if c != automation.Browser {
		return errors.New("BROWSER_LAB_BROWSER_ONLY")
	}
	p.ready.Store(false)
	report(automation.Progress{Step: "checking isolated CEF adapter"})
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	v, err := p.backend.call(bounded, http.MethodGet, "/health", nil)
	if err != nil {
		return err
	}
	m, ok := v.(map[string]any)
	if !ok || m["ready"] != true {
		return errors.New("BROWSER_LAB_BACKEND_NOT_READY")
	}
	targets, ok := m["targets"].(float64)
	if !ok || targets < 2 {
		return errors.New("BROWSER_LAB_REQUIRES_TWO_CEF_TARGETS")
	}
	p.ready.Store(true)
	return nil
}

type labResource struct {
	backend  *backendClient
	id       string
	closed   atomic.Bool
	once     sync.Once
	closeErr error
}

func (r *labResource) Execute(ctx context.Context, tool string, args map[string]any) (any, error) {
	if r.closed.Load() {
		return nil, automation.ErrRevoked
	}
	return r.backend.call(ctx, http.MethodPost, "/execute", map[string]any{"tool": tool, "arguments": args, "resourceId": r.id})
}
func (r *labResource) Close() error {
	r.once.Do(func() {
		r.closed.Store(true)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, r.closeErr = r.backend.call(ctx, http.MethodPost, "/release", map[string]any{"resourceId": r.id})
	})
	return r.closeErr
}

func ownedScratch(dataRoot string) (string, error) {
	if dataRoot == "" {
		return "", errors.New("BROWSER_LAB_DATA_ROOT_REQUIRED")
	}
	parent, err := filepath.EvalSymlinks(dataRoot)
	if err != nil {
		return "", fmt.Errorf("BROWSER_LAB_DATA_ROOT must already exist: %w", err)
	}
	parent, err = filepath.Abs(parent)
	if err != nil {
		return "", err
	}
	temp, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		return "", err
	}
	temp, err = filepath.Abs(temp)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(temp, parent)
	if err != nil || rel == "." || rel == ".." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("BROWSER_LAB_DATA_ROOT_REQUIRES_PRIVATE_TEMP_CHILD")
	}
	root := filepath.Join(parent, "business-gateway")
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	rel, err = filepath.Rel(parent, resolved)
	if err != nil || rel != "business-gateway" {
		return "", errors.New("BROWSER_LAB_GATEWAY_ROOT_CHANGED")
	}
	marker := filepath.Join(root, "browser-acceptance-owner.txt")
	const owner = "dsh-work-browser-acceptance-v1"
	f, err := os.OpenFile(marker, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err == nil {
		_, writeErr := f.WriteString(owner + "\n")
		closeErr := f.Close()
		if writeErr != nil {
			return "", writeErr
		}
		if closeErr != nil {
			return "", closeErr
		}
	} else if os.IsExist(err) {
		value, readErr := os.ReadFile(marker)
		if readErr != nil || strings.TrimSpace(string(value)) != owner {
			return "", errors.New("BROWSER_LAB_OWNER_MARKER_MISMATCH")
		}
	} else {
		return "", err
	}
	return root, nil
}

func makeService(ctx context.Context, root string, b *backendClient) (*automation.Service, error) {
	prepare := &labPreparer{backend: b}
	s, _, err := automation.NewDevelopmentAcceptance(root, prepare)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			s.Close()
		}
	}()
	var schemas []json.RawMessage
	for _, tool := range []string{"browser_snapshot", "browser_type", "browser_click", "browser_navigate"} {
		raw, _ := json.Marshal(map[string]any{"name": tool, "inputSchema": map[string]any{"type": "object"}})
		schemas = append(schemas, raw)
	}
	s.ConfigureBrowserBackend(func(_ context.Context, _ automation.Request) (automation.Resource, error) {
		return &labResource{backend: b, id: "browser-lab-" + rand.Text()}, nil
	}, schemas)
	s.EnableBrowserGateway(automation.BrowserGatewayConfig{NativePrompts: true, ActivityPath: filepath.Join(root, "browser-activity.json")})
	// Deliberate isolated lab policy: exercise real browser and new-site prompts
	// rather than inheriting an earlier lab run's persistent "always" choice.
	policyState := s.Snapshot().BrowserAccess
	policy := policyState.Policy
	policy.AskEachConversation, policy.NewSites = true, "ask"
	policy.Sites = nil
	if _, err = s.SetBrowserPolicy(ctx, policyState.PolicyRevision, policy); err != nil {
		return nil, err
	}
	generation := "browser-lab-" + rand.Text()
	s.Worker(generation, true, true)
	if _, err = s.SetEnabled(ctx, automation.Browser, true); err != nil {
		return nil, err
	}
	if _, err = s.SetDefaultBrowser(ctx, true); err != nil {
		return nil, err
	}
	if _, err = s.Retry(automation.Browser); err != nil {
		return nil, err
	}
	deadline := time.NewTimer(8 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
wait:
	for {
		v := s.Snapshot()
		switch v.Browser.Dependency {
		case "prepared":
			break wait
		case "failed":
			return nil, fmt.Errorf("preparation failed: %s", v.Browser.Failure)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			return nil, errors.New("BROWSER_LAB_PREPARATION_TIMEOUT")
		case <-tick.C:
		}
	}
	s.Gateway(generation, true)
	// Public metadata admission fixture. No native host/extension authentication
	// is claimed: it stands in solely for the missing product CEF provider.
	connection, err := s.BrowserConnected("chrome", automation.BrowserHello{
		Source: "browser-lab-cef-adapter", Protocol: automation.BrowserProtocol,
		ExtensionVersion: "0.1.0", ExtensionID: automation.DevelopmentExtensionID, Instance: fixtureInstance,
	})
	if err != nil {
		return nil, err
	}
	binding := ""
	for _, v := range s.Snapshot().Profiles.Bindings {
		if v.Name == fixtureName {
			binding = v.ID
			break
		}
	}
	if binding == "" {
		pending, err := s.RequestBrowserBinding(connection, fixtureName)
		if err != nil {
			return nil, err
		}
		binding, err = s.ConfirmBrowserBinding(ctx, pending.ID)
		if err != nil {
			return nil, err
		}
	}
	if err := s.SelectBrowserBinding(ctx, binding); err != nil {
		return nil, err
	}
	ok = true
	return s, nil
}

func reply(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Browser-Lab-Adapter", "real-service-synthetic-admission")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func fail(w http.ResponseWriter, err error) {
	reply(w, http.StatusConflict, map[string]any{"error": err.Error()})
}
func decode(w http.ResponseWriter, r *http.Request, value any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		reply(w, 400, map[string]any{"error": "INVALID_JSON: " + err.Error()})
		return false
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		reply(w, 400, map[string]any{"error": "TRAILING_JSON"})
		return false
	}
	return true
}
func browserRequest(w http.ResponseWriter, r *http.Request) (automation.Request, bool) {
	var req automation.Request
	if !decode(w, r, &req) {
		return req, false
	}
	if req.Capability != automation.Browser || req.Operation != "browser" {
		reply(w, 400, map[string]any{"error": "BROWSER_LAB_BROWSER_OPERATION_REQUIRED"})
		return req, false
	}
	return req, true
}
func handler(s *automation.Service, token string, shutdown context.CancelFunc) http.Handler {
	mux := http.NewServeMux()
	var stopping atomic.Bool
	mux.HandleFunc("POST /shutdown", func(w http.ResponseWriter, r *http.Request) {
		stopping.Store(true)
		reply(w, http.StatusAccepted, map[string]any{"shuttingDown": true})
		// Deliver the acknowledgement before cancellation starts service release
		// and the HTTP drain. This endpoint never closes the displayed guest.
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		shutdown()
	})
	mux.HandleFunc("GET /state", func(w http.ResponseWriter, r *http.Request) { reply(w, 200, s.Snapshot()) })
	mux.HandleFunc("POST /begin", func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Session  string `json:"session"`
			Lifetime string `json:"lifetime"`
			Turn     string `json:"turn"`
		}
		if !decode(w, r, &v) {
			return
		}
		state := s.Snapshot()
		req := automation.Request{Generation: state.Generation, Epoch: state.Browser.Epoch, Capability: automation.Browser,
			Session: v.Session, Lifetime: v.Lifetime, Turn: v.Turn, Operation: "browser", Tool: "browser_snapshot",
			Arguments: map[string]any{}, Title: "Browser Lab isolated fixture", UserTurn: true}
		turn, err := s.BrowserTurn(req)
		if err != nil {
			fail(w, err)
			return
		}
		req.Lifetime, req.Turn = turn["lifetime"], turn["turn"]
		req.Epoch = s.Snapshot().Browser.Epoch
		reply(w, 200, map[string]any{"turn": turn, "request": req})
	})
	mux.HandleFunc("POST /authorize", func(w http.ResponseWriter, r *http.Request) {
		req, ok := browserRequest(w, r)
		if !ok {
			return
		}
		value, err := s.AuthorizeBrowser(r.Context(), req)
		if err != nil {
			fail(w, err)
			return
		}
		reply(w, 200, value)
	})
	mux.HandleFunc("POST /execute", func(w http.ResponseWriter, r *http.Request) {
		req, ok := browserRequest(w, r)
		if !ok {
			return
		}
		value, err := s.Execute(r.Context(), req)
		if err != nil {
			fail(w, err)
			return
		}
		reply(w, 200, value)
	})
	mux.HandleFunc("POST /answer", func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			ID     string `json:"id"`
			Choice string `json:"choice"`
		}
		if !decode(w, r, &v) {
			return
		}
		value, err := s.AnswerBrowserConsent(r.Context(), v.ID, v.Choice)
		if err != nil {
			fail(w, err)
			return
		}
		reply(w, 200, value)
	})
	mux.HandleFunc("POST /revoke", func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Session  string `json:"session"`
			Lifetime string `json:"lifetime"`
		}
		if !decode(w, r, &v) {
			return
		}
		// Empty session could revoke every conversation; lab API scopes explicitly.
		if v.Session == "" || v.Lifetime == "" {
			reply(w, 400, map[string]any{"error": "BROWSER_LAB_SCOPE_REQUIRED"})
			return
		}
		if err := s.StopBrowserUse(v.Session, v.Lifetime); err != nil {
			fail(w, err)
			return
		}
		reply(w, 200, s.Snapshot())
	})
	mux.HandleFunc("POST /generation", func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Generation string `json:"generation"`
		}
		if !decode(w, r, &v) {
			return
		}
		if v.Generation == "" || v.Generation == s.Snapshot().Generation {
			reply(w, 400, map[string]any{"error": "BROWSER_LAB_NEW_GENERATION_REQUIRED"})
			return
		}
		s.Worker(v.Generation, true, true)
		s.Gateway(v.Generation, true)
		reply(w, 200, s.Snapshot())
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		provided := r.Header.Get("X-Browser-Lab-Token")
		if len(provided) != len(token) || subtle.ConstantTimeCompare([]byte(provided), []byte(token)) != 1 {
			reply(w, http.StatusUnauthorized, map[string]any{"error": "UNAUTHORIZED"})
			return
		}
		// Authenticated native caller only; browser origins are not an authorization channel.
		if r.Header.Get("Origin") != "" {
			reply(w, 403, map[string]any{"error": "BROWSER_LAB_NATIVE_CALLER_REQUIRED"})
			return
		}
		if stopping.Load() && r.URL.Path != "/shutdown" {
			reply(w, http.StatusServiceUnavailable, map[string]any{"error": "BROWSER_LAB_SHUTTING_DOWN"})
			return
		}
		mux.ServeHTTP(w, r)
	})
}
func run() error {
	token := os.Getenv("BROWSER_LAB_TOKEN")
	if len(token) < 24 {
		return errors.New("BROWSER_LAB_TOKEN_REQUIRES_AT_LEAST_24_CHARACTERS")
	}
	root, err := ownedScratch(os.Getenv("BROWSER_LAB_DATA_ROOT"))
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	backend := &backendClient{token: token, http: &http.Client{Timeout: 35 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("BROWSER_LAB_BACKEND_REDIRECT_BLOCKED")
		}}}
	s, err := makeService(ctx, root, backend)
	if err != nil {
		return err
	}
	var closeOnce sync.Once
	closeService := func() { closeOnce.Do(s.Close) }
	defer closeService()
	server := &http.Server{Addr: "127.0.0.1:19482", Handler: handler(s, token, cancel), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	shutdownDone := make(chan error, 1)
	go func() {
		<-ctx.Done()
		// Real Close cancels pending authorization/execution, releases adapter
		// resources and waits for the activity audit before HTTP handlers drain.
		closeService()
		c, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		shutdownDone <- server.Shutdown(c)
	}()
	fmt.Println("BROWSER_LAB gateway ready at 127.0.0.1:19482; real Service/permits, lab admission fixture; no consent auto-answer.")
	err = server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return <-shutdownDone
	}
	return err
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
