// GTK4 parent and owned subprocess lifecycle only. CEF runs in broker_linux.cc.
#include <X11/Xlib.h>
#include <atomic>
#include <cstdio>
#include <gdk/x11/gdkx.h>
#include <gtk/gtk.h>
#include <spawn.h>
#include <sstream>
#include <string>
#include <sys/wait.h>
#include <thread>
#include <unistd.h>

extern char **environ;
#define LAB_EXPORT extern "C" __attribute__((visibility("default")))
static std::string cache_path, broker_path, log_path, error;
static Window parent_window;
static Window slot_window;
static Display *slot_display = nullptr;
static int parent_depth = 0, slot_depth = 0;
static std::atomic<unsigned long> guest{0};
static std::atomic<bool> closed{false}, shutdown_returned{false},
    exited_cleanly{false};
static int command_fd = -1;
static std::thread reader;
LAB_EXPORT int lab_init(const char *cache, const char *helper,
                        const char *log) {
  cache_path = cache;
  broker_path = helper;
  log_path = log;
  return 1;
}
LAB_EXPORT int lab_create(void *native, const char *url) {
  GdkSurface *surface = gtk_native_get_surface(GTK_NATIVE(native));
  if (!surface || !GDK_IS_X11_SURFACE(surface)) {
    error = "A realized X11 parent is required";
    return 0;
  }
  parent_window = gdk_x11_surface_get_xid(surface);
  gdk_display_sync(gdk_surface_get_display(surface));
  // CEF's native X11 host chooses a non-ARGB visual. Give it an explicit
  // matching native slot instead of GTK4's potentially ARGB top-level visual.
  slot_display = XOpenDisplay(nullptr);
  XWindowAttributes parent_attributes{};
  if (!slot_display ||
      !XGetWindowAttributes(slot_display, parent_window, &parent_attributes)) {
    error = "GTK X11 parent is not published on the server";
    return 0;
  }
  parent_depth = parent_attributes.depth;
  int screen = DefaultScreen(slot_display);
  XSetWindowAttributes attributes{};
  attributes.colormap = DefaultColormap(slot_display, screen);
  attributes.border_pixel = 0;
  attributes.background_pixel = 0;
  slot_depth = DefaultDepth(slot_display, screen);
  slot_window = XCreateWindow(
      slot_display, parent_window, 240, 100, 700, 540, 0, slot_depth,
      InputOutput, DefaultVisual(slot_display, screen),
      CWColormap | CWBorderPixel | CWBackPixel, &attributes);
  XMapWindow(slot_display, slot_window);
  XSync(slot_display, False);
  XWindowAttributes slot_attributes{};
  if (!XGetWindowAttributes(slot_display, slot_window, &slot_attributes)) {
    error = "X11 native slot creation failed";
    return 0;
  }
  int input[2], output[2];
  if (pipe(input) != 0) {
    error = "broker input pipe failed";
    return 0;
  }
  if (pipe(output) != 0) {
    close(input[0]);
    close(input[1]);
    error = "broker output pipe failed";
    return 0;
  }
  posix_spawn_file_actions_t actions;
  posix_spawn_file_actions_init(&actions);
  posix_spawn_file_actions_adddup2(&actions, input[0], STDIN_FILENO);
  posix_spawn_file_actions_adddup2(&actions, output[1], STDOUT_FILENO);
  for (int fd : {input[0], input[1], output[0], output[1]})
    posix_spawn_file_actions_addclose(&actions, fd);
  std::string parent = "--parent-xid=" + std::to_string(slot_window),
              page = std::string("--url=") + url,
              cache = "--cache-path=" + cache_path,
              log = "--log-file=" + log_path;
  char *argv[] = {broker_path.data(), parent.data(), page.data(),
                  cache.data(),       log.data(),    nullptr};
  pid_t pid;
  int result =
      posix_spawn(&pid, broker_path.c_str(), &actions, nullptr, argv, environ);
  posix_spawn_file_actions_destroy(&actions);
  close(input[0]);
  close(output[1]);
  if (result) {
    close(input[1]);
    close(output[0]);
    error = "broker spawn failed: " + std::to_string(result);
    return 0;
  }
  command_fd = input[1];
  reader = std::thread([pid, fd = output[0]] {
    FILE *stream = fdopen(fd, "r");
    char *line = nullptr;
    size_t size = 0;
    while (getline(&line, &size, stream) > 0) {
      std::istringstream message(line);
      std::string event;
      message >> event;
      if (event == "CREATED") {
        unsigned long id = 0;
        message >> id;
        guest = id;
      } else if (event == "CLOSED")
        closed = true;
      else if (event == "SHUTDOWN")
        shutdown_returned = true;
    }
    free(line);
    fclose(stream);
    int status = 0;
    if (waitpid(pid, &status, 0) == pid)
      exited_cleanly = WIFEXITED(status) && WEXITSTATUS(status) == 0;
  });
  return 1;
}
LAB_EXPORT const char *lab_state() {
  static thread_local std::string state;
  bool attached = false;
  std::ostringstream ancestry;
  ancestry << "[";
  if (guest && !closed) {
    Display *display = XOpenDisplay(nullptr);
    Window current = guest;
    for (int i = 0; i < 16; i++) {
      Window root, parent, *children = nullptr;
      unsigned count = 0;
      bool ok = XQueryTree(display, current, &root, &parent, &children, &count);
      if (children)
        XFree(children);
      if (!ok)
        break;
      if (i) {
        ancestry << ",";
      }
      ancestry << parent;
      if (parent == parent_window) {
        attached = true;
        break;
      }
      if (!parent || parent == root || parent == current)
        break;
      current = parent;
    }
    XCloseDisplay(display);
  }
  ancestry << "]";
  std::ostringstream out;
  out << "{\"initialized\":true,\"processModel\":\"independent-cef-broker\","
         "\"created\":"
      << (guest ? 1 : 0) << ",\"closed\":" << (closed ? 1 : 0)
      << ",\"parentAttached\":" << (attached ? "true" : "false")
      << ",\"windowId\":\"" << parent_window << "\",\"guestId\":\"" << guest
      << "\",\"slotId\":\"" << slot_window
      << "\",\"parentDepth\":" << parent_depth
      << ",\"slotDepth\":" << slot_depth
      << ",\"guestAncestors\":" << ancestry.str() << "}";
  state = out.str();
  return state.c_str();
}
LAB_EXPORT void lab_close() {
  if (command_fd >= 0) {
    const char command[] = "close\n";
    if (write(command_fd, command, sizeof(command) - 1) < 0)
      error = "broker close command failed";
    close(command_fd);
    command_fd = -1;
  }
}
LAB_EXPORT int lab_closed() {
  return closed && shutdown_returned && exited_cleanly;
}
LAB_EXPORT void lab_shutdown() {
  if (reader.joinable())
    reader.join();
  if (slot_display) {
    XDestroyWindow(slot_display, slot_window);
    XSync(slot_display, False);
    XCloseDisplay(slot_display);
    slot_display = nullptr;
  }
}
LAB_EXPORT const char *lab_error() { return error.c_str(); }
