//go:build darwin && cgo && !server

package desktopapp

/*
#cgo LDFLAGS: -framework Cocoa
#cgo e67presenter,e67previewfixture,!production CFLAGS: -DDSH_MAC_PREVIEW_SHUTDOWN=1
#include <stdint.h>
#include <stdlib.h>
int dsh_mac_download_shutdown(uintptr_t, const char*, const char*, const char*, const char*, int);
*/
import "C"

import (
	"context"
	"runtime/cgo"
	"unsafe"

	"github.com/local/dsh-work/internal/embeddedbrowser"
	"github.com/local/dsh-work/internal/settings"
	"github.com/wailsapp/wails/v3/pkg/application"
)

func askDownloadShutdownPlatform(ctx context.Context, _ *application.App, locale settings.Locale) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	title, wait, cancel, back := "Downloads are in progress", "Wait for downloads", "Cancel downloads", "Keep working"
	switch locale {
	case settings.LocaleChinese:
		title, wait, cancel, back = "有下载正在进行", "等待下载完成", "取消下载并继续", "返回工作台"
	case settings.LocaleJapanese:
		title, wait, cancel, back = "ダウンロード中です", "完了を待つ", "ダウンロードを中止して続行", "作業に戻る"
	}
	nativeTitle, nativeWait, nativeCancel, nativeBack := C.CString(title), C.CString(wait), C.CString(cancel), C.CString(back)
	defer C.free(unsafe.Pointer(nativeTitle))
	defer C.free(unsafe.Pointer(nativeWait))
	defer C.free(unsafe.Pointer(nativeCancel))
	defer C.free(unsafe.Pointer(nativeBack))
	handle := cgo.NewHandle(ctx)
	defer handle.Delete()
	fixtureChoice := macPreviewDownloadShutdownChoice()
	choice, err := application.InvokeSyncWithResultAndError(func() (int, error) {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		return int(C.dsh_mac_download_shutdown(C.uintptr_t(handle), nativeTitle, nativeWait, nativeCancel, nativeBack, C.int(fixtureChoice))), nil
	})
	if err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := macPreviewRecordDownloadShutdown(fixtureChoice, choice); err != nil {
		return false, err
	}
	switch choice {
	case 1:
		return false, nil
	case 2:
		return true, nil
	default:
		return false, embeddedbrowser.ErrDownloadShutdownDeclined
	}
}

//export goMacDownloadShutdownCanceled
func goMacDownloadShutdownCanceled(handle C.uintptr_t) C.int {
	if cgo.Handle(handle).Value().(context.Context).Err() != nil {
		return 1
	}
	return 0
}
