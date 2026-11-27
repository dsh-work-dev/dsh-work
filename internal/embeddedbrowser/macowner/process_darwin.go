//go:build darwin && cgo

package macowner

/*
#include <errno.h>
#include <libproc.h>
#include <sys/proc_info.h>
#include <sys/proc.h>
#include <sys/sysctl.h>
static int cef_process(unsigned int pid, struct proc_bsdinfo *info, char *path, int length) {
  int n=0,e=0;
  for(int i=0;i<3;i++) { errno=0;n=proc_pidinfo(pid,PROC_PIDTBSDINFO,0,info,sizeof(*info));e=errno;if(n==sizeof(*info)||e!=EINTR)break; }
  if(n!=sizeof(*info))return e?e:EIO;
  if(path) {
    for(int i=0;i<3;i++) {
      errno=0;
      n=proc_pidpath(pid,path,length);
      if(n>0||errno!=EINTR) break;
    }
    if(n<=0)path[0]=0;
  }
  return 0;
}
static int cef_group(unsigned int group, int *pids, int bytes, int *error) {
  errno=0;int n=proc_listpgrppids(group,pids,bytes);*error=errno;return n;
}
static int cef_children(unsigned int pid, int *pids, int bytes, int *error) {
  errno=0;int n=proc_listchildpids(pid,pids,bytes);*error=errno;return n;
}
static int cef_target_path(const uint32_t *words, char *path, uint32_t length) {
  audit_token_t token;
  for(int i=0;i<8;i++) token.val[i]=words[i];
  int n=0;
  for(int i=0;i<3;i++) {
    errno=0;
    n=proc_pidpath_audittoken(&token,path,length);
    if(n>0||errno!=EINTR)break;
  }
  return n;
}
*/
import "C"

import (
	"errors"
	"fmt"
	"syscall"
	"unsafe"

	"github.com/local/dsh-work/internal/localipc"
)

type Process struct {
	PID     uint32 `json:"pid"`
	Started uint64 `json:"started"`
	PPID    uint32 `json:"ppid"`
	PGID    uint32 `json:"pgid"`
	Status  int    `json:"status"`
	State   string `json:"state"`
	Path    string `json:"path"`
	Error   int    `json:"error,omitempty"`
}

type Census struct {
	Members   []Process `json:"members"`
	Truncated bool      `json:"truncated"`
	Error     int       `json:"error"`
}

func process(pid uint32) (Process, error) {
	p := Process{PID: pid}
	var info C.struct_proc_bsdinfo
	var path [4096]C.char
	code := C.cef_process(C.uint(pid), &info, &path[0], C.int(len(path)))
	if code == C.ESRCH {
		p.State = "gone"
		return p, nil
	}
	if code != 0 {
		p.State, p.Error = "unknown", int(code)
		return p, syscall.Errno(code)
	}
	p.Started = uint64(info.pbi_start_tvsec)*1000000 + uint64(info.pbi_start_tvusec)
	p.PPID, p.PGID, p.Status = uint32(info.pbi_ppid), uint32(info.pbi_pgid), int(info.pbi_status)
	p.Path = C.GoString(&path[0])
	p.State = "live"
	if p.Status == C.SZOMB {
		p.State = "zombie"
	}
	if p.Started == 0 {
		return p, errors.New("CEF process creation identity unavailable")
	}
	return p, nil
}

func instance(original Process) (Process, error) {
	p, err := process(original.PID)
	if err == nil && p.State != "gone" && p.Started != original.Started {
		p.State = "reused"
	}
	return p, err
}

func group(pgid uint32) (Census, error) {
	return processCensus(pgid, false)
}

func processCensus(id uint32, children bool) (Census, error) {
	var values [4096]C.int
	n, err := groupCount(func() (int, syscall.Errno) {
		var code C.int
		var n C.int
		if children {
			n = C.cef_children(C.uint(id), &values[0], C.int(unsafe.Sizeof(values)), &code)
		} else {
			n = C.cef_group(C.uint(id), &values[0], C.int(unsafe.Sizeof(values)), &code)
		}
		return int(n), syscall.Errno(code)
	}, len(values))
	if err != nil {
		return Census{}, err
	}
	c := Census{Members: []Process{}}
	for _, pid := range values[:n] {
		if pid <= 0 {
			continue
		}
		p, err := process(uint32(pid))
		if err != nil {
			return c, err
		}
		c.Members = append(c.Members, p)
	}
	return c, nil
}

func targetPath(target localipc.ProcessTarget) (string, error) {
	var path [C.PROC_PIDPATHINFO_MAXSIZE]C.char
	n, err := C.cef_target_path((*C.uint32_t)(unsafe.Pointer(&target.AuditToken[0])), &path[0], C.uint32_t(len(path)))
	if n <= 0 {
		return "", errors.Join(errors.New("MAC_CEF_RENDERER_TARGET_PATH_UNAVAILABLE"), err)
	}
	return C.GoString(&path[0]), nil
}

// proc_listpgrppids may return zero on failure, preserving errno. A missing
// group and a failed measurement must not share the profile-release gate.
func groupCount(query func() (int, syscall.Errno), capacity int) (int, error) {
	for attempt := 0; attempt < 3; attempt++ {
		n, code := query()
		if n <= 0 && code == syscall.EINTR && attempt < 2 {
			continue
		}
		if n == 0 && (code == 0 || code == syscall.ESRCH) {
			return 0, nil
		}
		if code != 0 {
			return 0, fmt.Errorf("CEF group query: %w", code)
		}
		if n < 0 {
			return 0, fmt.Errorf("CEF group query: %w", syscall.EIO)
		}
		if n >= capacity {
			return 0, errors.New("CEF group query truncated")
		}
		return n, nil
	}
	return 0, syscall.EINTR
}
