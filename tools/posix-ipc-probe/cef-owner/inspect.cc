#include "identity.h"
#include <cstdlib>
#include <cerrno>
#include <signal.h>
#include <sys/file.h>
#include <poll.h>

int main(int argc,char** argv) {
  if(argc==3 && (std::string(argv[1])=="--profile-lease" || std::string(argv[1])=="--profile-check")) {
    const bool hold=std::string(argv[1])=="--profile-lease";
    int fd=open(argv[2],O_RDWR|O_CREAT|O_CLOEXEC|O_NOFOLLOW,0600);
    if(fd<0) { owner_probe::Emit("{\"event\":\"profile-lock-error\",\"stage\":\"open\"}"); return 1; }
    if(flock(fd,LOCK_EX|LOCK_NB)!=0) {
      const int error=errno; close(fd);
      if(!hold && (error==EWOULDBLOCK || error==EAGAIN)) { owner_probe::Emit("{\"locked\":true}"); return 0; }
      owner_probe::Emit("{\"event\":\"profile-lock-error\",\"stage\":\"flock\",\"error\":"+std::to_string(error)+"}"); return 1;
    }
    if(!hold) { close(fd); owner_probe::Emit("{\"locked\":false}"); return 0; }
    owner_probe::Emit("{\"event\":\"profile-lease-ready\",\"identity\":"+owner_probe::Identity(getpid())+"}");
    // The external diagnostic controller owns stdin. This is a bounded probe
    // lock holder, not the product's daemon-death containment implementation.
    pollfd input{STDIN_FILENO,POLLIN|POLLHUP,0}; int result;
    do { result=poll(&input,1,90000); } while(result<0 && errno==EINTR);
    char byte=0; const auto size=result>0?read(STDIN_FILENO,&byte,1):-1;
    close(fd);
    if(size==1 && byte=='R') { owner_probe::Emit("{\"event\":\"profile-lease-released\"}"); return 0; }
    owner_probe::Emit("{\"event\":\"profile-lease-aborted\"}"); return 1;
  }
  if(argc==10 && std::string(argv[1])=="--cleanup-audit") {
    audit_token_t token={}; for(int i=0;i<8;i++) token.val[i]=static_cast<unsigned int>(std::strtoul(argv[i+2],nullptr,10));
    int error=proc_signal_with_audittoken(&token,SIGKILL);
    owner_probe::Emit("{\"signal_error\":"+std::to_string(error)+"}"); return error==0 || error==ESRCH?0:1;
  }
  if(argc==3 && std::string(argv[1])=="--group") { owner_probe::Emit(owner_probe::Census(static_cast<pid_t>(std::strtol(argv[2],nullptr,10)))); return 0; }
  if(argc==4 && std::string(argv[1])=="--identity") {
    pid_t pid=static_cast<pid_t>(std::strtol(argv[2],nullptr,10));
    uint64_t expected=std::strtoull(argv[3],nullptr,10); int status=0,error=0;
    uint64_t current=owner_probe::Started(pid,&status,nullptr,nullptr,&error);
    std::string state=error==ESRCH?"gone":error?"unknown":current!=expected?"reused":status==SZOMB?"zombie":"live";
    owner_probe::Emit("{\"state\":"+owner_probe::Quote(state)+",\"error\":"+std::to_string(error)+",\"identity\":"+owner_probe::Identity(pid)+"}"); return 0;
  }
  std::fprintf(stderr,"Expected --group PGID or --identity PID STARTED\n"); return 2;
}
