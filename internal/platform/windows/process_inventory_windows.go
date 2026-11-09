//go:build windows

package windows

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"unsafe"

	"github.com/local/dsh-work/internal/supervisor"
	win "golang.org/x/sys/windows"
)

var isProcessInJob = win.NewLazySystemDLL("kernel32.dll").NewProc("IsProcessInJob")

// OwnedProcesses obtains leases from this Job, never a system-wide census.
func (w *jobWorker) OwnedProcesses(ctx context.Context) ([]supervisor.ProcessLease, error) {
	w.closeMu.Lock()
	defer w.closeMu.Unlock()
	if w.closed {
		return nil, errors.New("process inventory owner closed")
	}
	var pids []uint32
	queried := false
	for capacity := 32; capacity <= 16384; capacity *= 2 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		size := 8 + capacity*int(unsafe.Sizeof(uintptr(0)))
		data := make([]byte, size)
		err := win.QueryInformationJobObject(w.job, win.JobObjectBasicProcessIdList, uintptr(unsafe.Pointer(&data[0])), uint32(size), nil)
		if errors.Is(err, win.ERROR_MORE_DATA) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("owned process list: %w", err)
		}
		count := binary.LittleEndian.Uint32(data[4:8])
		if count > uint32(capacity) {
			return nil, errors.New("owned process list overflow")
		}
		for i := 0; i < int(count); i++ {
			offset := 8 + i*int(unsafe.Sizeof(uintptr(0)))
			value := *(*uintptr)(unsafe.Pointer(&data[offset]))
			if value == 0 || value > 1<<32-1 {
				return nil, errors.New("owned process id invalid")
			}
			pids = append(pids, uint32(value))
		}
		queried = true
		break
	}
	if !queried {
		return nil, errors.New("owned process inventory unavailable")
	}
	var leases []supervisor.ProcessLease
	var result error
	for _, pid := range pids {
		if err := ctx.Err(); err != nil {
			return leases, errors.Join(result, err)
		}
		lease, err := w.openOwnedProcess(pid)
		if err != nil {
			result = errors.Join(result, err)
			continue
		}
		leases = append(leases, lease)
	}
	return leases, result
}

func (w *jobWorker) openOwnedProcess(pid uint32) (*jobProcessLease, error) {
	handle, err := win.OpenProcess(win.PROCESS_QUERY_LIMITED_INFORMATION|win.SYNCHRONIZE, false, pid)
	if err != nil {
		return nil, fmt.Errorf("owned process handle: %w", err)
	}
	keep := false
	defer func() {
		if !keep {
			win.CloseHandle(handle)
		}
	}()
	var owned int32
	ok, _, callErr := isProcessInJob.Call(uintptr(handle), uintptr(w.job), uintptr(unsafe.Pointer(&owned)))
	if ok == 0 {
		return nil, fmt.Errorf("owned process membership query: %w", callErr)
	}
	if owned == 0 {
		return nil, errors.New("process is not in this Job")
	}
	var created, exited, kernel, user win.Filetime
	if err = win.GetProcessTimes(handle, &created, &exited, &kernel, &user); err != nil {
		return nil, err
	}
	birth := uint64(created.HighDateTime)<<32 | uint64(created.LowDateTime)
	if birth == 0 {
		return nil, errors.New("owned process creation unavailable")
	}
	image := make([]uint16, 32768)
	n := uint32(len(image))
	if err = win.QueryFullProcessImageName(handle, 0, &image[0], &n); err != nil {
		return nil, err
	}
	lease := &jobProcessLease{handle: handle, identity: supervisor.ProcessInstance{PID: pid, Started: birth, Image: win.UTF16ToString(image[:n])}}
	// A signaled instance still belongs to the census and the held handle.
	// Returning it lets the caller distinguish an observed exit from lost OS
	// identity; subsequent Alive reports false without discarding the instance.
	if _, err := lease.Alive(); err != nil {
		return nil, err
	}
	keep = true
	return lease, nil
}

type jobProcessLease struct {
	mu       sync.Mutex
	handle   win.Handle
	identity supervisor.ProcessInstance
}

func (p *jobProcessLease) Instance() supervisor.ProcessInstance { return p.identity }
func (p *jobProcessLease) aliveLocked() (bool, error) {
	if p.handle == 0 {
		return false, errors.New("process lease closed")
	}
	status, err := win.WaitForSingleObject(p.handle, 0)
	if err != nil {
		return false, err
	}
	if status == win.WAIT_OBJECT_0 {
		return false, nil
	}
	if status == uint32(win.WAIT_TIMEOUT) {
		return true, nil
	}
	return false, errors.New("unexpected process wait status")
}
func (p *jobProcessLease) Alive() (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.aliveLocked()
}
func (p *jobProcessLease) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.handle == 0 {
		return nil
	}
	if err := win.CloseHandle(p.handle); err != nil {
		return err
	}
	p.handle = 0
	return nil
}

func (p *jobProcessLease) Arguments(ctx context.Context) ([]string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if alive, err := p.aliveLocked(); err != nil || !alive {
		return nil, errors.Join(errors.New("process is no longer live"), err)
	}
	// x/sys exposes the maintained NT API and structure. No PEB traversal,
	// ReadProcessMemory, external shell/WMI, or PID-only process reconstruction.
	var needed uint32
	_ = win.NtQueryInformationProcess(p.handle, win.ProcessCommandLineInformation, nil, 0, &needed)
	if needed < uint32(unsafe.Sizeof(win.NTUnicodeString{})) || needed > 1<<20 {
		return nil, errors.New("owned command line length unavailable")
	}
	data := make([]byte, needed)
	if err := win.NtQueryInformationProcess(p.handle, win.ProcessCommandLineInformation, unsafe.Pointer(&data[0]), needed, &needed); err != nil {
		return nil, fmt.Errorf("owned command line query: %w", err)
	}
	text := *(*win.NTUnicodeString)(unsafe.Pointer(&data[0]))
	base := uintptr(unsafe.Pointer(&data[0]))
	pointer := uintptr(unsafe.Pointer(text.Buffer))
	if text.Length%2 != 0 || text.Length > text.MaximumLength || pointer < base || pointer-base > uintptr(len(data)) || uintptr(text.Length) > uintptr(len(data))-(pointer-base) {
		return nil, errors.New("owned command line bounds rejected")
	}
	offset := int(pointer - base)
	units := make([]uint16, int(text.Length)/2)
	for i := range units {
		units[i] = binary.LittleEndian.Uint16(data[offset+2*i : offset+2*i+2])
	}
	if alive, err := p.aliveLocked(); err != nil || !alive {
		return nil, errors.Join(errors.New("process exited during command line query"), err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return win.DecomposeCommandLine(win.UTF16ToString(units))
}
