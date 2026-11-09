#ifndef DSH_LOCALIPC_MACH_PEER_H
#define DSH_LOCALIPC_MACH_PEER_H
#include <stdint.h>
#include <mach/message.h>
#ifdef __cplusplus
extern "C" {
#endif

// Same-process system bridge. All calls may block on the calling thread for at
// most three seconds; use an IO/worker thread, never the CEF GUI/renderer pump.
// cancel_fd is a readable cancellation pipe or -1. No function owns cancel_fd.
// authenticate borrows a connected socket; its caller closes it on failure.
// connect returns an owned fd only on success; the caller then owns close.
// After authenticate, restore/use normal stream IO, then the existing HMAC.
// No helper process may call these on behalf of the socket's process owner.
typedef struct {
  uint32_t pid;
  uint64_t started;
  audit_token_t token;
} dsh_ipc_peer;
int dsh_ipc_authenticate(int fd, int cancel_fd, dsh_ipc_peer *peer);
int dsh_ipc_connect(const char *path, int cancel_fd, int *fd, dsh_ipc_peer *peer);
int dsh_ipc_recheck(int fd, const dsh_ipc_peer *peer);
uint64_t dsh_ipc_process_started(uint32_t pid);
#ifdef __cplusplus
}
#endif
#endif
