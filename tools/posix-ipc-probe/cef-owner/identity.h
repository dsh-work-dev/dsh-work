#ifndef DSH_CEF_OWNER_PROBE_IDENTITY_H
#define DSH_CEF_OWNER_PROBE_IDENTITY_H
#include <libproc.h>
#include <mach/mach.h>
#include <sys/proc_info.h>
#include <sys/proc.h>
#include <unistd.h>
#include <cstdio>
#include <cerrno>
#include <chrono>
#include <string>
#include <vector>

namespace owner_probe {
inline int64_t Now() { return std::chrono::duration_cast<std::chrono::milliseconds>(std::chrono::system_clock::now().time_since_epoch()).count(); }
inline std::string Quote(const std::string& value) {
  std::string out="\"";
  for(unsigned char c:value) {
    if(c=='"' || c=='\\') { out+='\\'; out+=static_cast<char>(c); }
    else if(c>=32 && c<127) out+=static_cast<char>(c);
    else { char escaped[7]; std::snprintf(escaped,sizeof(escaped),"\\u%04x",c); out+=escaped; }
  }
  return out+'"';
}
inline void Emit(const std::string& json) {
  const std::string stamped=json.substr(0,json.size()-1)+",\"source_timestamp_ms\":"+std::to_string(Now())+"}";
  flockfile(stdout); std::fwrite(stamped.data(),1,stamped.size(),stdout); std::fputc('\n',stdout); std::fflush(stdout); funlockfile(stdout);
}
inline uint64_t Started(pid_t pid,int* status=nullptr,pid_t* parent=nullptr,pid_t* group=nullptr,int* error=nullptr) {
  struct proc_bsdinfo info={};
  int size=0,code=0;
  for(int attempt=0;attempt<3;attempt++) {
    errno=0;
    size=proc_pidinfo(pid,PROC_PIDTBSDINFO,0,&info,sizeof(info));
    code=errno;
    if(size==static_cast<int>(sizeof(info)) || code!=EINTR) break;
  }
  if(size!=static_cast<int>(sizeof(info))) { if(error) *error=code?code:EIO; return 0; }
  uint64_t start=info.pbi_start_tvsec*1000000ULL+info.pbi_start_tvusec;
  if(!start) { if(error) *error=EIO; return 0; }
  if(error) *error=0;
  if(status) *status=info.pbi_status;
  if(parent) *parent=info.pbi_ppid;
  if(group) *group=info.pbi_pgid;
  return start;
}
inline std::string Identity(pid_t pid) {
  int status=0,error=0; pid_t parent=0,group=0;
  uint64_t start=Started(pid,&status,&parent,&group,&error);
  const char* state=error==ESRCH?"gone":error?"unknown":status==SZOMB?"zombie":"live";
  char path[PROC_PIDPATHINFO_MAXSIZE]={}; proc_pidpath(pid,path,sizeof(path));
  return "{\"pid\":"+std::to_string(pid)+",\"started\":"+std::to_string(start)+",\"state\":"+Quote(state)+",\"error\":"+std::to_string(error)+",\"ppid\":"+std::to_string(parent)+",\"pgid\":"+std::to_string(group)+",\"sid\":"+std::to_string(getsid(pid))+",\"status\":"+std::to_string(status)+",\"path\":"+Quote(path)+"}";
}
inline std::string Census(pid_t group) {
  std::vector<pid_t> pids(4096);
  errno=0;
  // Unlike proc_listpids (bytes), this convenience API returns a PID count.
  int count=proc_listpgrppids(group,pids.data(),static_cast<int>(pids.size()*sizeof(pid_t)));
  if(count<0 || (count==0 && errno && errno!=ESRCH)) return "{\"error\":\"proc_listpgrppids failed\"}";
  std::string out="{\"pgid\":"+std::to_string(group)+",\"truncated\":"+(count==static_cast<int>(pids.size())?"true":"false")+",\"members\":[";
  bool first=true;
  for(int i=0;i<count;i++) if(pids[i]>0) { if(!first) out+=','; first=false; out+=Identity(pids[i]); }
  return out+"]}";
}
inline std::string Argument(int argc,char** argv,const std::string& name) {
  const std::string prefix="--"+name+"=";
  for(int i=1;i<argc;i++) { std::string value=argv[i]; if(value.rfind(prefix,0)==0) return value.substr(prefix.size()); }
  return {};
}
inline void Self(const std::string& event,int argc,char** argv) {
  audit_token_t token={}; mach_msg_type_number_t count=TASK_AUDIT_TOKEN_COUNT;
  if(task_info(mach_task_self(),TASK_AUDIT_TOKEN,reinterpret_cast<task_info_t>(&token),&count)!=KERN_SUCCESS || count!=TASK_AUDIT_TOKEN_COUNT) { Emit("{\"event\":\"self-audit-failed\"}"); return; }
  std::string audit="[";
  for(size_t i=0;i<8;i++) { if(i) audit+=','; audit+=std::to_string(token.val[i]); }
  audit+=']';
  Emit("{\"event\":"+Quote(event)+",\"identity\":"+Identity(getpid())+",\"audit\":"+audit+",\"type\":"+Quote(Argument(argc,argv,"type"))+",\"utility\":"+Quote(Argument(argc,argv,"utility-sub-type"))+"}");
}
}
#endif
