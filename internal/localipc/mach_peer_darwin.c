//go:build darwin && cgo

#include "mach_peer.h"
#include <mach/mach.h>
#include <servers/bootstrap.h>
#include <libproc.h>
#include <sys/proc_info.h>
#include <sys/proc.h>
#include <sys/socket.h>
#include <sys/stat.h>
#include <sys/un.h>
#include <unistd.h>
#include <errno.h>
#include <fcntl.h>
#include <poll.h>
#include <stdlib.h>
#include <stdio.h>
#include <string.h>
#include <time.h>

typedef struct { char service[BOOTSTRAP_MAX_NAME_LEN]; unsigned char nonce[32]; } ipc_hello;
typedef struct { mach_msg_header_t header; unsigned char nonce[32]; } ipc_proof;
typedef struct { ipc_proof proof; mach_msg_audit_trailer_t trailer; } ipc_received;
static int64_t now_ms(void) {
  struct timespec t;
  if(clock_gettime(CLOCK_MONOTONIC,&t)) return 0;
  return (int64_t)t.tv_sec*1000+t.tv_nsec/1000000;
}
static int remaining(int64_t end) {
  int64_t left=end-now_ms(); return left>0 ? (int)left : 0;
}
static int ready(int fd,int cancel,short events,int64_t end) {
  struct pollfd p[2]={{fd,events,0},{cancel,POLLIN,0}};
  for(;;) {
    int left=remaining(end); if(!left) return ETIMEDOUT;
    int n=poll(p,cancel<0?1:2,left);
    if(n<0 && errno==EINTR) continue;
    if(n<0) return errno;
    if(!n) return ETIMEDOUT;
    if(cancel>=0 && p[1].revents) return ECANCELED;
    if(p[0].revents) return 0;
  }
}
static int bytes(int fd,int cancel,void *data,size_t size,int writing,int64_t end) {
  unsigned char *p=data;
  while(size) {
    int e=ready(fd,cancel,writing?POLLOUT:POLLIN,end); if(e) return e;
    ssize_t n=writing?send(fd,p,size,MSG_DONTWAIT):recv(fd,p,size,MSG_DONTWAIT);
    if(n<0 && (errno==EINTR || errno==EAGAIN || errno==EWOULDBLOCK)) continue;
    if(n<0) return errno;
    if(!n) return ECONNRESET;
    p+=n; size-=(size_t)n;
  }
  return 0;
}
uint64_t dsh_ipc_process_started(uint32_t pid) {
  struct proc_bsdinfo info={0};
  if(proc_pidinfo((pid_t)pid,PROC_PIDTBSDINFO,0,&info,sizeof(info))!=(int)sizeof(info) || info.pbi_status==SZOMB) return 0;
  return info.pbi_start_tvsec*1000000ULL+info.pbi_start_tvusec;
}
static int peer_token(int fd,audit_token_t *token) {
  socklen_t length=sizeof(*token);
  return getsockopt(fd,SOL_LOCAL,LOCAL_PEERTOKEN,token,&length)==0 && length==sizeof(*token);
}
static int snapshot(int fd,dsh_ipc_peer *peer) {
  pid_t pid=0; socklen_t length=sizeof(pid); audit_token_t after={0};
  if(getsockopt(fd,SOL_LOCAL,LOCAL_PEERPID,&pid,&length) || length!=sizeof(pid) || pid<=0 || !peer_token(fd,&peer->token)) return EACCES;
  peer->pid=(uint32_t)pid; peer->started=dsh_ipc_process_started(peer->pid);
  if(!peer->started || !peer_token(fd,&after) || memcmp(&peer->token,&after,sizeof(after)) || after.val[5]!=peer->pid || after.val[1]!=(unsigned)geteuid()) return EACCES;
  return 0;
}
int dsh_ipc_recheck(int fd,const dsh_ipc_peer *peer) {
  dsh_ipc_peer current={0}; int e=snapshot(fd,&current);
  if(e) return e;
  return current.pid==peer->pid && current.started==peer->started && !memcmp(&current.token,&peer->token,sizeof(current.token)) ? 0 : EACCES;
}
static int authenticate_deadline(int fd,int cancel,dsh_ipc_peer *peer,int64_t end) {
  int error=0, registered=0, received=0;
  mach_port_t receive=MACH_PORT_NULL,send=MACH_PORT_NULL;
  ipc_hello self={0},other={0};
  ipc_received message={0};
  if((error=snapshot(fd,peer))) return error;
  int enabled=1;
  if(setsockopt(fd,SOL_SOCKET,SO_NOSIGPIPE,&enabled,sizeof(enabled))) return errno;
  arc4random_buf(self.nonce,sizeof(self.nonce));
  snprintf(self.service,sizeof(self.service),"dev.dsh.ipc.%d.%08x.%08x",getpid(),arc4random(),arc4random());
  if(mach_port_allocate(mach_task_self(),MACH_PORT_RIGHT_RECEIVE,&receive)!=KERN_SUCCESS) return EIO;
  if(mach_port_insert_right(mach_task_self(),receive,receive,MACH_MSG_TYPE_MAKE_SEND)!=KERN_SUCCESS) { error=EIO; goto cleanup; }
  registered=1;
#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wdeprecated-declarations"
  if(bootstrap_register(bootstrap_port,self.service,receive)!=KERN_SUCCESS) { error=EACCES; goto cleanup; }
#pragma clang diagnostic pop
  if((error=bytes(fd,cancel,&self,sizeof(self),1,end)) || (error=bytes(fd,cancel,&other,sizeof(other),0,end))) goto cleanup;
  if(!memchr(other.service,0,sizeof(other.service)) || strncmp(other.service,"dev.dsh.ipc.",12)) { error=EACCES; goto cleanup; }
  if(bootstrap_look_up(bootstrap_port,other.service,&send)!=KERN_SUCCESS) { error=EACCES; goto cleanup; }
  ipc_proof proof={0};
  proof.header.msgh_bits=MACH_MSGH_BITS(MACH_MSG_TYPE_COPY_SEND,0);
  proof.header.msgh_size=sizeof(proof); proof.header.msgh_remote_port=send;
  memcpy(proof.nonce,other.nonce,sizeof(proof.nonce));
  for(;;) {
    int left=remaining(end); if(!left) { error=ETIMEDOUT; goto cleanup; }
    if(cancel>=0) { struct pollfd p={cancel,POLLIN,0}; if(poll(&p,1,0)>0) { error=ECANCELED; goto cleanup; } }
    kern_return_t k=mach_msg(&proof.header,MACH_SEND_MSG|MACH_SEND_TIMEOUT,sizeof(proof),0,MACH_PORT_NULL,(mach_msg_timeout_t)(left<25?left:25),MACH_PORT_NULL);
    if(k==MACH_SEND_TIMED_OUT) continue;
    if(k!=KERN_SUCCESS) { error=EACCES; goto cleanup; }
    break;
  }
  // Short bounded receive slices let context cancellation retire the handshake
  // without waiting for the three-second admission deadline.
  for(;;) {
    int left=remaining(end); if(!left) { error=ETIMEDOUT; goto cleanup; }
    if(cancel>=0) { struct pollfd p={cancel,POLLIN,0}; if(poll(&p,1,0)>0) { error=ECANCELED; goto cleanup; } }
    mach_msg_option_t options=MACH_RCV_MSG|MACH_RCV_TIMEOUT|MACH_RCV_TRAILER_TYPE(MACH_MSG_TRAILER_FORMAT_0)|MACH_RCV_TRAILER_ELEMENTS(MACH_RCV_TRAILER_AUDIT);
    kern_return_t k=mach_msg(&message.proof.header,options,0,sizeof(message),receive,(mach_msg_timeout_t)(left<25?left:25),MACH_PORT_NULL);
    if(k==MACH_RCV_TIMED_OUT) continue;
    if(k!=KERN_SUCCESS) { error=EACCES; goto cleanup; }
    received=1;
    break;
  }
  if((message.proof.header.msgh_bits&MACH_MSGH_BITS_COMPLEX) || message.proof.header.msgh_remote_port!=MACH_PORT_NULL || message.proof.header.msgh_voucher_port!=MACH_PORT_NULL) { error=EACCES; goto cleanup; }
  if(message.proof.header.msgh_size!=sizeof(ipc_proof) || message.trailer.msgh_trailer_type!=MACH_MSG_TRAILER_FORMAT_0 || message.trailer.msgh_trailer_size<sizeof(mach_msg_audit_trailer_t) || memcmp(self.nonce,message.proof.nonce,32) || memcmp(&peer->token,&message.trailer.msgh_audit,sizeof(peer->token))) { error=EACCES; goto cleanup; }
  error=dsh_ipc_recheck(fd,peer);
cleanup:
  // Even a simple message can carry a reply send/send-once right in its
  // header. Every successful receive owns those rights, on rejection or success.
  if(received) mach_msg_destroy(&message.proof.header);
  if(send!=MACH_PORT_NULL) mach_port_deallocate(mach_task_self(),send);
  if(registered) mach_port_deallocate(mach_task_self(),receive);
  if(receive!=MACH_PORT_NULL) mach_port_mod_refs(mach_task_self(),receive,MACH_PORT_RIGHT_RECEIVE,-1);
  return error;
}
int dsh_ipc_authenticate(int fd,int cancel,dsh_ipc_peer *peer) {
  return authenticate_deadline(fd,cancel,peer,now_ms()+3000);
}
int dsh_ipc_connect(const char *path,int cancel,int *output,dsh_ipc_peer *peer) {
  *output=-1;
  int64_t end=now_ms()+3000;
  struct sockaddr_un address={0}; address.sun_family=AF_UNIX;
  if(strlen(path)>=sizeof(address.sun_path)) return ENAMETOOLONG;
  strcpy(address.sun_path,path);
  char directory[sizeof(address.sun_path)]; strcpy(directory,path);
  char *separator=strrchr(directory,'/');
  if(!separator || separator==directory) return EACCES;
  *separator=0; struct stat dir_info={0},socket_info={0};
  if(lstat(directory,&dir_info) || lstat(path,&socket_info)) return errno;
  if(!S_ISDIR(dir_info.st_mode) || dir_info.st_uid!=geteuid() || (dir_info.st_mode&0777)!=0700 ||
     !S_ISSOCK(socket_info.st_mode) || socket_info.st_uid!=geteuid() || (socket_info.st_mode&0777)!=0600) return EACCES;
  int fd=socket(AF_UNIX,SOCK_STREAM,0); if(fd<0) return errno;
  int error=0,flags=fcntl(fd,F_GETFL);
  if(flags<0 || fcntl(fd,F_SETFL,flags|O_NONBLOCK) || fcntl(fd,F_SETFD,FD_CLOEXEC)) { error=errno; goto failure; }
  if(connect(fd,(struct sockaddr*)&address,sizeof(address))) {
    if(errno!=EINPROGRESS) { error=errno; goto failure; }
    if((error=ready(fd,cancel,POLLOUT,end))) goto failure;
    socklen_t size=sizeof(error);
    if(getsockopt(fd,SOL_SOCKET,SO_ERROR,&error,&size)) error=errno;
    if(error) goto failure;
  }
  if((error=authenticate_deadline(fd,cancel,peer,end))) goto failure;
  // net.Socket owns a nonblocking fd, as does Go's network poller.
  *output=fd; return 0;
failure:
  close(fd); return error;
}
