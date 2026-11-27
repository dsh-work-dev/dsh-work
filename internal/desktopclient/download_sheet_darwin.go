//go:build darwin && cgo && !server

package desktopclient

/*
#include <stdlib.h>
#include "presenter_view_darwin.h"
*/
import "C"

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unsafe"

	"github.com/local/dsh-work/internal/settings"
)

func (s *macViewSession) runMacDownloadSheet(operation, id string) {
	_ = s.conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	request, err := s.read()
	reply := macViewMessage{Op: "download-result", ID: id}
	if err != nil || request.Op != operation || request.ID != id || !request.Locale.Valid() ||
		request.Filename == "" || len(request.Filename) > 1024 ||
		filepath.Base(request.Filename) != request.Filename || strings.IndexFunc(request.Filename, unicode.IsControl) >= 0 ||
		len(request.Origin) > 2048 {
		reply.Error = "DOWNLOAD_MAC_SHEET_REQUEST_REJECTED"
		_ = s.write(reply)
		return
	}
	origin, err := browserPickerOrigin(request.Origin)
	if err != nil {
		reply.Error = "DOWNLOAD_MAC_SHEET_REQUEST_REJECTED"
		_ = s.write(reply)
		return
	}
	_ = s.conn.SetReadDeadline(time.Time{})
	closed := make(chan struct{})
	go func() {
		var cancel [1]byte
		_, _ = s.conn.Read(cancel[:])
		close(closed)
	}()
	title, detail, action, cancel := macDownloadCopy(request, origin)
	if title == "" {
		reply.Error = "DOWNLOAD_MAC_SHEET_REQUEST_REJECTED"
		_ = s.write(reply)
		return
	}
	var save C.int
	if operation == "download-save" {
		save = 1
	}
	nativeTitle, nativeDetail, nativeAction := C.CString(title), C.CString(detail), C.CString(action)
	nativeCancel, nativeName := C.CString(cancel), C.CString(request.Filename)
	defer C.free(unsafe.Pointer(nativeTitle))
	defer C.free(unsafe.Pointer(nativeDetail))
	defer C.free(unsafe.Pointer(nativeAction))
	defer C.free(unsafe.Pointer(nativeCancel))
	defer C.free(unsafe.Pointer(nativeName))
	result := newMacFilePickerResult()
	defer result.handle.Delete()
	if C.dsh_mac_download_sheet_begin(C.uintptr_t(s.host.handle), C.uintptr_t(result.handle), save, nativeTitle, nativeDetail, nativeAction, nativeCancel, nativeName) != 1 {
		reply.Error = "DOWNLOAD_MAC_SHEET_UNAVAILABLE"
	} else {
		select {
		case response := <-result.result:
			if len(response) == 1 {
				if save == 1 && filepath.IsAbs(response[0]) {
					reply.Path, reply.Error = s.host.macDownloadSavePath(response[0])
				}
				reply.Accepted = save == 0 && response[0] == "accepted"
			}
		case <-closed:
			C.dsh_mac_download_sheet_cancel(C.uintptr_t(result.handle))
			<-result.result
		}
	}
	_ = s.write(reply)
}

func (h *macWindowHost) macDownloadSavePath(path string) (string, string) {
	h.mu.Lock()
	target := h.fixtureDownloadTarget
	h.mu.Unlock()
	if target == "" {
		return path, ""
	}
	selected, err := filepath.EvalSymlinks(path)
	if err != nil || selected != target {
		return "", "MAC_FIXTURE_DOWNLOAD_DESTINATION_REJECTED"
	}
	return selected, ""
}

func macDownloadCopy(request macViewMessage, origin string) (title, body, action, cancel string) {
	var source, file, replace, risk, changed, replaceAction string
	switch request.Locale {
	case settings.LocaleChinese:
		title, action, cancel, replaceAction = "保存下载", "保存", "取消", "替换"
		source, file = "来源", "文件"
		replace = "替换现有文件？"
		risk = "此文件可能运行代码或更改设备设置。"
		changed = "文件内容与预期类型不同。仍要保存吗？"
	case settings.LocaleJapanese:
		title, action, cancel, replaceAction = "ダウンロードを保存", "保存", "キャンセル", "置き換える"
		source, file = "ダウンロード元", "ファイル"
		replace = "既存のファイルを置き換えますか？"
		risk = "このファイルはコードを実行したり、設定を変更したりする可能性があります。"
		changed = "ファイルの内容が予期された種類と異なります。保存しますか？"
	default:
		title, action, cancel, replaceAction = "Save download", "Save", "Cancel", "Replace"
		source, file = "Source", "File"
		replace = "Replace the existing file?"
		risk = "This file may run code or change device settings."
		changed = "The contents differ from the expected file type. Save anyway?"
	}
	body = fmt.Sprintf("%s: %s", source, origin)
	if request.Op == "download-save" {
		return
	}
	body += fmt.Sprintf("\n%s: %s", file, request.Filename)
	switch request.Op {
	case "download-content-risk":
		body = changed + "\n\n" + body + "\n\n" + risk
	case "download-confirm":
		if request.Overwrite {
			body = replace + "\n\n" + body
			action = replaceAction
		}
		if request.Risk {
			body += "\n\n" + risk
		}
		if !request.Overwrite && !request.Risk {
			title = ""
		}
	default:
		title = ""
	}
	return
}
