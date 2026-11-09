//go:build linux && cgo && e67presenter && !server

package desktopclient

/*
#cgo pkg-config: gtk4 x11-xcb xcb-res xcb-xfixes
#include <gtk/gtk.h>
#include <gdk/x11/gdkx.h>
#include <stdint.h>
#include "presenter_save_set_linux.h"
static int dsh_gtk_parent(void* value,uint64_t* xid,int* scale){
 if(!value||!GTK_IS_NATIVE(value))return 0;
 GdkSurface* surface=gtk_native_get_surface(GTK_NATIVE(value));
 if(!surface||!GDK_IS_X11_SURFACE(surface))return 0;
 *xid=gdk_x11_surface_get_xid(surface);*scale=gdk_surface_get_scale_factor(surface);
 gdk_display_sync(gdk_surface_get_display(surface));return *xid!=0&&*scale>0;
}
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"unsafe"

	"github.com/local/dsh-work/internal/localipc"
	"github.com/local/dsh-work/internal/logging"
	"github.com/wailsapp/wails/v3/pkg/application"
)

func presenterCapability(ctx context.Context, service *EmbeddedBrowserService) (bool, error) {
	// RegisterWindow obtains the actual trusted GTK XID, and the daemon checks
	// its XRes client/kernel instance before advertising native presentation.
	_, err := service.RegisterWindow(ctx)
	return err == nil, err
}

func presenterNativeWindow(window application.Window) (uint64, float64, error) {
	type geometry struct {
		id   uint64
		zoom float64
	}
	v, err := application.InvokeSyncWithResultAndError(func() (geometry, error) {
		var id C.uint64_t
		var scale C.int
		if C.dsh_gtk_parent(unsafe.Pointer(window.NativeWindow()), &id, &scale) != 1 {
			return geometry{}, errors.New("CEF_X11_PRESENTER_UNAVAILABLE")
		}
		return geometry{id: uint64(id), zoom: window.GetZoom() * float64(scale)}, nil
	})
	return v.id, v.zoom, err
}

// Protect only native CEF children of this actual GTK parent. The executable
// comes from the same trusted candidate configuration as daemon StartBroker,
// never from a frame/Agent field. Success precedes the Wails Update response.
func protectPresenterWindow(window application.Window) error {
	executable := os.Getenv("DSH_WORK_CEF_EXECUTABLE")
	if !filepath.IsAbs(executable) {
		return errors.New("CEF_X11_SAVE_SET_COMPONENT_REQUIRED")
	}
	started, err := localipc.ProcessStarted(uint32(os.Getpid()))
	if err != nil {
		return errors.New("CEF_X11_SAVE_SET_UI_INSTANCE_UNAVAILABLE")
	}
	return application.InvokeSyncWithError(func() error {
		path := C.CString(executable)
		defer C.free(unsafe.Pointer(path))
		var parent C.uint64_t
		var protected [4096]C.dsh_save_set_guest
		status := C.dsh_gtk_protect_cef(unsafe.Pointer(window.NativeWindow()), path, &parent, &protected[0], C.size_t(len(protected)))
		if status <= 0 {
			return fmt.Errorf("CEF_X11_SAVE_SET_REJECTED: %d", int(status))
		}
		type guest struct {
			ID      uint32 `json:"id"`
			PID     uint32 `json:"pid"`
			Started uint64 `json:"started"`
		}
		windows := make([]guest, int(status))
		for i := range windows {
			windows[i] = guest{uint32(protected[i].id), uint32(protected[i].pid), uint64(protected[i].started)}
		}
		logging.Module(logging.ModRuntime).Info("native CEF UI save-set ready", "pid", os.Getpid(), "created", started, "parent", uint64(parent), "protected", int(status), "windows", windows)
		return nil
	})
}
