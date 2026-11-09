//go:build linux && cgo && e67presenter

package embeddedbrowser

/*
#cgo pkg-config: xcb xcb-res
#cgo CXXFLAGS: -std=c++20
#cgo LDFLAGS: -lstdc++
int dsh_x11_parent(unsigned long,unsigned int,unsigned long long,int*,int*);
*/
import "C"

import (
	"errors"
	"fmt"
	"github.com/local/dsh-work/internal/localipc"
	"golang.org/x/sys/unix"
	"math"
	"os"
	"sync"
)

type linuxPresenter struct {
	done, stop, joined chan struct{}
	once               sync.Once
}

func (p *linuxPresenter) Done() <-chan struct{} { return p.done }
func (p *linuxPresenter) Close() error          { p.once.Do(func() { close(p.stop) }); <-p.joined; return nil }
func processHandle(peer PresenterPeer) (int, error) {
	if peer.PID == 0 || peer.Started == 0 {
		return -1, errors.New("CEF_UI_PROCESS_INSTANCE_REJECTED")
	}
	fd, err := unix.PidfdOpen(int(peer.PID), 0)
	if err != nil {
		return -1, err
	}
	started, e := localipc.ProcessStarted(peer.PID)
	if e != nil || started != peer.Started || unix.PidfdSendSignal(fd, 0, nil, 0) != nil {
		unix.Close(fd)
		return -1, errors.New("CEF_UI_PROCESS_INSTANCE_REJECTED")
	}
	return fd, nil
}
func presenterAlive(peer PresenterPeer) bool {
	fd, e := processHandle(peer)
	if e != nil {
		return false
	}
	defer unix.Close(fd)
	poll := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	n, e := unix.Poll(poll, 0)
	return e == nil && n == 0
}
func parentBounds(peer PresenterPeer, parent uintptr) (int, int, error) {
	var width, height C.int
	if C.dsh_x11_parent(C.ulong(parent), C.uint(peer.PID), C.ulonglong(peer.Started), &width, &height) != 1 {
		return 0, 0, errors.New("CEF_X11_PRESENTER_UNAVAILABLE_OR_OWNER_REJECTED")
	}
	return int(width), int(height), nil
}
func admitPresenter(peer PresenterPeer, parent uintptr) (presenterOwner, error) {
	fd, e := processHandle(peer)
	if e != nil {
		return nil, e
	}
	self, err := os.Executable()
	actual, e1 := os.Stat(fmt.Sprintf("/proc/%d/exe", peer.PID))
	expected, e2 := os.Stat(self)
	if err != nil || e1 != nil || e2 != nil || !os.SameFile(actual, expected) {
		unix.Close(fd)
		return nil, errors.New("CEF_UI_EXECUTABLE_REJECTED")
	}
	if _, _, err = parentBounds(peer, parent); err != nil {
		unix.Close(fd)
		return nil, err
	}
	p := &linuxPresenter{done: make(chan struct{}), stop: make(chan struct{}), joined: make(chan struct{})}
	go func() {
		defer close(p.joined)
		defer close(p.done)
		defer unix.Close(fd)
		for {
			poll := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
			n, e := unix.Poll(poll, 100)
			if n > 0 || e != nil {
				return
			}
			select {
			case <-p.stop:
				return
			default:
			}
		}
	}()
	return p, nil
}
func presenterBounds(peer PresenterPeer, parent uintptr, b SlotBounds, zoom float64) (PixelBounds, error) {
	// Linux callers provide trusted GTK scale * WebView zoom, not Agent data.
	if math.IsNaN(zoom) || math.IsInf(zoom, 0) || zoom < 0.25 || zoom > 20 {
		return PixelBounds{}, errors.New("CEF_ZOOM_REJECTED")
	}
	for _, v := range []float64{b.X, b.Y, b.Width, b.Height} {
		if math.IsNaN(v) || math.IsInf(v, 0) || math.Abs(v) > 1e6 {
			return PixelBounds{}, errors.New("CEF_SLOT_BOUNDS_REJECTED")
		}
	}
	w, h, e := parentBounds(peer, parent)
	if e != nil {
		return PixelBounds{}, e
	}
	left := math.Min(float64(w), math.Max(0, b.X*zoom))
	top := math.Min(float64(h), math.Max(40*zoom, b.Y*zoom))
	right := math.Min(float64(w), (b.X+b.Width)*zoom)
	bottom := math.Min(float64(h), (b.Y+b.Height)*zoom)
	return PixelBounds{X: int(math.Ceil(left)), Y: int(math.Ceil(top)), Width: max(0, int(math.Floor(right)-math.Ceil(left))), Height: max(0, int(math.Floor(bottom)-math.Ceil(top)))}, nil
}
