//go:build windows

package embeddedbrowser

import (
	"errors"
	"golang.org/x/sys/windows"
	"math"
	"os"
	"sync"
	"unsafe"
)

var presenterUser32 = windows.NewLazySystemDLL("user32.dll")

type presenterRECT struct{ left, top, right, bottom int32 }

func processHandle(peer PresenterPeer) (windows.Handle, error) {
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, peer.PID)
	if err != nil {
		return 0, err
	}
	var c, e, k, u windows.Filetime
	if err = windows.GetProcessTimes(handle, &c, &e, &k, &u); err != nil {
		windows.CloseHandle(handle)
		return 0, err
	}
	if uint64(c.HighDateTime)<<32|uint64(c.LowDateTime) != peer.Started {
		windows.CloseHandle(handle)
		return 0, errors.New("CEF_UI_PROCESS_INSTANCE_REJECTED")
	}
	return handle, nil
}
func presenterAlive(peer PresenterPeer) bool {
	h, e := processHandle(peer)
	if e != nil {
		return false
	}
	defer windows.CloseHandle(h)
	status, e := windows.WaitForSingleObject(h, 0)
	return e == nil && status == uint32(windows.WAIT_TIMEOUT)
}

type windowsPresenter struct {
	done, stop, joined chan struct{}
	once               sync.Once
}

func (p *windowsPresenter) Done() <-chan struct{} { return p.done }
func (p *windowsPresenter) Close() error          { p.once.Do(func() { close(p.stop) }); <-p.joined; return nil }
func admitPresenter(peer PresenterPeer, parent uintptr) (presenterOwner, error) {
	if peer.PID == 0 || parent == 0 {
		return nil, errors.New("CEF_UI_ADMISSION_REQUIRED")
	}
	h, err := processHandle(peer)
	if err != nil {
		return nil, err
	}
	var path [32768]uint16
	size := uint32(len(path))
	err = windows.QueryFullProcessImageName(h, 0, &path[0], &size)
	exe, e := os.Executable()
	actual, e2 := os.Stat(windows.UTF16ToString(path[:size]))
	expected, e3 := os.Stat(exe)
	if err != nil || e != nil || e2 != nil || e3 != nil || !os.SameFile(actual, expected) {
		windows.CloseHandle(h)
		return nil, errors.New("CEF_UI_EXECUTABLE_REJECTED")
	}
	if err = verifyPresenterWindow(peer, parent); err != nil {
		windows.CloseHandle(h)
		return nil, err
	}
	p := &windowsPresenter{done: make(chan struct{}), stop: make(chan struct{}), joined: make(chan struct{})}
	go func() {
		defer close(p.joined)
		defer close(p.done)
		defer windows.CloseHandle(h)
		for {
			status, e := windows.WaitForSingleObject(h, 100)
			if e != nil || status == windows.WAIT_OBJECT_0 {
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
func verifyPresenterWindow(peer PresenterPeer, parent uintptr) error {
	var pid uint32
	valid, _, _ := presenterUser32.NewProc("IsWindow").Call(parent)
	presenterUser32.NewProc("GetWindowThreadProcessId").Call(parent, uintptr(unsafe.Pointer(&pid)))
	root, _, _ := presenterUser32.NewProc("GetAncestor").Call(parent, 2)
	if valid == 0 || pid != peer.PID || root != parent {
		return errors.New("CEF_NATIVE_WINDOW_OWNER_REJECTED")
	}
	return nil
}
func presenterBounds(peer PresenterPeer, parent uintptr, b SlotBounds, zoom float64) (PixelBounds, error) {
	if err := verifyPresenterWindow(peer, parent); err != nil {
		return PixelBounds{}, err
	}
	if math.IsNaN(zoom) || math.IsInf(zoom, 0) || zoom < 0.25 || zoom > 5 {
		return PixelBounds{}, errors.New("CEF_ZOOM_REJECTED")
	}
	dpi, _, _ := presenterUser32.NewProc("GetDpiForWindow").Call(parent)
	if dpi == 0 {
		return PixelBounds{}, errors.New("CEF_DPI_UNAVAILABLE")
	}
	scale := float64(dpi) / 96 * zoom
	for _, v := range []float64{b.X, b.Y, b.Width, b.Height} {
		if math.IsNaN(v) || math.IsInf(v, 0) || math.Abs(v) > 1e6 {
			return PixelBounds{}, errors.New("CEF_SLOT_BOUNDS_REJECTED")
		}
	}
	var client presenterRECT
	ok, _, _ := presenterUser32.NewProc("GetClientRect").Call(parent, uintptr(unsafe.Pointer(&client)))
	if ok == 0 {
		return PixelBounds{}, errors.New("CEF_NATIVE_BOUNDS_UNAVAILABLE")
	}
	left := math.Min(float64(client.right), math.Max(0, b.X*scale))
	top := math.Min(float64(client.bottom), math.Max(40*scale, b.Y*scale))
	right := math.Min(float64(client.right), (b.X+b.Width)*scale)
	bottom := math.Min(float64(client.bottom), (b.Y+b.Height)*scale)
	return PixelBounds{X: int(math.Ceil(left)), Y: int(math.Ceil(top)), Width: max(0, int(math.Floor(right)-math.Ceil(left))), Height: max(0, int(math.Floor(bottom)-math.Ceil(top)))}, nil
}
