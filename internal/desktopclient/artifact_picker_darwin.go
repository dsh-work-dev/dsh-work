//go:build darwin && cgo && !server

package desktopclient

/*
#include <stdlib.h>
#include "presenter_view_darwin.h"
*/
import "C"

import (
	"encoding/json"
	"errors"
	"net/url"
	"path/filepath"
	"runtime/cgo"
	"strings"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/local/dsh-work/internal/nativeui"
)

func (s *macViewSession) runMacArtifactPicker(id string) {
	_ = s.conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	request, err := s.read()
	if err != nil || request.Op != "artifact-picker" || request.ID != id || !request.Locale.Valid() {
		return
	}
	_ = s.conn.SetReadDeadline(time.Time{})
	closed := make(chan struct{})
	go func() {
		var cancel [1]byte
		_, _ = s.conn.Read(cancel[:])
		close(closed)
	}()
	labels := nativeui.ArtifactPickerFor(request.Locale)
	reply := macViewMessage{Op: "artifact-result", ID: id}
	picker := startMacFilePicker(s.host.handle, 0, labels.Title, labels.Open, "", false, false, true)
	if picker == nil {
		reply.Error = "CEF_ARTIFACT_PICKER_UNAVAILABLE"
	} else {
		defer picker.handle.Delete()
		select {
		case paths := <-picker.result:
			if len(paths) == 1 {
				reply.Path = paths[0]
			}
		case <-closed:
			picker.cancel()
			<-picker.result
		}
	}
	_ = s.write(reply)
}

type macFilePicker struct {
	handle   cgo.Handle
	result   chan []string
	canceled atomic.Bool
}

func newMacFilePickerResult() *macFilePicker {
	picker := &macFilePicker{result: make(chan []string, 1)}
	picker.handle = cgo.NewHandle(picker.result)
	return picker
}

func startMacFilePicker(window, view cgo.Handle, title, open, ext string, multiple, folder, html bool) *macFilePicker {
	nativeTitle, nativeOpen, nativeExt := C.CString(title), C.CString(open), C.CString(ext)
	defer C.free(unsafe.Pointer(nativeTitle))
	defer C.free(unsafe.Pointer(nativeOpen))
	defer C.free(unsafe.Pointer(nativeExt))
	flag := func(value bool) C.int {
		if value {
			return 1
		}
		return 0
	}
	picker := newMacFilePickerResult()
	if C.dsh_mac_file_picker_begin(C.uintptr_t(window), C.uintptr_t(view), C.uintptr_t(picker.handle), nativeTitle, nativeOpen, nativeExt, flag(multiple), flag(folder), flag(html)) != 1 {
		picker.handle.Delete()
		return nil
	}
	return picker
}

func (p *macFilePicker) cancel() {
	if p != nil && p.canceled.CompareAndSwap(false, true) {
		C.dsh_mac_file_picker_cancel(C.uintptr_t(p.handle))
	}
}

//export goMacFilePickerDone
func goMacFilePickerDone(handle C.uintptr_t, encoded *C.char) {
	result := cgo.Handle(handle).Value().(chan []string)
	var paths []string
	if encoded != nil {
		if err := json.Unmarshal([]byte(C.GoString(encoded)), &paths); err != nil {
			paths = nil
		}
	}
	result <- paths
}

func (s *macViewSession) openBrowserFilePicker(message macViewMessage) bool {
	if s.picker != nil || !s.attached || !s.visible || s.epoch != message.Epoch || message.Picker == "" ||
		len(message.Picker) > 32 || len(message.Origin) > 2048 || len(message.Extensions) > 128 {
		return false
	}
	origin, err := browserPickerOrigin(message.Origin)
	if err != nil {
		return false
	}
	var extensions []string
	for _, ext := range message.Extensions {
		ext = strings.TrimPrefix(ext, ".")
		if ext != "" && len(ext) <= 64 && strings.Trim(ext, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_+-") == "" {
			extensions = append(extensions, ext)
		}
	}
	picker := startMacFilePicker(s.host.handle, s.handle, "为 "+origin+" 选择文件", "", strings.Join(extensions, ";"), message.Multiple, message.Folder, false)
	if picker == nil {
		return false
	}
	s.picker, s.pickerID = picker, message.Picker
	go func() {
		paths := <-picker.result
		s.operations.Lock()
		if s.picker == picker {
			s.picker, s.pickerID = nil, ""
		}
		if picker.canceled.Load() || s.closed || !s.visible || s.epoch != message.Epoch {
			paths = nil
		}
		s.operations.Unlock()
		picker.handle.Delete()
		// Each selected path must be a native absolute path, and the complete
		// response must fit one presenter frame. Refuse a partial selection.
		if (!message.Multiple && len(paths) > 1) || len(paths) > 128 {
			paths = nil
		}
		for _, path := range paths {
			if !filepath.IsAbs(path) || strings.ContainsRune(path, 0) {
				paths = nil
				break
			}
		}
		reply := macViewMessage{Op: "picker-result", ID: message.ID, Picker: message.Picker, View: message.View, Epoch: message.Epoch, Paths: paths}
		if data, _ := json.Marshal(reply); len(data) > 16384 {
			reply.Paths = nil
		}
		if err := s.write(reply); err != nil {
			s.close()
		}
	}()
	return true
}

func browserPickerOrigin(value string) (string, error) {
	u, err := url.Parse(value)
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" ||
		u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(value, "\r\n\t") {
		return "", errors.New("CEF_MAC_PICKER_ORIGIN_REJECTED")
	}
	return u.Scheme + "://" + u.Host, nil
}
