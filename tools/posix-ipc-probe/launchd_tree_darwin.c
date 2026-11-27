// Dedicated ownership experiment. All processes cooperate with probe cleanup
// and self-expire after 25s; neither behavior is claimed as product containment.
#include <libproc.h>
#include <sys/proc_info.h>
#include <sys/proc.h>
#include <sys/wait.h>
#include <unistd.h>
#include <signal.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

static void check(int ok,const char *what) { if(!ok) { perror(what); exit(1); } }
static uint64_t started(pid_t pid,int *status) {
  struct proc_bsdinfo info={0};
  if(proc_pidinfo(pid,PROC_PIDTBSDINFO,0,&info,sizeof(info))!=(int)sizeof(info)) return 0;
  *status=info.pbi_status; return info.pbi_start_tvsec*1000000ULL+info.pbi_start_tvusec;
}
static void path(char *out,size_t size,const char *dir,const char *name,const char *suffix) {
  check(snprintf(out,size,"%s/%s.%s",dir,name,suffix)>0,"path");
}
static void record(const char *dir,const char *name) {
  char file[1024]; path(file,sizeof(file),dir,name,"identity");
  FILE *f=fopen(file,"w"); check(f!=NULL,"identity"); int status=0;
  fprintf(f,"%d %llu %d %d %d\n",getpid(),(unsigned long long)started(getpid(),&status),getppid(),getpgrp(),getsid(0));
  check(fclose(f)==0,"identity close");
}
static int marker(const char *dir,const char *name) {
  char file[1024]; check(snprintf(file,sizeof(file),"%s/%s",dir,name)>0,"marker path"); return access(file,F_OK)==0;
}
static void tick(void) { struct timespec delay={0,100000000}; nanosleep(&delay,NULL); }
static void live(const char *dir,const char *name) {
  record(dir,name); char file[1024]; path(file,sizeof(file),dir,name,"heartbeats");
  FILE *f=fopen(file,"a"); check(f!=NULL,"heartbeat");
  for(int i=0;i<250 && !marker(dir,"cleanup");i++) { fprintf(f,"%d\n",i); fflush(f); tick(); }
  fclose(f);
}
int main(int argc,char **argv) {
  alarm(30);
  if(argc==3 && !strcmp(argv[1],"--inspect")) {
    FILE *f=fopen(argv[2],"r"); check(f!=NULL,"inspect identity");
    int pid=0,parent=0,group=0,session=0; unsigned long long original=0;
    check(fscanf(f,"%d %llu %d %d %d",&pid,&original,&parent,&group,&session)==5,"identity format"); fclose(f);
    int status=0; uint64_t current=started((pid_t)pid,&status);
    const char *state=!current?"gone":current!=original?"reused":status==SZOMB?"zombie":"live";
    printf("{\"pid\":%d,\"creation_us\":%llu,\"parent_at_start\":%d,\"group_at_start\":%d,\"session_at_start\":%d,\"state\":\"%s\"}\n",pid,original,parent,group,session,state); return 0;
  }
  check(argc==3,"usage: launchd-tree DIRECTORY MODE"); const char *dir=argv[1],*mode=argv[2];
  record(dir,"root");
  pid_t same=fork(); check(same>=0,"fork same group"); if(!same) { live(dir,"same-group"); return 0; }
  pid_t leader=fork(); check(leader>=0,"fork escape leader");
  if(!leader) {
    check(setsid()>=0,"setsid"); pid_t escaped=fork(); check(escaped>=0,"fork detached grandchild");
    if(!escaped) { live(dir,"escaped"); }
    return 0;
  }
  int status=0; check(waitpid(leader,&status,0)==leader && WIFEXITED(status) && WEXITSTATUS(status)==0,"join intermediary");
  for(int i=0;i<50 && (!marker(dir,"same-group.identity") || !marker(dir,"escaped.identity"));i++) tick();
  check(marker(dir,"same-group.identity") && marker(dir,"escaped.identity"),"children ready");
  char ready[1024]; check(snprintf(ready,sizeof(ready),"%s/ready",dir)>0,"ready path"); FILE *f=fopen(ready,"w"); check(f!=NULL,"ready"); fclose(f);
  for(int i=0;i<150 && !marker(dir,"trigger") && !marker(dir,"cleanup");i++) tick();
  if(!marker(dir,"cleanup") && !strcmp(mode,"root-crash")) { raise(SIGKILL); return 2; }
  if(!marker(dir,"cleanup") && !strcmp(mode,"bootout")) { for(int i=0;i<250 && !marker(dir,"cleanup");i++) tick(); }
  // root-exit deliberately leaves two descendants for launchd to account for.
  return 0;
}
