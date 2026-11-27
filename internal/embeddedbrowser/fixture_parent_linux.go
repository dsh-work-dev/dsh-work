//go:build linux && cgo && e67nativefixture

package embeddedbrowser

/*
#cgo pkg-config: gtk4
#include <gtk/gtk.h>
#include <gdk/x11/gdkx.h>
static GtkWidget* fixture_parent;
static unsigned long fixture_create(){
 gtk_init();fixture_parent=gtk_window_new();gtk_window_set_default_size(GTK_WINDOW(fixture_parent),800,600);
 gtk_window_present(GTK_WINDOW(fixture_parent));gtk_widget_realize(fixture_parent);
 GdkSurface* s=gtk_native_get_surface(GTK_NATIVE(fixture_parent));
 if(!s||!GDK_IS_X11_SURFACE(s))return 0;
 gdk_display_sync(gdk_surface_get_display(s));return gdk_x11_surface_get_xid(s);
}
static void fixture_pump(){while(g_main_context_iteration(NULL,FALSE)) {}}
static void fixture_destroy(){if(fixture_parent){gtk_window_destroy(GTK_WINDOW(fixture_parent));fixture_parent=NULL;}fixture_pump();}
*/
import "C"

import (
	"runtime"
	"time"
)

// Test-build-only native parent, not a product host or execution endpoint.
func fixtureParent() (uintptr, func()) {
	ready := make(chan uintptr, 1)
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		defer close(done)
		ready <- uintptr(C.fixture_create())
		for {
			select {
			case <-stop:
				C.fixture_destroy()
				return
			case <-time.After(time.Millisecond):
				C.fixture_pump()
			}
		}
	}()
	return <-ready, func() { close(stop); <-done }
}
