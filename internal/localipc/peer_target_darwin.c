#include "peer_target.h"
#include <libproc.h>
#include <mach/message.h>
#include <sys/proc_info.h>
#include <string.h>
#include <dlfcn.h>

int dsh_target_started(uint32_t pid, uint64_t *started) {
  struct proc_bsdinfo info = {0};
  int size = 0, error = 0;
  for (int i = 0; i < 3; ++i) {
    errno = 0;
    size = proc_pidinfo((int)pid, PROC_PIDTBSDINFO, 0, &info, sizeof(info));
    error = errno;
    if (size == sizeof(info) || error != EINTR) break;
  }
  if (size != sizeof(info)) return error ? error : EIO;
  *started = info.pbi_start_tvsec * 1000000ULL + info.pbi_start_tvusec;
  return *started ? 0 : EIO;
}

int dsh_target_signal(const uint32_t values[8], int signal) {
  audit_token_t token = {0};
  memcpy(token.val, values, sizeof(token.val));
  // This leaf is linked into the app whose existing minimum remains macOS12.
  // The CEF component is macOS15-only; do not add a hard newer-OS import to
  // callers that never request it. An absent public API fails explicitly.
  typedef int (*signal_function)(audit_token_t *, int);
  signal_function send = (signal_function)dlsym(RTLD_DEFAULT, "proc_signal_with_audittoken");
  return send ? send(&token, signal) : ENOTSUP;
}
