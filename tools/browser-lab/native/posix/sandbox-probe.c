// Test-only Seatbelt policy query. SPI declarations match WebKit SandboxSPI.h;
// this is not a product dependency or a way to relax an existing sandbox.
#include <dlfcn.h>
#include <errno.h>
#include <stdio.h>
#include <stdlib.h>
int main(int argc,char**argv){
 if(argc!=3)return 2;
 void*library=dlopen("/usr/lib/system/libsystem_sandbox.dylib",RTLD_NOW);
 if(!library)return 3;
 int(*check)(int,const char*,int,...)=dlsym(library,"sandbox_check");
 if(!check)return 4;
 const int*quiet=dlsym(library,"SANDBOX_CHECK_NO_REPORT");
 errno=0;int result=check(atoi(argv[1]),"file-read-data",1|(quiet?*quiet:0),argv[2]);
 printf("{\"result\":%d,\"errno\":%d}\n",result,errno);return result<0?1:0;
}
