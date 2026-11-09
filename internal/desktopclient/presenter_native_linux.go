//go:build linux && cgo && e67presenter && !server

package desktopclient

/*
#cgo pkg-config: gtk4
#include <gtk/gtk.h>
#include <gdk/x11/gdkx.h>
#include <stdint.h>
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
	"errors"
	"github.com/wailsapp/wails/v3/pkg/application"
	"unsafe"
)

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
