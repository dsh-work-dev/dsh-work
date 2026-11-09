#pragma once
#include <X11/Xlib-xcb.h>
#include <errno.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <sys/syscall.h>
#include <unistd.h>
#include <xcb/res.h>
#include <xcb/xfixes.h>

// Use the GTK surface's existing X client. A save set on a broker/query
// connection cannot protect a guest when the actual UI parent client dies.
static uint64_t dsh_save_set_started(uint32_t pid) {
 char path[64],buffer[8192];snprintf(path,sizeof(path),"/proc/%u/stat",pid);
 FILE* f=fopen(path,"r");if(!f)return 0;
 char* read=fgets(buffer,sizeof(buffer),f);fclose(f);if(!read)return 0;
 char* end=strrchr(buffer,')');if(!end)return 0;
 char *save=NULL,*field=strtok_r(end+1," ",&save);
 for(int i=0;i<19&&field;i++)field=strtok_r(NULL," ",&save);
 if(!field)return 0;
 errno=0;char* tail=NULL;unsigned long long value=strtoull(field,&tail,10);
 return errno||tail==field||(*tail&&*tail!='\n')?0:(uint64_t)value;
}
static uint32_t dsh_save_set_pid(xcb_connection_t* connection,xcb_window_t window) {
 xcb_res_client_id_spec_t spec={window,XCB_RES_CLIENT_ID_MASK_LOCAL_CLIENT_PID};
 xcb_generic_error_t* error=NULL;
 xcb_res_query_client_ids_reply_t* reply=xcb_res_query_client_ids_reply(connection,xcb_res_query_client_ids(connection,1,&spec),&error);
 uint32_t pid=0;
 if(reply&&!error){for(xcb_res_client_id_value_iterator_t i=xcb_res_query_client_ids_ids_iterator(reply);i.rem;xcb_res_client_id_value_next(&i)){
  if((i.data->spec.mask&XCB_RES_CLIENT_ID_MASK_LOCAL_CLIENT_PID)&&xcb_res_client_id_value_value_length(i.data)>0){pid=*xcb_res_client_id_value_value(i.data);break;}
 }}
 free(reply);free(error);return pid;
}
typedef struct {uint32_t id,pid;uint64_t started;} dsh_save_set_guest;
static int dsh_gtk_protect_cef(void* value,const char* executable,uint64_t* parent_out,dsh_save_set_guest* guests,size_t capacity) {
 *parent_out=0;
 if(!value||!GTK_IS_NATIVE(value)||!executable)return -1;
 GdkSurface* surface=gtk_native_get_surface(GTK_NATIVE(value));
 if(!surface||!GDK_IS_X11_SURFACE(surface))return -2;
 GdkDisplay* display=gdk_surface_get_display(surface);
 xcb_connection_t* connection=XGetXCBConnection(gdk_x11_display_get_xdisplay(display));
 if(!connection||xcb_connection_has_error(connection))return -3;
 const xcb_window_t parent=(xcb_window_t)gdk_x11_surface_get_xid(surface);
 // XRes must name this process as the actual parent client, not a caller
 // property. All calls run on the same GTK main thread as surface realization.
 if(!parent||dsh_save_set_pid(connection,parent)!=(uint32_t)getpid())return -4;
 struct stat expected;if(stat(executable,&expected))return -5;
 xcb_generic_error_t* error=NULL;
 xcb_xfixes_query_version_reply_t* version=xcb_xfixes_query_version_reply(connection,xcb_xfixes_query_version(connection,1,0),&error);
 int supported=version&&version->major_version>=1&&!error;free(version);free(error);error=NULL;
 if(!supported)return -6;
 xcb_query_tree_reply_t* tree=xcb_query_tree_reply(connection,xcb_query_tree(connection,parent),&error);
 if(!tree||error){free(tree);free(error);return -7;}
 int count=xcb_query_tree_children_length(tree),protected=0;
 if(count<0||count>4096||(size_t)count>capacity){free(tree);return -8;}
 xcb_window_t* children=xcb_query_tree_children(tree);
 for(int i=0;i<count;i++) {
  const uint32_t pid=dsh_save_set_pid(connection,children[i]);
  if(!pid||pid==(uint32_t)getpid())continue;
  char path[64];snprintf(path,sizeof(path),"/proc/%u/exe",pid);struct stat actual;
  if(stat(path,&actual)||actual.st_dev!=expected.st_dev||actual.st_ino!=expected.st_ino)continue;
  int pidfd=(int)syscall(SYS_pidfd_open,pid,0);
  uint64_t birth=dsh_save_set_started(pid);
  if(pidfd<0||!birth||syscall(SYS_pidfd_send_signal,pidfd,0,NULL,0)||stat(path,&actual)||actual.st_dev!=expected.st_dev||actual.st_ino!=expected.st_ino){if(pidfd>=0)close(pidfd);free(tree);return -9;}
  error=xcb_request_check(connection,xcb_xfixes_change_save_set_checked(connection,XCB_SET_MODE_INSERT,XCB_XFIXES_SAVE_SET_TARGET_ROOT,XCB_XFIXES_SAVE_SET_MAPPING_UNMAP,children[i]));
  // A disappearing guest is a failed protection, never a successful UI ACK.
  int ok=!error&&birth==dsh_save_set_started(pid)&&syscall(SYS_pidfd_send_signal,pidfd,0,NULL,0)==0;
  free(error);close(pidfd);
  if(!ok){free(tree);return -10;}
  guests[protected++]=(dsh_save_set_guest){children[i],pid,birth};
 }
 free(tree);gdk_display_sync(display);
 if(protected>0)*parent_out=parent;
 return protected>0?protected:-11;
}
