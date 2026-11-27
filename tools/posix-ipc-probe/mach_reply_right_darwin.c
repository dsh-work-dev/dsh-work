// Link this adversarial wire client against the actual product shim:
// clang -std=c11 -Wall -Wextra -Werror <this.c> <mach_peer_darwin.c> -o mach-reply-right
// The client waits for kernel right-destruction notifications, not just EACCES.
#include "../../internal/localipc/mach_peer.h"
#include <mach/mach.h>
#include <mach/notify.h>
#include <mach/mach_error.h>
#include <servers/bootstrap.h>
#include <sys/socket.h>
#include <sys/un.h>
#include <sys/wait.h>
#include <unistd.h>
#include <errno.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

typedef struct { char service[BOOTSTRAP_MAX_NAME_LEN]; unsigned char nonce[32]; } hello_t;
typedef struct { mach_msg_header_t header; unsigned char nonce[32]; } proof_t;
static void check(int good,const char *what) { if(!good) { fprintf(stderr,"%s errno=%d\n",what,errno); exit(1); } }
static void kernel(kern_return_t k,const char *what) { if(k!=KERN_SUCCESS) { fprintf(stderr,"%s: %s\n",what,mach_error_string(k)); exit(1); } }
static void bytes(int fd,void *data,size_t size,int writing) {
  unsigned char *p=data;
  while(size) { ssize_t n=writing?write(fd,p,size):read(fd,p,size); if(n<0 && errno==EINTR) continue; check(n>0,"stream IO"); p+=n; size-=(size_t)n; }
}
static mach_port_t receive_port(void) {
  mach_port_t port=MACH_PORT_NULL;
  kernel(mach_port_allocate(mach_task_self(),MACH_PORT_RIGHT_RECEIVE,&port),"allocate receive"); return port;
}
static void attacker(const char *path,int once,int bad_nonce) {
  alarm(10);
  int fd=socket(AF_UNIX,SOCK_STREAM,0); check(fd>=0,"socket");
  struct sockaddr_un address={0}; address.sun_family=AF_UNIX; strcpy(address.sun_path,path);
  check(connect(fd,(struct sockaddr*)&address,sizeof(address))==0,"connect");
  mach_port_t receive=receive_port();
  kernel(mach_port_insert_right(mach_task_self(),receive,receive,MACH_MSG_TYPE_MAKE_SEND),"receive send");
  hello_t self={0},peer={0}; arc4random_buf(self.nonce,sizeof(self.nonce));
  snprintf(self.service,sizeof(self.service),"dev.dsh.ipc.attack.%d.%08x",getpid(),arc4random());
#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wdeprecated-declarations"
  kernel(bootstrap_register(bootstrap_port,self.service,receive),"register attacker");
#pragma clang diagnostic pop
  bytes(fd,&self,sizeof(self),1); bytes(fd,&peer,sizeof(peer),0);
  mach_port_t destination=MACH_PORT_NULL,reply=receive_port();
  kernel(bootstrap_look_up(bootstrap_port,peer.service,&destination),"lookup actual shim");
  if(!once) {
    kernel(mach_port_insert_right(mach_task_self(),reply,reply,MACH_MSG_TYPE_MAKE_SEND),"reply send");
    mach_port_t previous=MACH_PORT_NULL;
    // The copy sent to the shim will be the final send reference after the
    // client drops its own. NO_SENDERS proves the shim released its copy.
    kernel(mach_port_request_notification(mach_task_self(),reply,MACH_NOTIFY_NO_SENDERS,1,reply,MACH_MSG_TYPE_MAKE_SEND_ONCE,&previous),"request no senders");
    check(previous==MACH_PORT_NULL,"unexpected previous notification");
  }
  proof_t proof={0};
  proof.header.msgh_bits=MACH_MSGH_BITS(MACH_MSG_TYPE_COPY_SEND,once?MACH_MSG_TYPE_MAKE_SEND_ONCE:MACH_MSG_TYPE_COPY_SEND);
  proof.header.msgh_size=sizeof(proof); proof.header.msgh_remote_port=destination; proof.header.msgh_local_port=reply;
  memcpy(proof.nonce,peer.nonce,32); if(bad_nonce) proof.nonce[0]^=1;
  kernel(mach_msg(&proof.header,MACH_SEND_MSG|MACH_SEND_TIMEOUT,sizeof(proof),0,MACH_PORT_NULL,3000,MACH_PORT_NULL),"send malicious reply right");
  if(!once) kernel(mach_port_deallocate(mach_task_self(),reply),"drop client's reply send");
  union { mach_no_senders_notification_t no_senders; mach_send_once_notification_t send_once; unsigned char room[256]; } notification={0};
  kernel(mach_msg((mach_msg_header_t*)&notification,MACH_RCV_MSG|MACH_RCV_TIMEOUT,0,sizeof(notification),reply,3000,MACH_PORT_NULL),"wait actual right destruction");
  mach_msg_header_t *header=(mach_msg_header_t*)&notification;
  check(header->msgh_id==(once?MACH_NOTIFY_SEND_ONCE:MACH_NOTIFY_NO_SENDERS),"wrong destruction notification");
  printf("kernel disposal confirmed right=%s nonce=%s notification=%d\n",once?"send-once":"send",bad_nonce?"bad":"valid",header->msgh_id); fflush(stdout);
  mach_msg_destroy(header);
  kernel(mach_port_deallocate(mach_task_self(),destination),"release destination");
  kernel(mach_port_deallocate(mach_task_self(),receive),"release listener send");
  kernel(mach_port_mod_refs(mach_task_self(),receive,MACH_PORT_RIGHT_RECEIVE,-1),"release listener receive");
  kernel(mach_port_mod_refs(mach_task_self(),reply,MACH_PORT_RIGHT_RECEIVE,-1),"release reply receive");
  close(fd);
}
static void run_case(const char *executable,int once,int bad_nonce) {
  char dir[]="/tmp/dsh-right-probe-XXXXXX"; check(mkdtemp(dir)!=NULL,"mkdtemp");
  struct sockaddr_un address={0}; address.sun_family=AF_UNIX;
  snprintf(address.sun_path,sizeof(address.sun_path),"%s/ipc.sock",dir);
  int server=socket(AF_UNIX,SOCK_STREAM,0); check(server>=0,"socket");
  check(bind(server,(struct sockaddr*)&address,sizeof(address))==0 && listen(server,1)==0,"listen");
  pid_t child=fork(); check(child>=0,"fork");
  if(!child) {
    close(server); execl(executable,executable,"--attacker",address.sun_path,once?"once":"send",bad_nonce?"bad":"valid",(char*)NULL); _exit(2);
  }
  int fd=accept(server,NULL,NULL); check(fd>=0,"accept"); dsh_ipc_peer peer={0};
  int error=dsh_ipc_authenticate(fd,-1,&peer);
  check(error==EACCES,"reply-right proof must be rejected by actual shim");
  close(fd); close(server); unlink(address.sun_path); rmdir(dir);
  int status=0; check(waitpid(child,&status,0)==child && WIFEXITED(status) && WEXITSTATUS(status)==0,"attacker must confirm disposal and join");
}
int main(int argc,char **argv) {
  alarm(20);
  if(argc==5 && !strcmp(argv[1],"--attacker")) { attacker(argv[2],!strcmp(argv[3],"once"),!strcmp(argv[4],"bad")); return 0; }
  check(argc==1,"usage: mach-reply-right");
  for(int once=0;once<2;once++) for(int bad=0;bad<2;bad++) run_case(argv[0],once,bad);
  puts("PROBE PASS actual shim rejected four reply-right messages; kernel confirmed all rights disposed; children joined"); return 0;
}
