// One-shot observation of this fixture's Xvfb windows. Not product admission.
#include "../../native/embeddedbrowser/presentation_linux.h"
#include <iostream>
#include <sys/stat.h>
#include <vector>

static uint32_t client_pid(dsh_x11::Connection &c, xcb_window_t w) {
  xcb_res_client_id_spec_t spec{w, XCB_RES_CLIENT_ID_MASK_LOCAL_CLIENT_PID};
  auto r = dsh_x11::own(xcb_res_query_client_ids_reply(
      c.value, xcb_res_query_client_ids(c.value, 1, &spec), nullptr));
  if (!r)
    return 0;
  for (auto i = xcb_res_query_client_ids_ids_iterator(r.get()); i.rem;
       xcb_res_client_id_value_next(&i))
    if ((i.data->spec.mask & XCB_RES_CLIENT_ID_MASK_LOCAL_CLIENT_PID) &&
        xcb_res_client_id_value_value_length(i.data) > 0)
      return *xcb_res_client_id_value_value(i.data);
  return 0;
}
int main(int argc, char **argv) {
  uint32_t ui = 0;
  uint64_t birth = 0;
  if (argc != 4 || !dsh_x11::number(std::string(argv[1]), ui) ||
      !dsh_x11::number(std::string(argv[2]), birth))
    return 2;
  dsh_x11::Process held(ui, birth);
  if (!held.valid())
    return 3;
  struct stat expected{};
  if (stat(argv[3], &expected))
    return 4;
  dsh_x11::Connection c;
  if (!c.value)
    return 5;
  auto screen = xcb_setup_roots_iterator(xcb_get_setup(c.value));
  if (!screen.rem)
    return 6;
  std::vector<xcb_window_t> todo{screen.data->root};
  bool comma = false;
  std::cout << "[";
  for (size_t n = 0; n < todo.size() && n < 8192; n++) {
    auto w = todo[n];
    auto tree = dsh_x11::own(
        xcb_query_tree_reply(c.value, xcb_query_tree(c.value, w), nullptr));
    if (!tree)
      continue;
    auto children = xcb_query_tree_children(tree.get());
    for (int i = 0; i < xcb_query_tree_children_length(tree.get()); i++)
      todo.push_back(children[i]);
    auto pid = client_pid(c, w);
    if (!pid)
      continue;
    auto started = dsh_x11::started(pid);
    dsh_x11::Process process(pid, started);
    if (!process.valid())
      continue;
    struct stat actual{};
    auto path = "/proc/" + std::to_string(pid) + "/exe";
    bool native = !stat(path.c_str(), &actual) &&
                  actual.st_dev == expected.st_dev &&
                  actual.st_ino == expected.st_ino;
    if (!(pid == ui && started == birth) && !native)
      continue;
    auto box = dsh_x11::geometry(c, w);
    if (!box)
      continue;
    if (comma)
      std::cout << ",";
    comma = true;
    std::cout << "{\"id\":" << w << ",\"parent\":" << tree->parent
              << ",\"pid\":" << pid << ",\"started\":" << started
              << ",\"native\":" << (native ? "true" : "false")
              << ",\"visible\":" << (dsh_x11::visible(w) ? "true" : "false")
              << ",\"width\":" << box->width << ",\"height\":" << box->height
              << "}";
  }
  std::cout << "]\n";
  return held.valid() ? 0 : 7;
}
