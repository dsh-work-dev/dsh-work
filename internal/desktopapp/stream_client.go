package desktopapp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/local/dsh-work/internal/acquisition"
	"github.com/local/dsh-work/internal/daemon"
	"github.com/local/dsh-work/internal/hostevents"
	"github.com/local/dsh-work/internal/lifecycle"
	"github.com/local/dsh-work/internal/logging"
	"github.com/local/dsh-work/internal/settings"
)

const daemonStreamIdleTimeout = 30 * time.Second

var errDaemonStreamIdle = errors.New("daemon stream was silent for 30 seconds")

type clientStreamTimer interface {
	Chan() <-chan time.Time
	Stop() bool
	Reset(time.Duration) bool
}

type clientStreamClock interface {
	NewTimer(time.Duration) clientStreamTimer
}

type realClientStreamClock struct{}

func (realClientStreamClock) NewTimer(duration time.Duration) clientStreamTimer {
	return realClientStreamTimer{Timer: time.NewTimer(duration)}
}

type realClientStreamTimer struct{ *time.Timer }

func (t realClientStreamTimer) Chan() <-chan time.Time { return t.C }

type streamRead struct {
	line []byte
	err  error
}

func consumeDaemonStream(ctx context.Context, body io.ReadCloser, clock clientStreamClock, onMessage func(hostevents.Message)) error {
	defer body.Close()
	if clock == nil {
		clock = realClientStreamClock{}
	}
	lines := make(chan streamRead, 1)
	readerDone := make(chan struct{})
	go func() {
		scanner := bufio.NewScanner(body)
		scanner.Buffer(make([]byte, 4096), 1<<20)
		for scanner.Scan() {
			line := append([]byte(nil), scanner.Bytes()...)
			select {
			case lines <- streamRead{line: line}:
			case <-readerDone:
				return
			}
		}
		err := scanner.Err()
		if err == nil {
			err = io.EOF
		}
		select {
		case lines <- streamRead{err: err}:
		case <-readerDone:
		}
	}()
	defer close(readerDone)

	timer := clock.NewTimer(daemonStreamIdleTimeout)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.Chan():
			return errDaemonStreamIdle
		case result := <-lines:
			if result.err != nil {
				return result.err
			}
			_ = timer.Reset(daemonStreamIdleTimeout)
			var message hostevents.Message
			if err := json.Unmarshal(result.line, &message); err != nil {
				continue
			}
			if onMessage != nil {
				onMessage(message)
			}
		}
	}
}

type streamProcessor struct {
	current   *daemon.Snapshot
	currentMu *sync.Mutex
	emitEvent func(string, any)
	commands  *commandDispatcher

	lastPreferences *settings.Values
}

func (p *streamProcessor) handle(message hostevents.Message) {
	switch message.Type {
	case "ping":
		return
	case "state":
		p.handleState(message)
	case "command":
		data, known, err := commandData(message.Name, message.Data)
		if err != nil {
			attrs := append([]any{"command", message.Name}, logging.ErrAttrs(err, "")...)
			logging.Module(logging.ModIPC).Warn("daemon command decode failed", attrs...)
			return
		}
		if !known {
			return
		}
		if p.commands != nil {
			p.commands.dispatch(message, data)
		}
	}
}

func (p *streamProcessor) handleState(message hostevents.Message) {
	switch message.Topic {
	case "lifecycle":
		var value lifecycle.Status
		if p.decode(message, &value) == nil {
			p.setCurrent(func(state *daemon.Snapshot) { state.Status = value })
			p.emit("lifecycle", value)
		}
	case "worker-url":
		var value string
		if p.decode(message, &value) == nil {
			p.setCurrent(func(state *daemon.Snapshot) { state.URL = value })
			p.emit("worker-url", shellWorkerURL(value))
		}
	case "update":
		var value daemon.UpdateSnapshot
		if p.decode(message, &value) == nil {
			p.setCurrent(func(state *daemon.Snapshot) { state.Update = value })
			p.emit("update-state", value)
		}
	case "preferences":
		var value settings.Values
		if p.decode(message, &value) != nil {
			return
		}
		logging.SetVerbose(value.VerboseLogging)
		if previous := p.lastPreferences; previous != nil {
			if previous.Locale != value.Locale {
				p.emit("locale", value.Locale)
			}
			if previous.Appearance != value.Appearance {
				p.emit("appearance", value.Appearance)
			}
		}
		copy := value
		p.lastPreferences = &copy
		p.setCurrent(func(state *daemon.Snapshot) { state.Preferences = value })
	case "acquisition":
		var value acquisition.OperationStatus
		if p.decode(message, &value) == nil {
			p.emit("acquisition", value)
		}
	}
}

func (p *streamProcessor) decode(message hostevents.Message, target any) error {
	if err := json.Unmarshal(message.Value, target); err != nil {
		attrs := append([]any{"topic", message.Topic}, logging.ErrAttrs(err, "")...)
		logging.Module(logging.ModIPC).Warn("daemon state decode failed", attrs...)
		return err
	}
	return nil
}

func (p *streamProcessor) setCurrent(update func(*daemon.Snapshot)) {
	if p.current == nil || p.currentMu == nil {
		return
	}
	p.currentMu.Lock()
	update(p.current)
	p.currentMu.Unlock()
}

func (p *streamProcessor) emit(name string, value any) {
	if p.emitEvent != nil {
		p.emitEvent(name, value)
	}
}
