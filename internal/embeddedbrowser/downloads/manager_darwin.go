//go:build darwin && cgo

package downloads

import (
	"errors"
	"fmt"
	"math/bits"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// DiscardAbandonedMacStages runs after the exact broker and the Host's
// publication tasks have stopped. Abort only unlinks the pinned private
// pathname. An outstanding descriptor can only write its anonymous inode,
// while process-tree and profile retirement still require their own proof.
func (m *Manager) DiscardAbandonedMacStages(generation string) error {
	return m.brokerFailed(generation)
}

// OpenWriter issues one distinct write-only descriptor after cancellation has
// been armed. Its caller holds it until native transfer or explicit closure.
func (m *Manager) OpenWriter(id string) (*os.File, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.transfers[id]
	if t == nil || t.entry.State != "starting" || t.destination == nil || t.writerIssued {
		return nil, ErrApprovalExpired
	}
	file, err := t.destination.OpenWriter()
	if err == nil {
		t.writerIssued = true
	}
	return file, err
}

func replaceDownloadJournal(name, destination string) error {
	if err := os.Rename(name, destination); err != nil {
		return err
	}
	parent, err := os.Open(filepath.Dir(destination))
	if err != nil {
		return err
	}
	return errors.Join(parent.Sync(), parent.Close())
}

func checkFree(parent string, reserve uint64, total int64) error {
	var info unix.Statfs_t
	if err := unix.Statfs(parent, &info); err != nil {
		return err
	}
	need := reserve
	if total > 0 {
		var overflow uint64
		need, overflow = bits.Add64(reserve, uint64(total), 0)
		if overflow != 0 {
			return ErrBudget
		}
	}
	high, free := bits.Mul64(info.Bavail, uint64(info.Bsize))
	if high == 0 && free < need {
		return fmt.Errorf("DOWNLOAD_DISK_SPACE_REQUIRED: %d", need)
	}
	return nil
}
