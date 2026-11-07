// GTK4 slots and a retained broker PID; no CEF main context inside Wails.
#include <X11/Xlib.h>
#include <array>
#include <atomic>
#include <csignal>
#include <cstdio>
#include <fcntl.h>
#include <gdk/x11/gdkx.h>
#include <gtk/gtk.h>
#include <mutex>
#include <spawn.h>
#include <sstream>
#include <string>
#include <sys/wait.h>
#include <thread>
#include <unistd.h>
#include <vector>
#include <xcb/xcb.h>
extern char **environ;
#define LAB_EXPORT extern "C" __attribute__((visibility("default")))
static std::string cache_path, broker_path, log_path, last_error;
static Window main_id = 0, float_id = 0;
static std::array<Window, 2> slots{};
static Display *display = nullptr;
static xcb_connection_t *query = nullptr;
static std::array<std::atomic<unsigned long>, 2> guests{};
static std::array<std::atomic<int>, 2> guest_dpi{};
static int main_dpi = 0, float_dpi = 0;
static GtkWidget *main_widget = nullptr;
static GtkWidget *FindWebView(GtkWidget *widget) {
  if (std::string(G_OBJECT_TYPE_NAME(widget)).find("WebKitWebView") !=
      std::string::npos)
    return widget;
  for (auto child = gtk_widget_get_first_child(widget); child;
       child = gtk_widget_get_next_sibling(child))
    if (auto result = FindWebView(child))
      return result;
  return nullptr;
}
static std::array<std::atomic<bool>, 2> closed{};
static std::atomic<bool> shutdown_returned{false}, exited_cleanly{false},
    alive{false};
static std::mutex popup_mutex;
static std::vector<unsigned long> popups;
static std::array<bool, 2> faults{};
static bool covered = false, floating = false;
static int selected = 0, command_fd = -1, parent_depth = 0, slot_depth = 0;
static pid_t broker_pid = 0;
static std::thread reader;
static Window RootFor(Window child) {
  Window current = child;
  for (int i = 0; i < 16 && current; i++) {
    auto reply =
        xcb_query_tree_reply(query, xcb_query_tree(query, current), nullptr);
    if (!reply)
      return 0;
    auto p = reply->parent, root = reply->root;
    free(reply);
    if (p == main_id || p == float_id)
      return p;
    if (p == root || p == current)
      return 0;
    current = p;
  }
  return 0;
}
static bool Visible(Window id) {
  auto r = xcb_get_window_attributes_reply(
      query, xcb_get_window_attributes(query, id), nullptr);
  if (!r)
    return false;
  bool v = r->map_state == XCB_MAP_STATE_VIEWABLE;
  free(r);
  return v;
}
static void Layout() {
  for (int i = 0; i < 2; i++) {
    Window parent = floating && i == selected ? float_id : main_id;
    if (!parent || !slots[i])
      continue;
    XWindowAttributes a{};
    if (!XGetWindowAttributes(display, parent, &a))
      continue;
    int left = parent == main_id ? 240 : 20,
        width = std::max(100, a.width - left - 20),
        height = std::max(100, a.height - 120);
    Window root, p, *children = nullptr;
    unsigned n = 0;
    XQueryTree(display, slots[i], &root, &p, &children, &n);
    if (children)
      XFree(children);
    if (p != parent)
      XReparentWindow(display, slots[i], parent, left, 100);
    XMoveResizeWindow(display, slots[i], left, 100, width, height);
    if (guests[i] && alive && !closed[i]) {
      uint32_t sizes[] = {static_cast<uint32_t>(width),
                          static_cast<uint32_t>(height)};
      auto e = xcb_request_check(
          query, xcb_configure_window_checked(query, guests[i],
                                              XCB_CONFIG_WINDOW_WIDTH |
                                                  XCB_CONFIG_WINDOW_HEIGHT,
                                              sizes));
      free(e);
    }
    if (i == selected && alive && guests[i] && !closed[i] && !covered && !faults[i])
      XMapWindow(display, slots[i]);
    else
      XUnmapWindow(display, slots[i]);
  }
  XSync(display, False);
  xcb_flush(query);
}
static bool Spawn() {
  if (reader.joinable())
    reader.join();
  if (command_fd >= 0) {
    close(command_fd);
    command_fd = -1;
  }
  for (int i = 0; i < 2; i++) {
    guests[i] = 0;
    closed[i] = false;
    faults[i] = false;
  }
  {
    std::lock_guard<std::mutex> lock(popup_mutex);
    popups.clear();
  }
  shutdown_returned = false;
  exited_cleanly = false;
  covered = false;
  floating = false;
  int input[2], output[2];
  // Wails/WebKit may exec more children after this spawn. They must not keep
  // the UI's command write end alive after the UI dies. dup2 below deliberately
  // publishes only the broker's stdin/stdout; all other ends remain CLOEXEC.
  if (pipe2(input, O_CLOEXEC) || pipe2(output, O_CLOEXEC)) {
    last_error = "broker pipe failed";
    return false;
  }
  posix_spawn_file_actions_t a;
  posix_spawn_file_actions_init(&a);
  posix_spawn_file_actions_adddup2(&a, input[0], STDIN_FILENO);
  posix_spawn_file_actions_adddup2(&a, output[1], STDOUT_FILENO);
  for (int fd : {input[0], input[1], output[0], output[1]})
    posix_spawn_file_actions_addclose(&a, fd);
  std::string pa = "--parent-a=" + std::to_string(slots[0]),
              pb = "--parent-b=" + std::to_string(slots[1]),
              cache = "--cache-path=" + cache_path,
              log = "--log-file=" + log_path;
  char *args[] = {broker_path.data(), pa.data(),  pb.data(),
                  cache.data(),       log.data(), nullptr};
  int status =
      posix_spawn(&broker_pid, broker_path.c_str(), &a, nullptr, args, environ);
  posix_spawn_file_actions_destroy(&a);
  close(input[0]);
  close(output[1]);
  if (status) {
    close(input[1]);
    close(output[0]);
    last_error = "broker spawn failed";
    return false;
  }
  command_fd = input[1];
  alive = true;
  reader = std::thread([pid = broker_pid, fd = output[0]] {
    FILE *f = fdopen(fd, "r");
    char *line = nullptr;
    size_t size = 0;
    while (getline(&line, &size, f) > 0) {
      fprintf(stderr, "Browser Lab broker: %s", line);
      std::istringstream s(line);
      std::string event;
      s >> event;
      if (event == "CREATED") {
        int i;
        unsigned long id;
        double scale = 0;
        s >> i >> id >> scale;
        if (i >= 0 && i < 2) {
          guest_dpi[i] = static_cast<int>(scale * 96);
          guests[i] = id;
        }
      } else if (event == "CLOSED") {
        int i;
        s >> i;
        if (i >= 0 && i < 2)
          closed[i] = true;
      } else if (event == "SHUTDOWN")
        shutdown_returned = true;
      else if (event == "POPUP") {
        unsigned long id;
        s >> id;
        std::lock_guard<std::mutex> lock(popup_mutex);
        popups.push_back(id);
      }
    }
    free(line);
    fclose(f);
    int status = 0;
    if (waitpid(pid, &status, 0) == pid)
      exited_cleanly = WIFEXITED(status) && WEXITSTATUS(status) == 0;
    alive = false;
  });
  return true;
}
LAB_EXPORT int lab_init(const char *cache, const char *helper,
                        const char *log) {
  cache_path = cache;
  broker_path = helper;
  log_path = log;
  return 1;
}
LAB_EXPORT int lab_create(void *native, const char *) {
  main_widget = GTK_WIDGET(native);
  auto surface = gtk_native_get_surface(GTK_NATIVE(native));
  if (!surface || !GDK_IS_X11_SURFACE(surface)) {
    last_error = "realized X11 parent required";
    return 0;
  }
  main_id = gdk_x11_surface_get_xid(surface);
  main_dpi = 96 * gdk_surface_get_scale_factor(surface);
  gdk_display_sync(gdk_surface_get_display(surface));
  display = XOpenDisplay(nullptr);
  query = xcb_connect(nullptr, nullptr);
  XWindowAttributes a{};
  if (!XGetWindowAttributes(display, main_id, &a)) {
    last_error = "parent not published";
    return 0;
  }
  parent_depth = a.depth;
  int screen = DefaultScreen(display);
  slot_depth = DefaultDepth(display, screen);
  XSetWindowAttributes attr{};
  attr.colormap = DefaultColormap(display, screen);
  attr.border_pixel = 0;
  attr.background_pixel = 0;
  for (int i = 0; i < 2; i++)
    slots[i] =
        XCreateWindow(display, main_id, 240, 100, 700, 540, 0, slot_depth,
                      InputOutput, DefaultVisual(display, screen),
                      CWColormap | CWBorderPixel | CWBackPixel, &attr);
  Layout();
  return Spawn();
}
LAB_EXPORT int lab_float_parent(void *native) {
  auto surface = gtk_native_get_surface(GTK_NATIVE(native));
  if (!surface || !GDK_IS_X11_SURFACE(surface))
    return 0;
  float_id = gdk_x11_surface_get_xid(surface);
  float_dpi = 96 * gdk_surface_get_scale_factor(surface);
  gdk_display_sync(gdk_surface_get_display(surface));
  return 1;
}
LAB_EXPORT int lab_command(const char *action, int index) {
  std::string a(action);
  if (a == "tab") {
    floating = false;
    selected = index;
  } else if (a == "float") {
    if (!float_id)
      return 0;
    floating = true;
  } else if (a == "dock")
    floating = false;
  else if (a == "cover")
    covered = true;
  else if (a == "uncover")
    covered = false;
  else if (a == "renderer-fault")
    faults[index] = true;
  else if (a == "renderer-recovered")
    faults[index] = false;
  else if (a == "restart-broker") {
    if (alive)
      return 0;
    return Spawn();
  } else if (a == "crash-broker") {
    if (!alive)
      return 0;
    return kill(broker_pid, SIGKILL) == 0;
  } else if (a == "close-broker") {
    if (!alive || command_fd < 0)
      return 0;
    const char cmd[] = "close\n";
    return write(command_fd, cmd, sizeof(cmd) - 1) == sizeof(cmd) - 1;
  } else if (a == "focus-guest") {
    if (covered || faults[selected] || !alive || !guests[selected])
      return 0;
    XSetInputFocus(display, guests[selected], RevertToParent, CurrentTime);
  } else if (a == "focus-host") {
    auto webview = FindWebView(main_widget);
    if (!webview)
      return 0;
    gtk_window_present(GTK_WINDOW(main_widget));
    if (!gtk_widget_grab_focus(webview))
      return 0;
  } else if (a != "layout")
    return 0;
  Layout();
  return 1;
}
LAB_EXPORT const char *lab_state() {
  static thread_local std::string result;
  if (display)
    Layout();
  std::ostringstream s;
  s << "{\"initialized\":true,\"created\":"
    << ((guests[0] ? 1 : 0) + (guests[1] ? 1 : 0))
    << ",\"closed\":" << ((closed[0] ? 1 : 0) + (closed[1] ? 1 : 0))
    << ",\"parentAttached\":"
    << (guests[0] && alive && RootFor(guests[0]) == main_id ? "true" : "false")
    << ",\"windowId\":\"" << main_id << "\",\"main\":{\"hwnd\":" << main_id
    << ",\"windowId\":\"" << main_id << "\",\"dpi\":" << main_dpi
    << "},\"float\":{\"hwnd\":" << float_id << ",\"windowId\":\"" << float_id
    << "\",\"dpi\":" << float_dpi
    << ",\"visible\":" << (float_id && Visible(float_id) ? "true" : "false")
    << "},\"guestRunning\":" << (alive ? "true" : "false")
    << ",\"guestPID\":" << broker_pid << ",\"selected\":\""
    << (selected ? "B" : "A")
    << "\",\"covered\":" << (covered ? "true" : "false")
    << ",\"floating\":" << (floating ? "true" : "false")
    << ",\"parentDepth\":" << parent_depth << ",\"slotDepth\":" << slot_depth
    << ",\"tabs\":[";
  for (int i = 0; i < 2; i++) {
    if (i)
      s << ",";
    auto id = guests[i].load();
    s << "{\"name\":\"" << (i ? "B" : "A") << "\",\"hwnd\":" << id
      << ",\"parentHWND\":" << (id && alive ? RootFor(id) : 0)
      << ",\"visible\":" << (id && alive && Visible(id) ? "true" : "false")
      << ",\"dpi\":" << guest_dpi[i]
      << ",\"fault\":" << (faults[i] ? "true" : "false") << "}";
  }
  s << "],\"guestTopLevelWindows\":[";
  {
    std::lock_guard<std::mutex> lock(popup_mutex);
    bool first = true;
    for (auto id : popups)
      if (alive && Visible(id)) {
        if (!first)
          s << ",";
        first = false;
        s << "{\"hwnd\":" << id
          << ",\"parentHWND\":0,\"visible\":true,\"title\":\"\"}";
      }
  }
  Window focused = 0;
  int revert = 0;
  if (display)
    XGetInputFocus(display, &focused, &revert);
  bool in_guest = false;
  Window cursor = focused;
  for (int n = 0; n < 16 && cursor > 1; n++) {
    if (cursor == guests[selected]) {
      in_guest = true;
      break;
    }
    auto r =
        xcb_query_tree_reply(query, xcb_query_tree(query, cursor), nullptr);
    if (!r)
      break;
    cursor = r->parent;
    free(r);
  }
  s << "],\"nativeFocus\":{\"inGuest\":" << (in_guest ? "true" : "false")
    << ",\"inHost\":"
    << ((focused == main_id || RootFor(focused) == main_id) && !in_guest
            ? "true"
            : "false")
    << "}}";
  result = s.str();
  return result.c_str();
}
LAB_EXPORT void lab_close() {
  if (command_fd >= 0) {
    const char c[] = "close\n";
    if (write(command_fd, c, sizeof(c) - 1) < 0)
      last_error = "broker close write failed";
    close(command_fd);
    command_fd = -1;
  }
}
LAB_EXPORT int lab_closed() {
  return closed[0] && closed[1] && shutdown_returned && exited_cleanly;
}
LAB_EXPORT void lab_shutdown() {
  if (reader.joinable())
    reader.join();
  if (display) {
    for (auto slot : slots)
      XDestroyWindow(display, slot);
    XSync(display, False);
    XCloseDisplay(display);
    display = nullptr;
  }
  if (query) {
    xcb_disconnect(query);
    query = nullptr;
  }
}
LAB_EXPORT const char *lab_error() { return last_error.c_str(); }
