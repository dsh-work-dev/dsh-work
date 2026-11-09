#pragma once
#include <stdint.h>
#include <stddef.h>
#ifdef __cplusplus
extern "C" {
#endif
// Called on a binding/IO thread. Implementations marshal native work to Cocoa.
int dsh_mac_window(void*, uint64_t*, double*, double*, double*);
int dsh_mac_view_attach(void*, uintptr_t, const char*, size_t, double, double, double, double, int);
int dsh_mac_view_visible(uintptr_t);
int dsh_mac_view_update(uintptr_t,double,double,double,double,int);
void dsh_mac_view_ime(uintptr_t,int,int,double,double,double,double);
void dsh_mac_view_close(uintptr_t);
int dsh_mac_window_watch(void*, uintptr_t);
void dsh_mac_window_unwatch(uintptr_t);
int dsh_mac_process_matches(uint32_t,const char*);

int dsh_mac_view_replace_frame(uintptr_t,const char*,size_t);
#ifdef __cplusplus
}
#endif
// Fixture-only, process-local consumers of the already-owned native view.
#ifdef __cplusplus
extern "C" {
#endif
char* dsh_mac_fixture_observe(uintptr_t);
int dsh_mac_fixture_interact(uintptr_t);
int dsh_mac_fixture_render(uintptr_t,const char*);

#ifdef __cplusplus
}
#endif
