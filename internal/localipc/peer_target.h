#ifndef DSH_LOCALIPC_PEER_TARGET_H
#define DSH_LOCALIPC_PEER_TARGET_H
#include <stdint.h>
#include <errno.h>
int dsh_target_started(uint32_t pid, uint64_t *started);
int dsh_target_signal(const uint32_t token[8], int signal);
#endif
