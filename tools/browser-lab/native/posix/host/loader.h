#include <stdlib.h>
#include <dlfcn.h>
static int (*bridge_init)(const char*,const char*,const char*);
static int (*bridge_create)(void*,const char*);
static const char* (*bridge_state)(void);
static const char* (*bridge_error)(void);
static void (*bridge_close)(void);
static int (*bridge_closed)(void);
static void (*bridge_shutdown)(void);
static int (*bridge_float_parent)(void*);
static int (*bridge_command)(const char*,int);
static const char* load_bridge(const char* path) {
 void* lib=dlopen(path,RTLD_NOW|RTLD_GLOBAL);if(!lib)return dlerror();
 #define LOAD(name) *(void**)(&bridge_##name)=dlsym(lib,"lab_"#name);if(!bridge_##name)return dlerror();
 LOAD(init) LOAD(create) LOAD(state) LOAD(error) LOAD(close) LOAD(closed) LOAD(shutdown) LOAD(float_parent) LOAD(command)
 return NULL;
}
static int lab_initialize(const char* cache,const char* helper,const char* log){return bridge_init(cache,helper,log);}
static int lab_create(void* parent,const char* url){return bridge_create(parent,url);}
static const char* lab_state(void){return bridge_state();}
static const char* lab_error(void){return bridge_error();}
static void lab_close(void){bridge_close();}
static int lab_closed(void){return bridge_closed();}
static void lab_shutdown(void){bridge_shutdown();}
static int lab_float_parent(void* parent){return bridge_float_parent(parent);}
static int lab_command(const char* action,int index){return bridge_command(action,index);}
