#pragma once
#include <stdint.h>
#include <stddef.h>
#ifdef __cplusplus
extern "C" {
#endif
// Called on a binding/IO thread. Implementations marshal native work to Cocoa.
int dsh_mac_window(void*, uint64_t*, double*, double*, double*);
int dsh_mac_view_attach(void*, uintptr_t, const char*, size_t, double, double, double, double, int, uintptr_t);
int dsh_mac_view_status(uintptr_t,int*);
int dsh_mac_view_update(uintptr_t,double,double,double,double,int,uintptr_t);
void dsh_mac_view_ime(uintptr_t,int,int,double,double,double,double);
void dsh_mac_view_close(uintptr_t);
int dsh_mac_window_watch(void*, uintptr_t);
void dsh_mac_window_unwatch(uintptr_t);
int dsh_mac_file_picker_begin(uintptr_t,uintptr_t,uintptr_t,const char*,const char*,const char*,int,int,int);
int dsh_mac_workspace_picker_begin(void*,uintptr_t);
void dsh_mac_file_picker_cancel(uintptr_t);
int dsh_mac_download_sheet_begin(uintptr_t,uintptr_t,int,const char*,const char*,const char*,const char*,const char*);
void dsh_mac_download_sheet_cancel(uintptr_t);
int dsh_mac_process_matches(uint32_t,const char*);

int dsh_mac_view_replace_frame(uintptr_t,const char*,size_t);
#ifdef __cplusplus
}
#endif
