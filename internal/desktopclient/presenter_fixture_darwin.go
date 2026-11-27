//go:build darwin && cgo && e67previewfixture && !server

package desktopclient

/*
#cgo CXXFLAGS: -DDSH_MAC_PREVIEW_FIXTURE=1
#include <stdlib.h>
#include "presenter_view_darwin.h"
*/
import "C"
import (
	"encoding/json"
	"errors"
	"github.com/wailsapp/wails/v3/pkg/application"
	"unsafe"
)

func macFixtureSession(window application.Window, lease string) (*macViewSession, error) {
	id, _, err := presenterNativeWindow(window)
	if err != nil {
		return nil, err
	}
	macWindows.Lock()
	host := macWindows.hosts[id]
	macWindows.Unlock()
	if host == nil {
		return nil, errors.New("MAC_FIXTURE_NATIVE_HOST_MISSING")
	}
	host.mu.Lock()
	sessions := make([]*macViewSession, 0, len(host.sessions))
	for session := range host.sessions {
		sessions = append(sessions, session)
	}
	host.mu.Unlock()
	for _, session := range sessions {
		session.operations.Lock()
		session.mu.Lock()
		match := session.attached && !session.closed && session.view == lease
		session.mu.Unlock()
		session.operations.Unlock()
		if match {
			return session, nil
		}
	}
	return nil, errors.New("MAC_FIXTURE_NATIVE_VIEW_MISSING")
}

// MacPreviewObserve reports only the actual owned view tree, not a CDP image.
func MacPreviewObserve(window application.Window, lease string) (map[string]any, error) {
	s, err := macFixtureSession(window, lease)
	if err != nil {
		return nil, err
	}
	s.operations.Lock()
	defer s.operations.Unlock()
	if s.closed || s.handle == 0 {
		return nil, errors.New("MAC_FIXTURE_NATIVE_VIEW_MISSING")
	}
	data := C.dsh_mac_fixture_observe(C.uintptr_t(s.handle))
	if data == nil {
		return nil, errors.New("MAC_FIXTURE_NATIVE_OBSERVATION_FAILED")
	}
	defer C.free(unsafe.Pointer(data))
	var value map[string]any
	err = json.Unmarshal([]byte(C.GoString(data)), &value)
	return value, err
}
func MacPreviewInteract(window application.Window, lease string) error {
	s, err := macFixtureSession(window, lease)
	if err != nil {
		return err
	}
	s.operations.Lock()
	defer s.operations.Unlock()
	if s.closed || s.handle == 0 || C.dsh_mac_fixture_interact(C.uintptr_t(s.handle)) != 1 {
		return errors.New("MAC_FIXTURE_OWNED_NSEVENT_FAILED")
	}
	return nil
}
func MacPreviewRender(window application.Window, lease, path string) error {
	s, err := macFixtureSession(window, lease)
	if err != nil {
		return err
	}
	s.operations.Lock()
	defer s.operations.Unlock()
	if s.closed || s.handle == 0 {
		return errors.New("MAC_FIXTURE_NATIVE_VIEW_MISSING")
	}
	target := C.CString(path)
	defer C.free(unsafe.Pointer(target))
	if C.dsh_mac_fixture_render(C.uintptr_t(s.handle), target) != 1 {
		return errors.New("MAC_FIXTURE_APPKIT_RENDER_FAILED")
	}
	return nil
}
