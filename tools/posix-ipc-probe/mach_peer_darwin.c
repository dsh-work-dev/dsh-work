// Standalone macOS admission experiment, not a production transport.
// clang -std=c11 -Wall -Wextra -Werror mach_peer_darwin.c -o mach-peer
// Uses public system Mach/bootstrap/libproc APIs; no task_for_pid entitlement.
// Default run: positive admission and four actual-process negative cases.
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
#include <fcntl.h>
#include <errno.h>
#include <signal.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

typedef struct { char service[BOOTSTRAP_MAX_NAME_LEN]; unsigned char nonce[32]; } hello_t;
typedef struct { mach_msg_header_t header; unsigned char nonce[32]; } proof_t;
typedef struct { proof_t proof; mach_msg_audit_trailer_t trailer; } received_t;
typedef struct { pid_t pid; uint64_t creation; audit_token_t token; } identity_t;
typedef enum { VERIFIED, REJECT_NONCE, REJECT_SENDER, REJECT_NOT_LIVE,
               REJECT_SOCKET, REJECT_IDENTITY } verdict_t;

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
static void close_listener(mach_port_t port) {
  // listener owns one local send reference and one receive right. Release
  // precisely those rights rather than destroying every right at this name.
  kern_check(mach_port_deallocate(mach_task_self(),port),"release listener send");
  kern_check(mach_port_mod_refs(mach_task_self(),port,MACH_PORT_RIGHT_RECEIVE,-1),"release listener receive");
}
static uint64_t started(pid_t pid) {
  struct proc_bsdinfo info={0};
  if(proc_pidinfo(pid,PROC_PIDTBSDINFO,0,&info,sizeof(info))!=(int)sizeof(info) || info.pbi_status==SZOMB) return 0;
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
static identity_t snapshot(int fd,pid_t expected) {
  identity_t identity={0}; audit_token_t after={0};
  pid_t socket_pid=0; socklen_t length=sizeof(socket_pid);
  if(getsockopt(fd,SOL_LOCAL,LOCAL_PEERPID,&socket_pid,&length) ||
     socket_pid!=expected || !token(fd,&identity.token)) fail("initial socket identity");
  identity.pid=expected; identity.creation=started(expected);
  if(!identity.creation || !token(fd,&after) || memcmp(&after,&identity.token,sizeof(after)) ||
     (pid_t)after.val[5]!=expected || after.val[1]!=(unsigned)geteuid()) {
    fprintf(stderr,"initial process identity unstable\n"); exit(1);
  }
  return identity;
}
static verdict_t receive_proof(int fd,mach_port_t port,const hello_t *self,const identity_t *expected) {
  received_t message={0};
  mach_msg_option_t options=MACH_RCV_MSG|MACH_RCV_TIMEOUT|
    MACH_RCV_TRAILER_TYPE(MACH_MSG_TRAILER_FORMAT_0)|MACH_RCV_TRAILER_ELEMENTS(MACH_RCV_TRAILER_AUDIT);
  kern_check(mach_msg(&message.proof.header,options,0,sizeof(message),port,3000,MACH_PORT_NULL),"receive audit proof");
  if(message.proof.header.msgh_size!=sizeof(proof_t) ||
     message.trailer.msgh_trailer_type!=MACH_MSG_TRAILER_FORMAT_0 ||
     message.trailer.msgh_trailer_size<sizeof(mach_msg_audit_trailer_t)) {
    fprintf(stderr,"invalid proof shape (not an expected rejection)\n"); exit(1);
  }
  printf("proof arrived expected_pid=%d audit_pid=%u audit_version=%u nonce=%s\n",
         expected->pid,message.trailer.msgh_audit.val[5],message.trailer.msgh_audit.val[7],
         memcmp(self->nonce,message.proof.nonce,32)?"mismatch":"matched"); fflush(stdout);
  if(memcmp(self->nonce,message.proof.nonce,32)) return REJECT_NONCE;
  // Freeze the launch instance before issuing the challenge. A later process
  // cannot replace it by presenting a new current socket token or creation.
  if(memcmp(&expected->token,&message.trailer.msgh_audit,sizeof(expected->token))) return REJECT_SENDER;
  // The trailer is captured from the actual Mach sender. LOCAL_PEERTOKEN alone
  // is insufficient: XNU relooks up last_pid. Compare its complete PID-version
  // token to the immutable sender token around the process creation lookup.
  audit_token_t before={0},after={0};
  pid_t socket_pid=0; socklen_t length=sizeof(socket_pid);
  uint64_t creation=started(expected->pid);
  if(!creation) return REJECT_NOT_LIVE;
  if(getsockopt(fd,SOL_LOCAL,LOCAL_PEERPID,&socket_pid,&length) ||
     socket_pid!=expected->pid || !token(fd,&before)) return REJECT_SOCKET;
  if(!token(fd,&after) || memcmp(&before,&after,sizeof(before)) ||
     memcmp(&before,&expected->token,sizeof(before)) || creation!=expected->creation ||
     started(expected->pid)!=expected->creation) return REJECT_IDENTITY;
  printf("audit sender verified pid=%d pidversion=%u creation_us=%llu nonce=matched\n",expected->pid,before.val[7],(unsigned long long)creation);
  fflush(stdout);
  return VERIFIED;
}
static void deadlines(int fd) {
  struct timeval timeout={5,0};
  if(setsockopt(fd,SOL_SOCKET,SO_RCVTIMEO,&timeout,sizeof(timeout)) ||
     setsockopt(fd,SOL_SOCKET,SO_SNDTIMEO,&timeout,sizeof(timeout))) fail("socket timeout");
}
static void join(pid_t child) {
  int status=0;
  if(waitpid(child,&status,0)!=child || !WIFEXITED(status) || WEXITSTATUS(status)) {
    fprintf(stderr,"child %d did not exit normally\n",child); exit(1);
  }
}
static void send_fd(int socket,int fd) {
  char byte='F'; struct iovec iov={&byte,1};
  union { struct cmsghdr align; unsigned char bytes[CMSG_SPACE(sizeof(int))]; } control={0};
  struct msghdr msg={0}; msg.msg_iov=&iov; msg.msg_iovlen=1;
  msg.msg_control=control.bytes; msg.msg_controllen=sizeof(control.bytes);
  struct cmsghdr *c=CMSG_FIRSTHDR(&msg); c->cmsg_level=SOL_SOCKET; c->cmsg_type=SCM_RIGHTS; c->cmsg_len=CMSG_LEN(sizeof(int));
  memcpy(CMSG_DATA(c),&fd,sizeof(fd));
  if(sendmsg(socket,&msg,0)!=1) fail("send connected fd");
}
static int receive_fd(int socket) {
  char byte=0; struct iovec iov={&byte,1};
  union { struct cmsghdr align; unsigned char bytes[CMSG_SPACE(sizeof(int))]; } control={0};
  struct msghdr msg={0}; msg.msg_iov=&iov; msg.msg_iovlen=1;
  msg.msg_control=control.bytes; msg.msg_controllen=sizeof(control.bytes);
  if(recvmsg(socket,&msg,0)!=1 || byte!='F' || (msg.msg_flags&MSG_CTRUNC)) fail("receive connected fd");
  struct cmsghdr *c=CMSG_FIRSTHDR(&msg); int fd=-1;
  if(!c || c->cmsg_level!=SOL_SOCKET || c->cmsg_type!=SCM_RIGHTS || c->cmsg_len!=CMSG_LEN(sizeof(fd))) fail("fd shape");
  memcpy(&fd,CMSG_DATA(c),sizeof(fd)); return fd;
}
static void child_case(const char *path,pid_t parent,const char *mode) {
  hello_t self={0},peer={0};
  mach_port_t port=listener(&self);
  int fd=socket(AF_UNIX,SOCK_STREAM,0); if(fd<0) fail("socket"); deadlines(fd);
  struct sockaddr_un addr={0}; addr.sun_family=AF_UNIX;
  if(strlen(path)>=sizeof(addr.sun_path)) fail("path length");
  strcpy(addr.sun_path,path);
  if(connect(fd,(struct sockaddr*)&addr,sizeof(addr))) fail("connect");
  transfer(fd,&self,sizeof(self),1); transfer(fd,&peer,sizeof(peer),0);
  if(!strcmp(mode,"positive")) {
    identity_t expected=snapshot(fd,parent);
    send_proof(&peer);
    if(receive_proof(fd,port,&self,&expected)!=VERIFIED) { fprintf(stderr,"positive child rejected\n"); exit(1); }
  } else if(!strcmp(mode,"wrong-nonce")) {
    peer.nonce[0]^=1; send_proof(&peer);
  } else if(!strcmp(mode,"exited-sender")) {
    send_proof(&peer);
  } else if(!strcmp(mode,"inherited-socket")) {
    send_fd(fd,fd);
  }
  // Keep the original socket owner alive until parent snapshots identity and
  // authorizes exit. The exited-sender message stays queued until waitpid.
  char end=0; transfer(fd,&end,1,0);
  close(fd); close_listener(port);
}
static void run_case(const char *executable,const char *mode,verdict_t want) {
  alarm(15);
  hello_t self={0},peer={0}; mach_port_t port=listener(&self);
  char dir[]="/tmp/dsh-mach-probe-XXXXXX"; if(!mkdtemp(dir)) fail("mkdtemp");
  struct sockaddr_un addr={0}; addr.sun_family=AF_UNIX;
  snprintf(addr.sun_path,sizeof(addr.sun_path),"%s/peer.sock",dir);
  int server=socket(AF_UNIX,SOCK_STREAM,0); if(server<0) fail("socket");
  if(bind(server,(struct sockaddr*)&addr,sizeof(addr)) || listen(server,1)) fail("listen");
  pid_t child=fork(); if(child<0) fail("fork");
  if(!child) {
    char pid[32]; snprintf(pid,sizeof(pid),"%d",getppid());
    close(server); execl(executable,executable,"--child",addr.sun_path,pid,mode,(char*)NULL); _exit(2);
  }
  int fd=accept(server,NULL,NULL); if(fd<0) fail("accept"); deadlines(fd);
  transfer(fd,&peer,sizeof(peer),0);
  identity_t expected=snapshot(fd,child);
  transfer(fd,&self,sizeof(self),1);
  char end='E'; int joined=0; pid_t inheritor=0;
  if(!strcmp(mode,"positive")) send_proof(&peer);
  if(!strcmp(mode,"foreign-sender")) {
    // The original socket owner is alive. This different same-UID process
    // sends a genuine Mach message with the exact correct nonce.
    send_proof(&self);
  }
  if(!strcmp(mode,"inherited-socket")) {
    int inherited=receive_fd(fd);
    transfer(fd,&end,1,1); join(child); joined=1;
    if(fcntl(inherited,F_SETFD,0)) fail("inherited fd flags");
    inheritor=fork(); if(inheritor<0) fail("fork inheritor");
    if(!inheritor) {
      close(fd); close(server);
      char descriptor[32]; snprintf(descriptor,sizeof(descriptor),"%d",inherited);
      execl(executable,executable,"--inheritor",descriptor,(char*)NULL); _exit(2);
    }
    close(inherited);
    char response=0; transfer(fd,&self,sizeof(self),1); transfer(fd,&response,1,0);
    if(response!='R') fail("inherited socket exchange");
  } else if(!strcmp(mode,"exited-sender")) {
    transfer(fd,&end,1,1); join(child); joined=1;
  }
  verdict_t got=receive_proof(fd,port,&self,&expected);
  if(!joined) { transfer(fd,&end,1,1); join(child); }
  if(inheritor) join(inheritor);
  close(fd); close(server); unlink(addr.sun_path); rmdir(dir);
  close_listener(port);
  if(got!=want) { fprintf(stderr,"%s: verdict %d, expected %d\n",mode,got,want); exit(1); }
  printf("CASE PASS %s verdict=%d original_pid=%d inheritor_pid=%d joined=yes\n",mode,got,child,inheritor); fflush(stdout);
}
int main(int argc,char **argv) {
  alarm(15);
  if(argc==5 && !strcmp(argv[1],"--child")) { child_case(argv[2],(pid_t)atoi(argv[3]),argv[4]); return 0; }
  if(argc==3 && !strcmp(argv[1],"--inheritor")) {
    int fd=atoi(argv[2]); deadlines(fd); hello_t peer={0};
    transfer(fd,&peer,sizeof(peer),0);
    char response='R'; transfer(fd,&response,1,1);
    send_proof(&peer); close(fd); return 0;
  }
  if(argc!=1) { fprintf(stderr,"usage: mach-peer\n"); return 2; }
  run_case(argv[0],"positive",VERIFIED);
  run_case(argv[0],"wrong-nonce",REJECT_NONCE);
  run_case(argv[0],"foreign-sender",REJECT_SENDER);
  run_case(argv[0],"exited-sender",REJECT_NOT_LIVE);
  run_case(argv[0],"inherited-socket",REJECT_SENDER);
  puts("PROBE PASS: live bidirectional proof and nonce/foreign/dead/inherited rejection; formal macOS transport remains pending");
  return 0;
}
