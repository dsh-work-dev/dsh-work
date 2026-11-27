//go:build darwin && cgo && e67previewfixture && !server

package desktopclient

/*
#cgo CXXFLAGS: -DDSH_MAC_PREVIEW_FIXTURE=1
#cgo LDFLAGS: -framework UserNotifications
#include <stdlib.h>
#include "presenter_fixture_darwin.h"
*/
import "C"
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"
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
	if err == nil {
		value["inputTransport"] = s.inputTrace.snapshot()
		value["layoutPreviousEpoch"], value["layoutEpoch"] = s.inputTrace.layoutSnapshot()
	}
	return value, err
}
func MacPreviewInteract(window application.Window, lease string) error {
	s, err := macFixtureSession(window, lease)
	if err != nil {
		return err
	}
	s.operations.Lock()
	defer s.operations.Unlock()
	if s.closed || s.handle == 0 {
		return errors.New("MAC_FIXTURE_NATIVE_VIEW_MISSING")
	}
	switch C.dsh_mac_fixture_interact(C.uintptr_t(s.handle)) {
	case 1:
		return nil
	case -1:
		return errors.New("MAC_FIXTURE_NATIVE_VIEW_NOT_READY")
	case -2:
		return errors.New("MAC_FIXTURE_MOUSE_DID_NOT_FOCUS_VIEW")
	case -3:
		return errors.New("MAC_FIXTURE_SCROLL_EVENT_REJECTED")
	default:
		return errors.New("MAC_FIXTURE_OWNED_NSEVENT_FAILED")
	}
}
func MacPreviewClick(window application.Window, lease string, x, y float64) error {
	s, err := macFixtureSession(window, lease)
	if err != nil {
		return err
	}
	s.operations.Lock()
	defer s.operations.Unlock()
	if s.closed || s.handle == 0 {
		return errors.New("MAC_FIXTURE_OWNED_NSEVENT_FAILED")
	}
	switch C.dsh_mac_fixture_click(C.uintptr_t(s.handle), C.double(x), C.double(y)) {
	case 1:
		return nil
	case -1:
		return errors.New("MAC_FIXTURE_WINDOW_SHEET_ATTACHED")
	case -2:
		return errors.New("MAC_FIXTURE_POINT_DOES_NOT_HIT_CEF")
	default:
		return errors.New("MAC_FIXTURE_POINT_OUTSIDE_VISIBLE_CEF")
	}
}

func MacPreviewApplicationReopen() error {
	if C.dsh_mac_fixture_application_reopen() != 1 {
		return errors.New("MAC_FIXTURE_APPLICATION_REOPEN_UNAVAILABLE")
	}
	return nil
}

func MacPreviewNotificationDefault(category, allow, reject string, invokeDefault bool) error {
	nativeCategory, nativeAllow, nativeReject := C.CString(category), C.CString(allow), C.CString(reject)
	defer C.free(unsafe.Pointer(nativeCategory))
	defer C.free(unsafe.Pointer(nativeAllow))
	defer C.free(unsafe.Pointer(nativeReject))
	if result := C.dsh_mac_fixture_notification_default(nativeCategory, nativeAllow, nativeReject, C.int(boolInt(invokeDefault))); result != 1 {
		return fmt.Errorf("native notification category or default response unavailable (%d)", result)
	}
	return nil
}

func MacPreviewTrayAction(ctx context.Context, menuTitle string, trackMenu bool) error {
	var title *C.char
	if menuTitle != "" {
		title = C.CString(menuTitle)
		defer C.free(unsafe.Pointer(title))
	}
	var track C.int
	if trackMenu {
		track = 1
	}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		switch result := C.dsh_mac_fixture_tray_action(title, track); result {
		case 1:
			return nil
		case 2:
			select {
			case <-ticker.C:
			case <-ctx.Done():
				return ctx.Err()
			}
		default:
			return fmt.Errorf("native tray action unavailable (%d)", result)
		}
	}
}

type MacPreviewPetFrame struct {
	X, Y, Width, Height                 int
	AreaX, AreaY, AreaWidth, AreaHeight int
	Scale                               float64
}

func MacPreviewObservePetFrame(window application.Window) (MacPreviewPetFrame, error) {
	var frame C.DSHPetFrame
	if C.dsh_mac_fixture_pet_frame(window.NativeWindow(), &frame) != 1 {
		return MacPreviewPetFrame{}, errors.New("MAC_FIXTURE_PET_FRAME_UNAVAILABLE")
	}
	return MacPreviewPetFrame{
		X: int(frame.x), Y: int(frame.y), Width: int(frame.width), Height: int(frame.height),
		AreaX: int(frame.area_x), AreaY: int(frame.area_y), AreaWidth: int(frame.area_width), AreaHeight: int(frame.area_height),
		Scale: float64(frame.scale),
	}, nil
}

func MacPreviewPetDisplayChange(window application.Window, change int) error {
	if C.dsh_mac_fixture_pet_display_change(window.NativeWindow(), C.int(change)) != 1 {
		return errors.New("MAC_FIXTURE_PET_DISPLAY_EVENT_UNAVAILABLE")
	}
	return nil
}

func MacPreviewCancelDownloadSave(window application.Window) bool {
	value, err := application.InvokeSyncWithResultAndError(func() (bool, error) {
		return C.dsh_mac_fixture_cancel_download_save(window.NativeWindow()) == 1, nil
	})
	return err == nil && value
}

func MacPreviewDownloadSaveVisible(window application.Window) bool {
	value, err := application.InvokeSyncWithResultAndError(func() (bool, error) {
		return C.dsh_mac_fixture_download_save_visible(window.NativeWindow()) == 1, nil
	})
	return err == nil && value
}

func MacPreviewPinDownloadDestination(window application.Window, target string) error {
	path, err := filepath.EvalSymlinks(target)
	if err != nil {
		return err
	}
	id, _, err := presenterNativeWindow(window)
	if err != nil {
		return err
	}
	macWindows.Lock()
	host := macWindows.hosts[id]
	macWindows.Unlock()
	if host == nil {
		return errors.New("MAC_FIXTURE_NATIVE_HOST_MISSING")
	}
	host.mu.Lock()
	host.fixtureDownloadTarget = path
	host.mu.Unlock()
	return nil
}

func MacPreviewParent(window application.Window) (uint64, error) {
	parent, _, err := presenterNativeWindow(window)
	return parent, err
}

func MacPreviewDownloadConfirmationVisible(window application.Window) bool {
	return macPreviewDownloadConfirmation(window, -1)
}

func MacPreviewRespondDownloadConfirmation(window application.Window, accept bool) bool {
	var choice C.int
	if accept {
		choice = 1
	}
	return macPreviewDownloadConfirmation(window, choice)
}

func macPreviewDownloadConfirmation(window application.Window, choice C.int) bool {
	return application.InvokeSyncWithResult(func() bool {
		return C.dsh_mac_fixture_download_confirmation(window.NativeWindow(), choice) == 1
	})
}

func MacPreviewFilePickerVisible(window application.Window) bool {
	return application.InvokeSyncWithResult(func() bool {
		return C.dsh_mac_fixture_file_picker_visible(window.NativeWindow()) == 1
	})
}

func MacPreviewCheckFilePickerFilter(window application.Window) error {
	result := application.InvokeSyncWithResult(func() C.int {
		return C.dsh_mac_fixture_file_picker_filter(window.NativeWindow())
	})
	if result != 1 {
		return fmt.Errorf("native upload file filter switch failed (%d)", result)
	}
	return nil
}

func MacPreviewCancelWorkspacePicker(window application.Window) bool {
	return application.InvokeSyncWithResult(func() bool {
		return C.dsh_mac_fixture_cancel_workspace_picker(window.NativeWindow()) == 1
	})
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
