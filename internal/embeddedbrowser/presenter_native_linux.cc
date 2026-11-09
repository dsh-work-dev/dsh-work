//go:build linux && cgo && e67presenter

#include "../../native/embeddedbrowser/presentation_linux.h"
extern "C" int dsh_x11_parent(unsigned long window, unsigned int pid,
                              unsigned long long started, int *width,
                              int *height) {
  return dsh_x11::parent_info(window, pid, started, *width, *height) ? 1 : 0;
}
