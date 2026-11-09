package devrunner

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"time"
)

type Request struct {
	Generation string `json:"generation"`
	Action     string `json:"action"`
	Instance   int    `json:"instance,omitempty"`
	Deadline   int64  `json:"deadline"`
}
type ProcessState struct {
	PID             uint32 `json:"pid,omitempty"`
	Started         uint64 `json:"started,omitempty"`
	Alive           bool   `json:"alive"`
	IPCReady        bool   `json:"ipcReady"`
	Ready           bool   `json:"ready"`
	ReadyAt         string `json:"readyAt,omitempty"`
	TreeOwned       bool   `json:"treeOwned"`
	ActiveProcesses uint32 `json:"activeProcesses"`
	ExitCode        *int   `json:"exitCode,omitempty"`
}
type Status struct {
	Generation string               `json:"generation"`
	PID        int                  `json:"pid"`
	Workspace  string               `json:"workspace"`
	Tools      map[string]string    `json:"tools"`
	Active     string               `json:"active,omitempty"`
	Build      string               `json:"build"`
	BuildLog   string               `json:"buildLog,omitempty"`
	LastError  string               `json:"lastError,omitempty"`
	Daemon     ProcessState         `json:"daemon"`
	UIs        map[int]ProcessState `json:"uis"`
}
type Response struct {
	Status Status `json:"status"`
	Error  string `json:"error,omitempty"`
}
type Backend interface {
	Execute(context.Context, string, int) error
	Status(context.Context) Status
}
type Service struct {
	Generation string
	Backend    Backend
	serial     chan struct{}
	stopped    atomic.Bool
	Stop       chan struct{}
	exitErr    error
}

func NewService(b Backend) *Service {
	return &Service{Generation: rand.Text(), Backend: b, serial: make(chan struct{}, 1), Stop: make(chan struct{})}
}
func validate(r Request, now time.Time) error {
	switch r.Action {
	case "status", "build", "shutdown":
		if r.Instance != 0 {
			return errors.New("instance is not allowed for this command")
		}
	case "start", "restart":
		if r.Instance != 1 && r.Instance != 2 {
			return errors.New("instance must be 1 or 2")
		}
	case "stop":
		if r.Instance < 0 || r.Instance > 2 {
			return errors.New("instance must be 1, 2, or all")
		}
	default:
		return errors.New("unknown fixed command")
	}
	if r.Deadline <= now.UnixMilli() || r.Deadline > now.Add(3*time.Minute).UnixMilli() {
		return errors.New("request deadline is expired or exceeds three minutes")
	}
	return nil
}
func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" || r.URL.Path != "/command" || r.Header.Get("Origin") != "" {
		http.Error(w, "fixed local command required", 400)
		return
	}
	var req Request
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		http.Error(w, "invalid command fields", 400)
		return
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		http.Error(w, "one command required", 400)
		return
	}
	if req.Generation != s.Generation {
		http.Error(w, "runner generation changed", 409)
		return
	}
	if err := validate(req, time.Now()); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	ctx, cancel := context.WithDeadline(r.Context(), time.UnixMilli(req.Deadline))
	defer cancel()
	var err error
	if req.Action != "status" {
		select {
		case s.serial <- struct{}{}:
			defer func() { <-s.serial }()
		case <-ctx.Done():
			http.Error(w, "request canceled before execution", 408)
			return
		}
		if ctx.Err() != nil || time.Now().UnixMilli() >= req.Deadline {
			http.Error(w, "request expired before execution", 408)
			return
		}
		if s.stopped.Load() {
			http.Error(w, "runner is stopping", 409)
			return
		}
		err = s.Backend.Execute(ctx, req.Action, req.Instance)
	}
	statusCtx, stop := context.WithTimeout(context.Background(), 8*time.Second)
	defer stop()
	status := s.Backend.Status(statusCtx)
	status.Generation = s.Generation
	response := Response{Status: status}
	if err != nil {
		response.Error = err.Error()
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
	complete := err == nil
	if terminal, ok := s.Backend.(interface{ CleanupComplete() bool }); ok && req.Action == "shutdown" {
		complete = terminal.CleanupComplete()
	}
	if req.Action == "shutdown" && complete && s.stopped.CompareAndSwap(false, true) {
		s.exitErr = err
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		close(s.Stop)
	}
}
func (s *Service) Shutdown(ctx context.Context) error {
	select {
	case s.serial <- struct{}{}:
		defer func() { <-s.serial }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if s.stopped.Load() {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	err := s.Backend.Execute(ctx, "shutdown", 0)
	complete := err == nil
	if terminal, ok := s.Backend.(interface{ CleanupComplete() bool }); ok {
		complete = terminal.CleanupComplete()
	}
	if !complete {
		return fmt.Errorf("runner remains available for cleanup retry: %w", err)
	}
	if s.stopped.CompareAndSwap(false, true) {
		s.exitErr = err
		close(s.Stop)
	}
	return err
}
