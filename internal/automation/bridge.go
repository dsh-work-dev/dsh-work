package automation

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Bridge uses the existing authenticated Worker transport. The plugin's queue
// inverts requests over that owned channel; there is no new listening socket.
type Bridge struct {
	mu                sync.Mutex
	generation, token string
	service           *Service
	wake              chan struct{}
	cancel            context.CancelFunc
	done              chan struct{}
}

func NewBridge() *Bridge            { return &Bridge{wake: make(chan struct{}, 1)} }
func (b *Bridge) Attach(s *Service) { b.mu.Lock(); b.service = s; b.mu.Unlock() }
func (b *Bridge) Publish() {
	select {
	case b.wake <- struct{}{}:
	default:
	}
}
func (b *Bridge) Begin(generation string) (map[string]string, error) {
	b.Stop()
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.generation = generation
	b.token = hex.EncodeToString(raw)
	return map[string]string{"generation": generation, "token": b.token}, nil
}
func (b *Bridge) Stop() {
	b.mu.Lock()
	cancel, done := b.cancel, b.done
	b.cancel = nil
	b.done = nil
	b.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}
func (b *Bridge) Start(ctx context.Context, auth string, transport http.RoundTripper) {
	b.Stop()
	u, err := url.Parse(auth)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.User != nil || transport == nil {
		return
	}
	b.mu.Lock()
	service, generation, token := b.service, b.generation, b.token
	run, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	b.cancel, b.done = cancel, done
	b.mu.Unlock()
	if service == nil {
		cancel()
		close(done)
		return
	}
	go func() {
		defer close(done)
		defer cancel()
		jar, _ := cookiejar.New(nil)
		client := &http.Client{Transport: transport, Jar: jar, CheckRedirect: func(r *http.Request, via []*http.Request) error {
			if r.URL.Scheme != u.Scheme || r.URL.Host != u.Host || len(via) > 3 {
				return errors.New("Worker redirect escaped authority")
			}
			return nil
		}}
		defer client.CloseIdleConnections()
		origin := u.Scheme + "://" + u.Host
		request := func(ctx context.Context, method, path string, value any) (*http.Response, error) {
			var body io.Reader
			if value != nil {
				data, _ := json.Marshal(value)
				body = bytes.NewReader(data)
			}
			req, e := http.NewRequestWithContext(ctx, method, origin+path, body)
			if e != nil {
				return nil, e
			}
			req.Header.Set("X-DSH-Work-Token", token)
			req.Header.Set("Content-Type", "application/json")
			return client.Do(req)
		}
		post := func(path string, value any) error {
			bounded, stop := context.WithTimeout(run, 5*time.Second)
			defer stop()
			resp, e := request(bounded, "POST", path, value)
			if e != nil {
				return e
			}
			defer resp.Body.Close()
			io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			if resp.StatusCode != 200 {
				return errors.New("automation host exchange rejected")
			}
			return nil
		}
		authCtx, authCancel := context.WithTimeout(run, 5*time.Second)
		defer authCancel()
		authReq, _ := http.NewRequestWithContext(authCtx, "GET", auth, nil)
		resp, e := client.Do(authReq)
		if e != nil {
			return
		}
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		if resp.StatusCode != 200 {
			return
		}
		state := service.Snapshot()
		if state.Generation != generation {
			return
		}
		if post("/__dshwork/automation/state", state) != nil {
			return
		}
		var wg sync.WaitGroup
		defer func() {
			cancel()
			wg.Wait()
			current := service.Snapshot()
			if current.Generation == generation {
				service.Gateway(generation, false)
			}
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-run.Done():
					return
				case <-b.wake:
					if post("/__dshwork/automation/state", service.Snapshot()) != nil {
						cancel()
						return
					}
				}
			}
		}()
		resp, e = request(run, "GET", "/__dshwork/automation/events", nil)
		if e != nil {
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return
		}
		service.Gateway(generation, true)
		silence := time.AfterFunc(45*time.Second, cancel)
		defer silence.Stop()
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 4096), 65536)
		pending := map[string]context.CancelFunc{}
		var pendingMu sync.Mutex
		for scanner.Scan() {
			silence.Reset(45 * time.Second)
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			var message struct {
				ID      string  `json:"id"`
				Kind    string  `json:"kind"`
				Request Request `json:"request"`
			}
			if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &message) != nil {
				return
			}
			r := message.Request
			if r.Generation != generation {
				return
			}
			switch message.Kind {
			case "stop":
				service.StopSession(generation, r.Session, r.Lifetime)
			case "cancel":
				pendingMu.Lock()
				if c := pending[message.ID]; c != nil {
					c()
				}
				pendingMu.Unlock()
			case "call":
				pendingMu.Lock()
				if len(pending) >= 64 || pending[message.ID] != nil {
					pendingMu.Unlock()
					return
				}
				callCtx, stop := context.WithTimeout(run, 15*time.Second)
				pending[message.ID] = stop
				pendingMu.Unlock()
				wg.Add(1)
				go func(id string, r Request) {
					defer wg.Done()
					defer stop()
					value, err := service.Execute(callCtx, r)
					code := ""
					if err != nil {
						code = "AUTOMATION_CALL_FAILED"
						if errors.Is(err, ErrRevoked) {
							code = "AUTOMATION_REVOKED"
						}
						if errors.Is(err, ErrUnavailable) {
							code = "AUTOMATION_BACKEND_UNAVAILABLE"
						}
						if errors.Is(err, context.Canceled) {
							code = "AUTOMATION_CANCELLED"
						}
					}
					if post("/__dshwork/automation/reply", map[string]any{"id": id, "generation": generation, "value": value, "error": code}) != nil {
						cancel()
					}
					pendingMu.Lock()
					delete(pending, id)
					pendingMu.Unlock()
				}(message.ID, r)
			default:
				return
			}
		}
	}()
}
