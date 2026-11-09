// Environment probe, not a product presenter or authorization service.
#include <gtk/gtk.h>
#include <gdk/x11/gdkx.h>
#include <X11/extensions/XRes.h>
#include <stdio.h>
#include <unistd.h>

int main(void) {
  gtk_init();
  GtkWidget *window = gtk_window_new();
  gtk_window_set_default_size(GTK_WINDOW(window), 320, 200);
  // Realize without mapping: no screen-driving or user-visible test window.
  gtk_widget_realize(window);
  GdkSurface *surface = gtk_native_get_surface(GTK_NATIVE(window));
  if (!surface || !GDK_IS_X11_SURFACE(surface)) {
    puts("{\"qualified\":false,\"reason\":\"realized local X11 surface required\"}");
    gtk_window_destroy(GTK_WINDOW(window));
    return 2;
  }
  Display *display = gdk_x11_display_get_xdisplay(gdk_surface_get_display(surface));
  Window xid = gdk_x11_surface_get_xid(surface);
  gdk_display_sync(gdk_surface_get_display(surface));
  int event_base = 0, error_base = 0, major = 0, minor = 0;
  if (!XResQueryExtension(display, &event_base, &error_base) ||
      !XResQueryVersion(display, &major, &minor) ||
      major < 1 || (major == 1 && minor < 2)) {
    puts("{\"qualified\":false,\"reason\":\"XRes 1.2 client PID unavailable\"}");
    gtk_window_destroy(GTK_WINDOW(window));
    return 2;
  }
  XResClientIdSpec spec = {xid, XRES_CLIENT_ID_PID_MASK};
  long count = 0;
  XResClientIdValue *ids = NULL;
  pid_t actual = -1;
  // This newer request returns an X status (Success == 0), unlike the Bool
  // extension/version queries above.
  int query_status = XResQueryClientIds(display, 1, &spec, &count, &ids);
  if (query_status == Success) {
    for (long i = 0; i < count; ++i) {
      pid_t pid = XResGetClientPid(&ids[i]);
      if (pid > 0) actual = pid;
    }
  }
  if (ids) XResClientIdsDestroy(count, ids);
  printf("{\"qualified\":%s,\"fixture\":\"GTK4-realized-unmapped\","
         "\"pid\":%ld,\"xresPid\":%ld,\"xid\":%lu,\"queryStatus\":%d,\"xresVersion\":\"%d.%d\"}\n",
         actual == getpid() ? "true" : "false", (long)getpid(),
         (long)actual, xid, query_status, major, minor);
  gtk_window_destroy(GTK_WINDOW(window));
  gdk_display_sync(gdk_display_get_default());
  return actual == getpid() ? 0 : 2;
}
