//go:build darwin

package downloads

import (
	"errors"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

func macStageName(r FileRecord) (string, error) {
	directory := filepath.Dir(r.Stage)
	token := strings.TrimPrefix(directory, macStagePrefix)
	if !filepath.IsAbs(r.Parent) || filepath.Clean(r.Parent) != r.Parent || !macChild(r.Name) ||
		r.StageDirectory == nil || !strings.HasPrefix(directory, macStagePrefix) || !macChild(directory) ||
		len(token) != 26 || strings.Trim(token, "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567") != "" ||
		r.Stage != filepath.Join(directory, macPartial) ||
		(r.Recovery != "" && (r.Previous == nil || r.Recovery != filepath.Join(directory, macPrevious))) {
		return "", ErrOwnedFileChanged
	}
	return directory, nil
}

func openMacRecovery(r FileRecord) (int, int, string, error) {
	name, err := macStageName(r)
	if err != nil {
		return -1, -1, "", err
	}
	parent, err := openMacDownloadDirectory(unix.AT_FDCWD, r.Parent)
	if err != nil {
		return -1, -1, "", err
	}
	var pi unix.Stat_t
	if err = unix.Fstat(parent, &pi); err != nil || macIdentity(pi) != r.ParentIdentity {
		unix.Close(parent)
		return -1, -1, "", errors.Join(ErrDestinationChanged, err)
	}
	dir, err := openMacDownloadDirectory(parent, name)
	if errors.Is(err, unix.ENOENT) {
		return parent, -1, name, nil
	}
	if err != nil {
		unix.Close(parent)
		return -1, -1, "", err
	}
	var info unix.Stat_t
	if err = unix.Fstat(dir, &info); err != nil || !macPrivateDirectory(info) || macIdentity(info) != *r.StageDirectory {
		unix.Close(dir)
		unix.Close(parent)
		return -1, -1, "", errors.Join(ErrOwnedFileChanged, err)
	}
	return parent, dir, name, nil
}

// RecoverFile verifies a recorded transaction after writer cleanup. It leaves
// the original recovery file intact until publication is durable in the ledger.
func RecoverFile(r FileRecord) (FileRecord, error) {
	parent, dir, name, err := openMacRecovery(r)
	if err != nil {
		return r, err
	}
	defer unix.Close(parent)
	if dir >= 0 {
		defer unix.Close(dir)
	}
	final, err := macStatAt(parent, r.Name)
	finalMissing := errors.Is(err, unix.ENOENT)
	if err != nil && !finalMissing {
		return r, err
	}
	r.Published = err == nil && macRegular(final) && macIdentity(final) == r.StageIdentity
	if r.Published {
		r.Phase = "published"
	}
	if r.Recovery != "" {
		old := unix.Stat_t{}
		oldErr := error(unix.ENOENT)
		if dir >= 0 {
			old, oldErr = macStatAt(dir, macPrevious)
		}
		if errors.Is(oldErr, unix.ENOENT) {
			originalInPlace := !finalMissing && macRegular(final) && macVersion(final) == *r.Previous
			if !r.Published && !originalInPlace {
				return r, ErrOwnedFileChanged
			}
			r.Recovery = ""
		} else if oldErr != nil || !macRegular(old) || macVersion(old) != *r.Previous {
			return r, errors.Join(ErrOwnedFileChanged, oldErr)
		} else if !r.Published {
			if !finalMissing {
				return r, ErrDestinationChanged
			}
			if err = macRenameExclusive(dir, macPrevious, parent, r.Name); err != nil {
				return r, err
			}
			if err = errors.Join(unix.Fsync(parent), unix.Fsync(dir)); err != nil {
				return r, err
			}
			r.Recovery = ""
		}
	}
	if dir >= 0 {
		if err = removeMacPartial(dir, r.StageIdentity); err != nil {
			return r, err
		}
		if r.Recovery == "" {
			if err = removeMacStageDir(parent, name, *r.StageDirectory); err != nil {
				return r, err
			}
		}
	}
	if !r.Published {
		r.Phase = "discarded"
	}
	return r, nil
}

func CleanupPublishedRecovery(r FileRecord) (FileRecord, error) {
	if !r.Published || r.Recovery == "" {
		return r, nil
	}
	checked, err := RecoverFile(r)
	if err != nil || !checked.Published || checked.Recovery == "" {
		return checked, err
	}
	parent, dir, name, err := openMacRecovery(checked)
	if err != nil {
		return checked, err
	}
	defer unix.Close(parent)
	if dir < 0 {
		return checked, ErrOwnedFileChanged
	}
	defer unix.Close(dir)
	final, err := macStatAt(parent, checked.Name)
	if err != nil || !macRegular(final) || macIdentity(final) != checked.StageIdentity {
		return checked, errors.Join(ErrDestinationChanged, err)
	}
	if err = removeMacPrevious(dir, checked.Previous); err != nil {
		return checked, err
	}
	checked.Recovery = ""
	return checked, removeMacStageDir(parent, name, *checked.StageDirectory)
}
