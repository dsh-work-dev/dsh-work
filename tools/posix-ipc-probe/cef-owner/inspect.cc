#include "identity.h"
#include <cstdlib>
#include <cerrno>
#include <signal.h>

int main(int argc,char** argv) {
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
