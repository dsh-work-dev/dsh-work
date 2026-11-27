#pragma once
#include <cstdint>
#include <charconv>
#include <cstdlib>
#include <fstream>
#include <memory>
#include <sstream>
#include <string>
#include <sys/syscall.h>
#include <unistd.h>
#include <xcb/res.h>
#include <xcb/xcb.h>

// Checked XCB requests avoid a process-global Xlib error handler. XRes supplies
// the server's local client PID, never a writable _NET_WM_PID property.
namespace dsh_x11 {
template <class T> bool number(const std::string &text, T &value) {
  auto result = std::from_chars(text.data(), text.data() + text.size(), value);
  return result.ec == std::errc{} && result.ptr == text.data() + text.size();
}
template <class T> using Reply = std::unique_ptr<T, decltype(&std::free)>;
template <class T> Reply<T> own(T *value) {
  return Reply<T>(value, &std::free);
}
struct Connection {
  xcb_connection_t *value = nullptr;
  Connection() {
    value = xcb_connect(nullptr, nullptr);
    if (value && xcb_connection_has_error(value)) {
      xcb_disconnect(value);
      value = nullptr;
    }
  }
  ~Connection() {
    if (value)
      xcb_disconnect(value);
  }
  Connection(const Connection &) = delete;
  bool checked(xcb_void_cookie_t request) {
    auto error = own(xcb_request_check(value, request));
    return !error;
  }
};
inline uint64_t started(uint32_t pid) {
  std::ifstream stream("/proc/" + std::to_string(pid) + "/stat");
  std::string text;
  if (!std::getline(stream, text))
    return 0;
  auto end = text.rfind(')');
  if (end == std::string::npos)
    return 0;
  std::istringstream fields(text.substr(end + 1));
  std::string field;
  for (int i = 0; i <= 19; i++)
    if (!(fields >> field))
      return 0;
  uint64_t value = 0;
  return number(field, value) ? value : 0;
}
struct Process {
  int fd = -1;
  uint32_t pid;
  uint64_t birth;
  Process(uint32_t p, uint64_t b) : pid(p), birth(b) {
    if (p && b)
      fd = static_cast<int>(syscall(SYS_pidfd_open, p, 0));
  }
  ~Process() {
    if (fd >= 0)
      close(fd);
  }
  bool valid() {
    return fd >= 0 && syscall(SYS_pidfd_send_signal, fd, 0, nullptr, 0) == 0 &&
           started(pid) == birth &&
           syscall(SYS_pidfd_send_signal, fd, 0, nullptr, 0) == 0;
  }
};
inline bool owner(Connection &c, xcb_window_t window, uint32_t expected) {
  if (!c.value || !window)
    return false;
  xcb_res_client_id_spec_t spec{window,
                                XCB_RES_CLIENT_ID_MASK_LOCAL_CLIENT_PID};
  xcb_generic_error_t *error = nullptr;
  auto reply = own(xcb_res_query_client_ids_reply(
      c.value, xcb_res_query_client_ids(c.value, 1, &spec), &error));
  auto failure = own(error);
  if (!reply || failure)
    return false;
  for (auto it = xcb_res_query_client_ids_ids_iterator(reply.get()); it.rem;
       xcb_res_client_id_value_next(&it)) {
    if ((it.data->spec.mask & XCB_RES_CLIENT_ID_MASK_LOCAL_CLIENT_PID) &&
        xcb_res_client_id_value_value_length(it.data) >= 1 &&
        *xcb_res_client_id_value_value(it.data) == expected)
      return true;
  }
  return false;
}
inline auto geometry(Connection &c, xcb_window_t w) {
  xcb_generic_error_t *error = nullptr;
  auto reply = own(
      xcb_get_geometry_reply(c.value, xcb_get_geometry(c.value, w), &error));
  auto failure = own(error);
  if (failure)
    reply.reset();
  return reply;
}
inline bool parent_info(uint64_t id, uint32_t pid, uint64_t birth, int &width,
                        int &height) {
  if (!id || id > UINT32_MAX)
    return false;
  Process process(pid, birth);
  if (!process.valid())
    return false;
  Connection c;
  if (!owner(c, static_cast<xcb_window_t>(id), pid))
    return false;
  auto box = geometry(c, static_cast<xcb_window_t>(id));
  if (!box || !process.valid())
    return false;
  width = box->width;
  height = box->height;
  return width > 0 && height > 0;
}
inline bool visible(uint64_t id) {
  Connection c;
  if (!c.value || !id || id > UINT32_MAX)
    return false;
  xcb_generic_error_t *error = nullptr;
  auto attrs = own(xcb_get_window_attributes_reply(
      c.value,
      xcb_get_window_attributes(c.value, static_cast<xcb_window_t>(id)),
      &error));
  auto failure = own(error);
  return attrs && !failure && attrs->map_state == XCB_MAP_STATE_VIEWABLE;
}
inline std::string hide(uint64_t id) {
  Connection c;
  if (!c.value || !id || id > UINT32_MAX)
    return "CEF_PRESENT_WINDOW_GONE";
  if (!owner(c, static_cast<xcb_window_t>(id), static_cast<uint32_t>(getpid())))
    return "CEF_PRESENT_GUEST_OWNER_REJECTED";
  if (!c.checked(
          xcb_unmap_window_checked(c.value, static_cast<xcb_window_t>(id))))
    return "CEF_PRESENT_HIDE_FAILED";
  xcb_generic_error_t *error = nullptr;
  auto attrs = own(xcb_get_window_attributes_reply(
      c.value,
      xcb_get_window_attributes(c.value, static_cast<xcb_window_t>(id)),
      &error));
  auto failure = own(error);
  return attrs && !failure && attrs->map_state == XCB_MAP_STATE_UNMAPPED
             ? ""
             : "CEF_PRESENT_HIDE_FAILED";
}
inline std::string attach(uint64_t guest, uint64_t parent, uint32_t pid,
                          uint64_t birth, int x, int y, int width, int height,
                          bool show) {
  auto fail = [&](const std::string &code) {
    auto hidden = hide(guest);
    return code + (hidden.empty() ? "" : " / " + hidden);
  };
  if (auto error = hide(guest); !error.empty())
    return error;
  int pw = 0, ph = 0;
  if (!parent_info(parent, pid, birth, pw, ph))
    return fail("CEF_PRESENT_PARENT_REJECTED");
  if (x < 0 || y < 0 || x > INT16_MAX || y > INT16_MAX || width < 0 ||
      height < 0 || x > pw || y > ph || width > pw - x || height > ph - y)
    return fail("CEF_PRESENT_BOUNDS_REJECTED");
  // X11 windows have a minimum size of one pixel. A zero-size slot is an
  // acknowledged Hide only; no displayed geometry or control is admitted.
  if (!width || !height)
    return {};
  Process process(pid, birth);
  Connection c;
  if (!process.valid() || !owner(c, static_cast<xcb_window_t>(parent), pid))
    return fail("CEF_PRESENT_PROCESS_INSTANCE_REJECTED");
  const auto g = static_cast<xcb_window_t>(guest),
             p = static_cast<xcb_window_t>(parent);
  if (!c.checked(xcb_reparent_window_checked(
          c.value, g, p, static_cast<int16_t>(x), static_cast<int16_t>(y))))
    return fail("CEF_PRESENT_REPARENT_FAILED");
  uint32_t values[] = {static_cast<uint32_t>(x), static_cast<uint32_t>(y),
                       static_cast<uint32_t>(width),
                       static_cast<uint32_t>(height), XCB_STACK_MODE_ABOVE};
  if (!c.checked(xcb_configure_window_checked(
          c.value, g,
          XCB_CONFIG_WINDOW_X | XCB_CONFIG_WINDOW_Y | XCB_CONFIG_WINDOW_WIDTH |
              XCB_CONFIG_WINDOW_HEIGHT | XCB_CONFIG_WINDOW_STACK_MODE,
          values)))
    return fail("CEF_PRESENT_POSITION_FAILED");
  if (show && !c.checked(xcb_map_window_checked(c.value, g)))
    return fail("CEF_PRESENT_SHOW_FAILED");
  xcb_generic_error_t *error = nullptr;
  auto tree =
      own(xcb_query_tree_reply(c.value, xcb_query_tree(c.value, g), &error));
  auto failure = own(error);
  auto box = geometry(c, g);
  if (failure || !tree || tree->parent != p || !box || box->x != x ||
      box->y != y || box->width != width || box->height != height ||
      !process.valid() || visible(guest) != show)
    return fail("CEF_PRESENT_READBACK_FAILED");
  return {};
}
// Keep managed CEF children under an unmapped broker-owned parking parent
// until verified Present. CEF's initial Show can never expose a top-level page.
inline xcb_window_t parking() {
  static Connection c;
  static xcb_window_t id = 0;
  if (!c.value)
    return 0;
  if (id)
    return id;
  auto screens = xcb_setup_roots_iterator(xcb_get_setup(c.value));
  if (!screens.rem)
    return 0;
  id = xcb_generate_id(c.value);
  auto screen = screens.data;
  if (!c.checked(xcb_create_window_checked(
          c.value, XCB_COPY_FROM_PARENT, id, screen->root, 0, 0, 1, 1, 0,
          XCB_WINDOW_CLASS_INPUT_OUTPUT, screen->root_visual, 0, nullptr)))
    id = 0;
  return id;
}
} // namespace dsh_x11
