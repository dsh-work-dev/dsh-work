//go:build darwin && cgo && !server

package desktopclient

/*
#cgo CXXFLAGS: -std=c++20 -x objective-c++
#cgo LDFLAGS: -framework Cocoa -framework QuartzCore
#include <stdlib.h>
#include "presenter_view_darwin.h"
*/
import "C"

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"path/filepath"
	"runtime/cgo"
	"sync"
	"time"
	"unsafe"

	"github.com/local/dsh-work/internal/embeddedbrowser"
	"github.com/local/dsh-work/internal/embeddedbrowser/macowner"
	"github.com/local/dsh-work/internal/localipc"
	"github.com/local/dsh-work/internal/workeripc"
	"github.com/wailsapp/wails/v3/pkg/application"
)

const macFrameHeaderBytes = 32
const macFrameMagic = uint64(0x4453484d41434631)
const macFrameBudget = 256 << 20

var macWindows = struct {
	sync.Mutex
	hosts map[uint64]*macWindowHost
}{hosts: map[uint64]*macWindowHost{}}

type macWindowHost struct {
	mu                                     sync.Mutex
	native                                 unsafe.Pointer
	id                                     uint64
	handle                                 cgo.Handle
	listener                               net.Listener
	directory, endpoint, token, executable string
	sessions                               map[*macViewSession]bool
	closed                                 bool
	once                                   sync.Once
}
type macViewMessage struct {
	Op                  string                      `json:"op"`
	ID                  string                      `json:"id,omitempty"`
	View                string                      `json:"view,omitempty"`
	Epoch               string                      `json:"epoch,omitempty"`
	Browser             int                         `json:"browser,omitempty"`
	Bounds              embeddedbrowser.PixelBounds `json:"bounds"`
	Visible             bool                        `json:"visible"`
	Path                string                      `json:"path,omitempty"`
	Bytes               int                         `json:"bytes,omitempty"`
	Scale               float64                     `json:"scale,omitempty"`
	Error               string                      `json:"error,omitempty"`
	From, To            int
	X, Y, Width, Height float64
}
type macViewSession struct {
	operations                sync.Mutex
	capacity                  int
	mu                        sync.Mutex
	host                      *macWindowHost
	conn                      net.Conn
	handle                    cgo.Handle
	view, epoch, path         string
	attached, visible, closed bool
	writeMu                   sync.Mutex
	inputs                    chan []byte
	done                      chan struct{}
	once                      sync.Once
}

func presenterNativeWindow(window application.Window) (uint64, float64, error) {
	type geometry struct {
		id   uint64
		zoom float64
	}
	value, err := application.InvokeSyncWithResultAndError(func() (geometry, error) {
		var id C.uint64_t
		var scale, width, height C.double
		if C.dsh_mac_window(unsafe.Pointer(window.NativeWindow()), &id, &scale, &width, &height) != 1 {
			return geometry{}, errors.New("CEF_MAC_WINDOW_UNAVAILABLE")
		}
		return geometry{uint64(id), window.GetZoom()}, nil
	})
	return value.id, value.zoom, err
}
func presenterCapability(ctx context.Context, service *EmbeddedBrowserService) (bool, error) {
	_, err := service.RegisterWindow(ctx)
	return err == nil, err
}
func preparePresenterWindow(ctx context.Context, service *EmbeddedBrowserService, window application.Window) error {
	id, _, err := presenterNativeWindow(window)
	if err != nil {
		return err
	}
	macWindows.Lock()
	host := macWindows.hosts[id]
	if host == nil {
		host, err = newMacWindowHost(ctx, window, id)
		if err == nil {
			macWindows.hosts[id] = host
		}
	}
	macWindows.Unlock()
	if err != nil {
		return err
	}
	return call(ctx, service.Client, "EmbeddedBrowserService", "ConfigureMacPresenter", []any{id, host.endpoint, host.token}, nil)
}
func newMacWindowHost(ctx context.Context, window application.Window, id uint64) (*macWindowHost, error) {
	publication, err := macowner.OpenPublication(ctx, os.Getenv("DSH_WORK_CEF_MAC_PUBLICATION"))
	if err != nil {
		return nil, err
	}
	directory, err := os.MkdirTemp(localipc.SocketTempRoot(), "dsh-mac-view-")
	if err != nil {
		return nil, err
	}
	host := &macWindowHost{id: id, native: unsafe.Pointer(window.NativeWindow()), directory: directory, endpoint: filepath.Join(directory, "ui.sock"), token: rand.Text() + rand.Text(), executable: publication.Executable, sessions: map[*macViewSession]bool{}}
	host.listener, err = localipc.Listen(host.endpoint, true)
	if err != nil {
		os.Remove(directory)
		return nil, err
	}
	host.handle = cgo.NewHandle(host)
	if C.dsh_mac_window_watch(host.native, C.uintptr_t(host.handle)) != 1 {
		host.listener.Close()
		host.handle.Delete()
		os.Remove(directory)
		return nil, errors.New("CEF_MAC_WINDOW_UNAVAILABLE")
	}
	go host.accept()
	return host, nil
}
func (h *macWindowHost) accept() {
	for {
		connection, err := h.listener.Accept()
		if err != nil {
			return
		}
		h.mu.Lock()
		if h.closed || len(h.sessions) >= 64 {
			h.mu.Unlock()
			connection.Close()
			continue
		}
		s := &macViewSession{host: h, conn: connection, inputs: make(chan []byte, 128), done: make(chan struct{})}
		h.sessions[s] = true
		h.mu.Unlock()
		go s.run()
	}
}
func (h *macWindowHost) close() {
	h.once.Do(func() {
		h.mu.Lock()
		h.closed = true
		sessions := make([]*macViewSession, 0, len(h.sessions))
		for session := range h.sessions {
			sessions = append(sessions, session)
		}
		h.mu.Unlock()
		for _, session := range sessions {
			session.close()
		}
		h.listener.Close()
		C.dsh_mac_window_unwatch(C.uintptr_t(h.handle))
		h.handle.Delete()
		macWindows.Lock()
		if macWindows.hosts[h.id] == h {
			delete(macWindows.hosts, h.id)
		}
		macWindows.Unlock()
		os.Remove(h.directory)
	})
}
func (s *macViewSession) close() {
	s.once.Do(func() {
		s.operations.Lock()
		defer s.operations.Unlock()
		s.mu.Lock()
		s.closed = true
		s.visible = false
		s.mu.Unlock()
		close(s.done)
		s.conn.Close()
		if s.handle != 0 {
			C.dsh_mac_view_close(C.uintptr_t(s.handle))
			s.handle.Delete()
		}
		if s.path != "" {
			os.Remove(s.path)
		}
		s.host.mu.Lock()
		delete(s.host.sessions, s)
		s.host.mu.Unlock()
	})
}
func (s *macViewSession) write(value any) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	data, err := json.Marshal(value)
	if err != nil || len(data) > 16384 {
		return errors.New("CEF_MAC_VIEW_FRAME_REJECTED")
	}
	var header [4]byte
	binary.LittleEndian.PutUint32(header[:], uint32(len(data)))
	s.conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
	defer s.conn.SetWriteDeadline(time.Time{})
	for _, part := range [][]byte{header[:], data} {
		for len(part) > 0 {
			n, e := s.conn.Write(part)
			if e != nil {
				return e
			}
			if n == 0 {
				return io.ErrShortWrite
			}
			part = part[n:]
		}
	}
	return nil
}
func (s *macViewSession) read() (macViewMessage, error) {
	var value macViewMessage
	var header [4]byte
	if _, err := io.ReadFull(s.conn, header[:]); err != nil {
		return value, err
	}
	n := binary.LittleEndian.Uint32(header[:])
	if n == 0 || n > 16384 {
		return value, errors.New("CEF_MAC_VIEW_FRAME_REJECTED")
	}
	data := make([]byte, n)
	if _, err := io.ReadFull(s.conn, data); err != nil {
		return value, err
	}
	return value, json.Unmarshal(data, &value)
}
func (s *macViewSession) run() {
	defer s.close()
	peer, err := localipc.Peer(s.conn)
	if err != nil {
		return
	}
	executable := C.CString(s.host.executable)
	matched := C.dsh_mac_process_matches(C.uint32_t(peer.PID), executable) == 1
	C.free(unsafe.Pointer(executable))
	if !matched {
		// A same-app daemon may subscribe only to this NSWindow's lifetime. It
		// cannot attach a frame or send input, and receives no presenter credential.
		own, err := os.Executable()
		if err != nil {
			return
		}
		path := C.CString(own)
		sameApp := C.dsh_mac_process_matches(C.uint32_t(peer.PID), path) == 1
		C.free(unsafe.Pointer(path))
		if !sameApp {
			return
		}
		s.conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		message, err := s.read()
		if err != nil || message.Op != "watch" || message.ID != fmt.Sprint(s.host.id) {
			return
		}
		if s.write(macViewMessage{Op: "ack", ID: message.ID}) != nil {
			return
		}
		s.conn.SetReadDeadline(time.Time{})
		var byte [1]byte
		_, _ = s.conn.Read(byte[:])
		return
	}
	if err = workeripc.AuthenticateServer(s.conn, s.host.token); err != nil {
		return
	}
	if _, err = localipc.Peer(s.conn); err != nil {
		return
	}
	go func() {
		for {
			select {
			case data := <-s.inputs:
				if err := s.write(json.RawMessage(data)); err != nil {
					s.close()
					return
				}
			case <-s.done:
				return
			}
		}
	}()
	for {
		message, err := s.read()
		if err != nil {
			return
		}
		response := macViewMessage{Op: "ack", ID: message.ID, View: message.View, Epoch: message.Epoch}
		if message.ID == "" || len(message.ID) > 64 || len(message.View) < 16 || len(message.View) > 128 || len(message.Epoch) > 32 {
			return
		}
		s.mu.Lock()
		valid := !s.closed && (s.view == "" || s.view == message.View)
		s.mu.Unlock()
		if !valid {
			return
		}
		s.operations.Lock()
		switch message.Op {
		case "present":
			response.Path, response.Bytes, response.Scale, err = s.present(message)
			if err == nil {
				response.Visible = C.dsh_mac_view_visible(C.uintptr_t(s.handle)) == 1
			}
		case "state":
			s.mu.Lock()
			valid := s.attached && s.epoch == message.Epoch && !s.closed
			s.mu.Unlock()
			if !valid {
				err = errors.New("CEF_MAC_VIEW_RETIRED")
			} else {
				response.Visible = C.dsh_mac_view_visible(C.uintptr_t(s.handle)) == 1
			}
		case "hide":
			s.mu.Lock()
			s.epoch = message.Epoch
			s.visible = false
			s.mu.Unlock()
			if !s.attached || C.dsh_mac_view_update(C.uintptr_t(s.handle), 0, 0, 0, 0, 0) != 1 {
				err = errors.New("CEF_MAC_VIEW_UNAVAILABLE")
			}
		case "ime":
			if s.attached {
				C.dsh_mac_view_ime(C.uintptr_t(s.handle), C.int(message.From), C.int(message.To), C.double(message.X), C.double(message.Y), C.double(message.Width), C.double(message.Height))
			}
		default:
			s.operations.Unlock()
			return
		}
		s.operations.Unlock()
		if err != nil {
			response.Error = "CEF_MAC_VIEW_UNAVAILABLE"
		}
		if s.write(response) != nil {
			return
		}
		if err != nil {
			return
		}
	}
}
func (s *macViewSession) present(message macViewMessage) (string, int, float64, error) {
	b := message.Bounds
	var id C.uint64_t
	var scale, width, height C.double
	if C.dsh_mac_window(s.host.native, &id, &scale, &width, &height) != 1 || uint64(id) != s.host.id {
		return "", 0, 0, errors.New("CEF_MAC_WINDOW_UNAVAILABLE")
	}
	dpr := float64(scale)
	if b.X < 0 || b.Y < 40 || b.Width < 0 || b.Height < 0 || float64(b.X+b.Width) > float64(width)+1 || float64(b.Y+b.Height) > float64(height)+1 || dpr < 1 || dpr > 4 {
		return "", 0, 0, errors.New("CEF_MAC_VIEW_BOUNDS_REJECTED")
	}
	capacity := int64(math.Ceil(float64(max(1, b.Width))*dpr)) * int64(math.Ceil(float64(max(1, b.Height))*dpr)) * 4
	if capacity <= 0 || capacity > macFrameBudget {
		return "", 0, 0, errors.New("CEF_MAC_VIEW_BUDGET_EXCEEDED")
	}
	size := int(capacity) + macFrameHeaderBytes

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return "", 0, 0, errors.New("CEF_MAC_VIEW_CLOSED")
	}
	s.view = message.View
	s.epoch = message.Epoch
	s.visible = message.Visible
	s.mu.Unlock()
	if s.attached && size <= s.capacity {
		if C.dsh_mac_view_update(C.uintptr_t(s.handle), C.double(b.X), C.double(b.Y), C.double(b.Width), C.double(b.Height), C.int(boolInt(message.Visible))) != 1 {
			return "", 0, 0, errors.New("CEF_MAC_VIEW_UNAVAILABLE")
		}
		return s.path, s.capacity, dpr, nil
	}
	file, err := os.CreateTemp(s.host.directory, "frame-")
	if err != nil {
		return "", 0, 0, err
	}
	pathName := file.Name()
	keep := false
	defer func() {
		if !keep {
			os.Remove(pathName)
		}
	}()
	if err = file.Truncate(int64(size)); err == nil {
		var header [macFrameHeaderBytes]byte
		binary.LittleEndian.PutUint64(header[:], macFrameMagic)
		binary.LittleEndian.PutUint64(header[16:], uint64(capacity))
		_, err = file.WriteAt(header[:], 0)
	}
	file.Close()
	if err != nil {
		return "", 0, 0, err
	}
	path := C.CString(pathName)
	defer C.free(unsafe.Pointer(path))
	if s.attached {
		if C.dsh_mac_view_replace_frame(C.uintptr_t(s.handle), path, C.size_t(size)) != 1 {
			return "", 0, 0, errors.New("CEF_MAC_VIEW_UNAVAILABLE")
		}
		if C.dsh_mac_view_update(C.uintptr_t(s.handle), C.double(b.X), C.double(b.Y), C.double(b.Width), C.double(b.Height), C.int(boolInt(message.Visible))) != 1 {
			return "", 0, 0, errors.New("CEF_MAC_VIEW_UNAVAILABLE")
		}
		os.Remove(s.path)
	} else {
		s.handle = cgo.NewHandle(s)
		if C.dsh_mac_view_attach(s.host.native, C.uintptr_t(s.handle), path, C.size_t(size), C.double(b.X), C.double(b.Y), C.double(b.Width), C.double(b.Height), C.int(boolInt(message.Visible))) != 1 {
			return "", 0, 0, errors.New("CEF_MAC_VIEW_UNAVAILABLE")
		}
		s.attached = true
	}
	s.path = pathName
	s.capacity = size
	keep = true

	return s.path, size, dpr, nil
}
func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

//export goMacPresenterInput
func goMacPresenterInput(handle C.uintptr_t, data *C.char, size C.int) {
	if size <= 0 || size > 16384 {
		return
	}
	s, ok := cgo.Handle(handle).Value().(*macViewSession)
	if !ok {
		return
	}
	s.mu.Lock()
	if s.closed || !s.visible {
		s.mu.Unlock()
		return
	}
	view, epoch := s.view, s.epoch
	s.mu.Unlock()
	var message map[string]any
	if json.Unmarshal(C.GoBytes(unsafe.Pointer(data), size), &message) != nil {
		return
	}
	message["view"] = view
	message["epoch"] = epoch
	payload, err := json.Marshal(message)
	if err != nil {
		return
	}
	select {
	case s.inputs <- payload:
	default:
		go s.close()
	}
}

//export goMacPresenterWindowClosed
func goMacPresenterWindowClosed(handle C.uintptr_t) {
	host, ok := cgo.Handle(handle).Value().(*macWindowHost)
	if ok {
		go host.close()
	}
}
