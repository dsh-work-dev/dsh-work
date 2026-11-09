// Standalone macOS admission experiment, not a production transport.
// clang -std=c11 -Wall -Wextra -Werror mach_peer_darwin.c -o mach-peer
// Uses public system Mach/bootstrap/libproc APIs; no task_for_pid entitlement.
// bootstrap_register availability itself is an explicit runtime unknown.
#include <mach/mach.h>
#include <mach/mach_error.h>
#include <mach/message.h>
#include <servers/bootstrap.h>
#include <bsm/audit.h>
#include <libproc.h>
#include <sys/proc_info.h>
#include <sys/proc.h>
#include <sys/socket.h>
#include <sys/time.h>
#include <sys/un.h>
#include <sys/wait.h>
#include <unistd.h>
#include <errno.h>
#include <signal.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

typedef struct { char service[BOOTSTRAP_MAX_NAME_LEN]; unsigned char nonce[32]; } hello_t;
typedef struct { mach_msg_header_t header; unsigned char nonce[32]; } proof_t;
typedef struct { proof_t proof; mach_msg_audit_trailer_t trailer; } received_t;

static void fail(const char *what) { perror(what); exit(1); }
static void kern_check(kern_return_t k, const char *what) {
  if (k != KERN_SUCCESS) { fprintf(stderr,"%s: %s (%d)\n",what,mach_error_string(k),k); exit(1); }
}
static void transfer(int fd, void *buffer, size_t size, int writing) {
  unsigned char *p=buffer;
  while(size) {
    ssize_t n=writing ? write(fd,p,size) : read(fd,p,size);
    if(n<0 && errno==EINTR) continue;
    if(n<=0) fail("socket transfer");
    p+=n; size-=(size_t)n;
  }
}
static mach_port_t listener(hello_t *hello) {
  mach_port_t port=MACH_PORT_NULL;
  arc4random_buf(hello->nonce,sizeof(hello->nonce));
  snprintf(hello->service,sizeof(hello->service),"dev.dsh.ipc-probe.%d.%08x",getpid(),arc4random());
  kern_check(mach_port_allocate(mach_task_self(),MACH_PORT_RIGHT_RECEIVE,&port),"allocate");
  kern_check(mach_port_insert_right(mach_task_self(),port,port,MACH_MSG_TYPE_MAKE_SEND),"insert send");
  // This deprecated public API is a probe dependency, not a product choice.
#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wdeprecated-declarations"
  kern_check(bootstrap_register(bootstrap_port,hello->service,port),"bootstrap_register");
#pragma clang diagnostic pop
  return port;
}
static uint64_t started(pid_t pid) {
  struct proc_bsdinfo info={0};
  if(proc_pidinfo(pid,PROC_PIDTBSDINFO,0,&info,sizeof(info))!=(int)sizeof(info)) fail("PROC_PIDTBSDINFO");
  if(info.pbi_status==SZOMB) { fprintf(stderr,"zombie rejected\n"); exit(1); }
  return info.pbi_start_tvsec*1000000ULL+info.pbi_start_tvusec;
}
static void send_proof(const hello_t *peer) {
  mach_port_t port=MACH_PORT_NULL;
  kern_check(bootstrap_look_up(bootstrap_port,peer->service,&port),"bootstrap_look_up");
  proof_t message={0};
  message.header.msgh_bits=MACH_MSGH_BITS(MACH_MSG_TYPE_COPY_SEND,0);
  message.header.msgh_size=sizeof(message);
  message.header.msgh_remote_port=port;
  memcpy(message.nonce,peer->nonce,sizeof(message.nonce));
  kern_check(mach_msg(&message.header,MACH_SEND_MSG|MACH_SEND_TIMEOUT,sizeof(message),0,MACH_PORT_NULL,3000,MACH_PORT_NULL),"send proof");
  kern_check(mach_port_deallocate(mach_task_self(),port),"release send");
}
static int token(int fd,audit_token_t *token) {
  socklen_t length=sizeof(*token);
  return getsockopt(fd,SOL_LOCAL,LOCAL_PEERTOKEN,token,&length)==0 && length==sizeof(*token);
}
static void receive_proof(int fd,mach_port_t port,const hello_t *self,pid_t expected) {
  received_t message={0};
  mach_msg_option_t options=MACH_RCV_MSG|MACH_RCV_TIMEOUT|
    MACH_RCV_TRAILER_TYPE(MACH_MSG_TRAILER_FORMAT_0)|MACH_RCV_TRAILER_ELEMENTS(MACH_RCV_TRAILER_AUDIT);
  kern_check(mach_msg(&message.proof.header,options,0,sizeof(message),port,3000,MACH_PORT_NULL),"receive audit proof");
  if(message.proof.header.msgh_size!=sizeof(proof_t) ||
     message.trailer.msgh_trailer_type!=MACH_MSG_TRAILER_FORMAT_0 ||
     message.trailer.msgh_trailer_size<sizeof(mach_msg_audit_trailer_t) ||
     memcmp(self->nonce,message.proof.nonce,32)) {
    fprintf(stderr,"proof shape/nonce rejected\n"); exit(1);
  }
  // The trailer is captured from the actual Mach sender. LOCAL_PEERTOKEN alone
  // is insufficient: XNU relooks up last_pid. Compare its complete PID-version
  // token to the immutable sender token around the process creation lookup.
  audit_token_t before={0},after={0};
  pid_t socket_pid=0; socklen_t length=sizeof(socket_pid);
  if(getsockopt(fd,SOL_LOCAL,LOCAL_PEERPID,&socket_pid,&length) ||
     socket_pid!=expected || !token(fd,&before)) fail("socket peer");
  uint64_t creation=started(expected);
  if(!token(fd,&after) || memcmp(&before,&after,sizeof(before)) ||
     memcmp(&before,&message.trailer.msgh_audit,sizeof(before)) ||
     (pid_t)before.val[5]!=expected || before.val[1]!=(unsigned)geteuid() || !creation) {
    fprintf(stderr,"sender process instance rejected\n"); exit(1);
  }
  printf("audit sender verified pid=%d pidversion=%u creation_us=%llu nonce=matched\n",expected,before.val[7],(unsigned long long)creation);
  fflush(stdout);
}
static void deadlines(int fd) {
  struct timeval timeout={5,0};
  if(setsockopt(fd,SOL_SOCKET,SO_RCVTIMEO,&timeout,sizeof(timeout)) ||
     setsockopt(fd,SOL_SOCKET,SO_SNDTIMEO,&timeout,sizeof(timeout))) fail("socket timeout");
}
int main(int argc,char **argv) {
  alarm(15);
  hello_t self={0},peer={0};
  mach_port_t port=listener(&self);
  if(argc==4 && !strcmp(argv[1],"--child")) {
    int fd=socket(AF_UNIX,SOCK_STREAM,0); if(fd<0) fail("socket"); deadlines(fd);
    struct sockaddr_un addr={0}; addr.sun_family=AF_UNIX;
    if(strlen(argv[2])>=sizeof(addr.sun_path)) fail("path length");
    strcpy(addr.sun_path,argv[2]);
    if(connect(fd,(struct sockaddr*)&addr,sizeof(addr))) fail("connect");
    transfer(fd,&self,sizeof(self),1); transfer(fd,&peer,sizeof(peer),0);
    send_proof(&peer); receive_proof(fd,port,&self,(pid_t)atoi(argv[3]));
    close(fd); mach_port_destroy(mach_task_self(),port); return 0;
  }
  if(argc!=1) { fprintf(stderr,"usage: mach-peer\n"); return 2; }
  char dir[]="/tmp/dsh-mach-probe-XXXXXX"; if(!mkdtemp(dir)) fail("mkdtemp");
  struct sockaddr_un addr={0}; addr.sun_family=AF_UNIX;
  snprintf(addr.sun_path,sizeof(addr.sun_path),"%s/peer.sock",dir);
  int server=socket(AF_UNIX,SOCK_STREAM,0); if(server<0) fail("socket");
  if(bind(server,(struct sockaddr*)&addr,sizeof(addr)) || listen(server,1)) fail("listen");
  pid_t child=fork(); if(child<0) fail("fork");
  if(!child) {
    char pid[32]; snprintf(pid,sizeof(pid),"%d",getppid());
    close(server); execl(argv[0],argv[0],"--child",addr.sun_path,pid,(char*)NULL); _exit(2);
  }
  int fd=accept(server,NULL,NULL); if(fd<0) fail("accept"); deadlines(fd);
  transfer(fd,&peer,sizeof(peer),0); transfer(fd,&self,sizeof(self),1);
  send_proof(&peer); receive_proof(fd,port,&self,child);
  close(fd); close(server); unlink(addr.sun_path); rmdir(dir);
  int status=0; if(waitpid(child,&status,0)!=child || !WIFEXITED(status) || WEXITSTATUS(status)) return 1;
  kern_check(mach_port_destroy(mach_task_self(),port),"destroy receive");
  puts("PROBE PASS: bidirectional live socket nonce + kernel Mach sender audit token; adversarial and hardened-runtime qualification pending");
  return 0;
}
