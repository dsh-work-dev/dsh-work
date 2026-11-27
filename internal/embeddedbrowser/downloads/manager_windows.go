//go:build windows

package downloads

import (
	"fmt"

	win "golang.org/x/sys/windows"
)

func (m *Manager) DuplicateWriter(id string, brokerProcess win.Handle) (uintptr, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.transfers[id]
	if t == nil || t.entry.State != "starting" || t.destination == nil || t.writerIssued {
		return 0, ErrApprovalExpired
	}
	h, err := t.destination.DuplicateWriter(brokerProcess)
	if err == nil {
		t.writerIssued = true
	}
	return h, err
}

func replaceDownloadJournal(name, destination string) error {
	from, err := win.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	to, err := win.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	return win.MoveFileEx(from, to, win.MOVEFILE_REPLACE_EXISTING|win.MOVEFILE_WRITE_THROUGH)
}

func checkFree(parent string, reserve uint64, total int64) error {
	p, e := win.UTF16PtrFromString(parent)
	if e != nil {
		return e
	}
	var free uint64
	if e = win.GetDiskFreeSpaceEx(p, &free, nil, nil); e != nil {
		return e
	}
	need := reserve
	if total > 0 {
		if uint64(total) > ^uint64(0)-need {
			return ErrBudget
		}
		need += uint64(total)
	}
	if free < need {
		return fmt.Errorf("DOWNLOAD_DISK_SPACE_REQUIRED: %d", need)
	}
	return nil
}
