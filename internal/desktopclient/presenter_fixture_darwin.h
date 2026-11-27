#pragma once
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif
char* dsh_mac_fixture_observe(uintptr_t);
int dsh_mac_fixture_interact(uintptr_t);
int dsh_mac_fixture_click(uintptr_t,double,double);
int dsh_mac_fixture_application_reopen(void);
int dsh_mac_fixture_notification_default(const char*,const char*,const char*,int);
int dsh_mac_fixture_tray_action(const char*,int);
typedef struct {
 int x, y, width, height;
 int area_x, area_y, area_width, area_height;
 double scale;
} DSHPetFrame;
int dsh_mac_fixture_pet_frame(void*,DSHPetFrame*);
int dsh_mac_fixture_pet_display_change(void*,int);
int dsh_mac_fixture_file_picker_visible(void*);
int dsh_mac_fixture_cancel_workspace_picker(void*);
int dsh_mac_fixture_file_picker_filter(void*);
int dsh_mac_fixture_cancel_download_save(void*);
int dsh_mac_fixture_download_save_visible(void*);
int dsh_mac_fixture_download_confirmation(void*,int);
int dsh_mac_fixture_render(uintptr_t,const char*);

#ifdef __cplusplus
}
#endif
