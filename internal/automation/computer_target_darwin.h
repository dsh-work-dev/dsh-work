#pragma once

#include <stdint.h>

typedef struct {
    uint64_t started;
    double x, y, width, height;
    int owned;
    char executable[4096];
    char title[4096];
    char app[1024];
    char identifier[1024];
    char team[64];
} dsh_computer_target;

// 1 success, 0 unavailable, -1 different or elevated user.
int dsh_inspect_computer_target(uint32_t pid, uint32_t window, dsh_computer_target *target);
