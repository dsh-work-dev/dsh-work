//go:build darwin && cgo

package localipc

/*
#include "peer_target.h"
*/
import "C"

import (
	"errors"
	"net"
	"os"
	"syscall"
	"unsafe"
)

// ProcessTarget is kernel evidence obtained from an authenticated connection.
// An owner persists it only in its private generation journal. Its contents
// identify an instance; they do not grant ownership or permission to signal it.
type ProcessTarget struct {
	PID        uint32    `json:"pid"`
	Started    uint64    `json:"started"`
	AuditToken [8]uint32 `json:"auditToken"`
}

var ErrTargetStale = errors.New("LOCAL_IPC_TARGET_INSTANCE_CHANGED")

func TargetFromConn(c net.Conn) (ProcessTarget, error) {
	p, err := Peer(c)
	if err != nil {
		return ProcessTarget{}, err
	}
	owned, ok := c.(*authenticatedConn)
	if !ok {
		return ProcessTarget{}, errors.New("authenticated Mach connection required")
	}
	target := ProcessTarget{PID: p.PID, Started: p.Started}
	for i := range target.AuditToken {
		target.AuditToken[i] = uint32(owned.peer.token.val[i])
	}
	if target.AuditToken[5] != p.PID || target.AuditToken[7] == 0 {
		return ProcessTarget{}, errors.New("kernel audit target does not match peer")
	}
	// Recheck this same socket after copying its audit evidence. A sender that
	// exited or changed during extraction cannot become a persisted live owner.
	actual, err := Peer(c)
	if err != nil || actual != p {
		return ProcessTarget{}, errors.Join(ErrTargetStale, err)
	}
	return target, nil
}

func SignalTarget(target ProcessTarget, signal syscall.Signal) error {
	if target.PID == 0 || target.Started == 0 || target.AuditToken[5] != target.PID || target.AuditToken[7] == 0 {
		return errors.New("authenticated process target required")
	}
	var started C.uint64_t
	code := C.dsh_target_started(C.uint32_t(target.PID), &started)
	if code == C.ESRCH {
		return os.ErrProcessDone
	}
	if code != 0 {
		return syscall.Errno(code)
	}
	if uint64(started) != target.Started {
		return ErrTargetStale
	}
	// The kernel checks pidversion while holding the process reference. There is
	// deliberately no PID-only fallback after any observation or signal error.
	code = C.dsh_target_signal((*C.uint32_t)(unsafe.Pointer(&target.AuditToken[0])), C.int(signal))
	if code == C.ESRCH {
		return os.ErrProcessDone
	}
	if code != 0 {
		return syscall.Errno(code)
	}
	return nil
}
