// Node-API ABI-stable, same-process bridge. No Node/V8 private socket handles.
// Link with ../../localipc/mach_peer_darwin.c; Node headers are build inputs,
// never fetched or compiled by application startup.
#define NAPI_VERSION 8
#include <node_api.h>
#include "../../localipc/mach_peer.h"
#include <stdlib.h>
#include <unistd.h>
#include <errno.h>
#include <fcntl.h>
#include <string.h>

typedef struct { int fd; } socket_owner;
typedef struct {
  napi_env env;
  napi_async_work work;
  napi_deferred promise;
  napi_async_cleanup_hook_handle cleanup;
  int cancel[2],fd,error,closing;
  char path[104];
} connection;
static void finalize_socket(napi_env env,void *data,void *hint) {
  (void)env; (void)hint; socket_owner *owner=data;
  if(owner->fd>=0) close(owner->fd); free(owner);
}
static napi_value failure(napi_env env,const char *message) {
  napi_throw_error(env,"WORKER_IPC_MAC_BRIDGE_FAILED",message); return NULL;
}
static void execute(napi_env env,void *data) {
  (void)env; connection *c=data; dsh_ipc_peer peer={0};
  c->error=dsh_ipc_connect(c->path,c->cancel[0],&c->fd,&peer);
}
static void shutdown_connection(napi_async_cleanup_hook_handle handle,void *data) {
  (void)handle; connection *c=data; c->closing=1;
  char byte=1; (void)write(c->cancel[1],&byte,1);
  (void)napi_cancel_async_work(c->env,c->work);
}
static void complete(napi_env env,napi_status status,void *data) {
  connection *c=data;
  if(!c->closing && status==napi_ok && !c->error) {
    socket_owner *owner=calloc(1,sizeof(*owner)); napi_value value;
    if(owner) {
      owner->fd=c->fd;
      if(napi_create_external(env,owner,finalize_socket,NULL,&value)==napi_ok) {
        c->fd=-1; (void)napi_resolve_deferred(env,c->promise,value);
      } else { free(owner); c->error=ENOMEM; }
    } else c->error=ENOMEM;
  }
  if(!c->closing && (status!=napi_ok || c->error)) {
    napi_value text,error,code;
    napi_create_string_utf8(env,"Mac process admission failed",NAPI_AUTO_LENGTH,&text);
    napi_create_string_utf8(env,"WORKER_IPC_MAC_PEER_REJECTED",NAPI_AUTO_LENGTH,&code);
    napi_create_error(env,code,text,&error); napi_reject_deferred(env,c->promise,error);
  }
  if(c->fd>=0) close(c->fd);
  close(c->cancel[0]); close(c->cancel[1]);
  napi_delete_async_work(env,c->work);
  napi_remove_async_cleanup_hook(c->cleanup); free(c);
}
static napi_value connect_socket(napi_env env,napi_callback_info info) {
  size_t argc=1; napi_value args[1],promise,name; size_t size=0;
  if(napi_get_cb_info(env,info,&argc,args,NULL,NULL)!=napi_ok || argc!=1) return failure(env,"socket path required");
  connection *c=calloc(1,sizeof(*c)); if(!c) return failure(env,"allocation failed");
  c->fd=-1; c->env=env;
  if(napi_get_value_string_utf8(env,args[0],NULL,0,&size)!=napi_ok || !size || size>=sizeof(c->path) ||
     napi_get_value_string_utf8(env,args[0],c->path,sizeof(c->path),&size)!=napi_ok || strlen(c->path)!=size) {
    free(c); return failure(env,"invalid socket path");
  }
  if(pipe(c->cancel)) { free(c); return failure(env,"cancellation pipe failed"); }
  if(fcntl(c->cancel[0],F_SETFD,FD_CLOEXEC) || fcntl(c->cancel[1],F_SETFD,FD_CLOEXEC)) {
    close(c->cancel[0]); close(c->cancel[1]); free(c); return failure(env,"cancellation pipe flags failed");
  }
  if(napi_create_promise(env,&c->promise,&promise)!=napi_ok ||
     napi_create_string_utf8(env,"dsh.mac.ipc",NAPI_AUTO_LENGTH,&name)!=napi_ok ||
     napi_create_async_work(env,NULL,name,execute,complete,c,&c->work)!=napi_ok) {
    close(c->cancel[0]); close(c->cancel[1]); free(c); return failure(env,"async connection failed");
  }
  if(napi_add_async_cleanup_hook(env,shutdown_connection,c,&c->cleanup)!=napi_ok) {
    napi_delete_async_work(env,c->work); close(c->cancel[0]); close(c->cancel[1]); free(c); return failure(env,"cleanup hook failed");
  }
  if(napi_queue_async_work(env,c->work)!=napi_ok) {
    napi_remove_async_cleanup_hook(c->cleanup); napi_delete_async_work(env,c->work);
    close(c->cancel[0]); close(c->cancel[1]); free(c); return failure(env,"connection queue failed");
  }
  return promise;
}
static socket_owner *argument(napi_env env,napi_callback_info info) {
  size_t argc=1; napi_value args[1]; socket_owner *owner=NULL;
  if(napi_get_cb_info(env,info,&argc,args,NULL,NULL)!=napi_ok || argc!=1 ||
     napi_get_value_external(env,args[0],(void**)&owner)!=napi_ok || !owner) {
    failure(env,"socket owner required"); return NULL;
  }
  return owner;
}
static napi_value descriptor(napi_env env,napi_callback_info info) {
  socket_owner *owner=argument(env,info); if(!owner) return NULL;
  if(owner->fd<0) return failure(env,"socket ownership already transferred");
  napi_value result; napi_create_int32(env,owner->fd,&result); return result;
}
static napi_value release(napi_env env,napi_callback_info info) {
  socket_owner *owner=argument(env,info); if(!owner) return NULL;
  owner->fd=-1; return NULL;
}
static napi_value close_socket(napi_env env,napi_callback_info info) {
  socket_owner *owner=argument(env,info); if(!owner) return NULL;
  if(owner->fd>=0) { close(owner->fd); owner->fd=-1; } return NULL;
}
static napi_value initialize(napi_env env,napi_value exports) {
  napi_property_descriptor properties[]={
    {"connect",NULL,connect_socket,NULL,NULL,NULL,napi_default,NULL},
    {"fd",NULL,descriptor,NULL,NULL,NULL,napi_default,NULL},
    {"release",NULL,release,NULL,NULL,NULL,napi_default,NULL},
    {"close",NULL,close_socket,NULL,NULL,NULL,napi_default,NULL}
  };
  napi_define_properties(env,exports,sizeof(properties)/sizeof(properties[0]),properties); return exports;
}
NAPI_MODULE(NODE_GYP_MODULE_NAME,initialize)
