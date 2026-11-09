//go:build windows

package downloads

import (
	"errors"
	win "golang.org/x/sys/windows"
	"path/filepath"
	"strings"
)

// RecoverFile is called under the existing profile lease after the previous
// broker/writers have drained. It never replays a URL or deletes by filename.
// A conflicting new final occupant is left intact and the previous object
// remains at its explicit recovery path.
func RecoverFile(r FileRecord) (FileRecord, error) {
	if !filepath.IsAbs(r.Parent) || !validChild(r.Name) || !strings.HasPrefix(r.Stage, ".dsh-download-") || !validChild(r.Stage) || (r.Recovery != "" && (!validChild(r.Recovery) || !strings.HasPrefix(r.Recovery, ".dsh-download-recovery-"))) {
		return r, ErrOwnedFileChanged
	}
	p, e := win.UTF16PtrFromString(r.Parent)
	if e != nil {
		return r, e
	}
	parent, e := win.CreateFile(p, win.FILE_READ_ATTRIBUTES|win.FILE_TRAVERSE, win.FILE_SHARE_READ|win.FILE_SHARE_WRITE, nil, win.OPEN_EXISTING, win.FILE_FLAG_BACKUP_SEMANTICS|win.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if e != nil {
		return r, e
	}
	defer win.CloseHandle(parent)
	pi, e := fileInfo(parent)
	if e != nil {
		return r, e
	}
	if identity(pi) != r.ParentIdentity || pi.FileAttributes&win.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return r, ErrDestinationChanged
	}
	// Establish publication from the held object's ID, including a crash between
	// rename and its journal update.
	target, te := openChild(parent, r.Name, win.FILE_READ_ATTRIBUTES, win.FILE_SHARE_READ|win.FILE_SHARE_WRITE|win.FILE_SHARE_DELETE, win.FILE_OPEN)
	if te == nil {
		ti, ie := fileInfo(target)
		win.CloseHandle(target)
		if ie != nil {
			return r, ie
		}
		r.Published = identity(ti) == r.StageIdentity
	} else if !notExist(te) {
		return r, te
	} else {
		r.Published = false
	}
	if r.Recovery != "" {
		if r.Previous == nil {
			return r, ErrOwnedFileChanged
		}
		old, oe := openChild(parent, r.Recovery, win.FILE_READ_ATTRIBUTES|win.DELETE, win.FILE_SHARE_READ, win.FILE_OPEN)
		if oe == nil {
			oi, ie := fileInfo(old)
			if ie != nil || version(oi) != *r.Previous {
				win.CloseHandle(old)
				return r, ErrOwnedFileChanged
			}
			if !r.Published {
				if e = renameNoReplace(old, parent, r.Name); e == nil {
					r.Recovery = ""
				} else {
					win.CloseHandle(old)
					return r, errors.New("DOWNLOAD_RECOVERY_REQUIRED")
				}
			}
			// Published + recovery: retain it until the manager durably records the
			// publication. CleanupPublishedRecovery then deletes this exact old object.
			win.CloseHandle(old)
		} else if notExist(oe) {
			// Intent can precede the move. Only clear it if the original remains at
			// its approved name, or a durably published cleanup already removed it.
			h, he := openChild(parent, r.Name, win.FILE_READ_ATTRIBUTES, win.FILE_SHARE_READ|win.FILE_SHARE_WRITE|win.FILE_SHARE_DELETE, win.FILE_OPEN)
			if he == nil {
				info, ie := fileInfo(h)
				win.CloseHandle(h)
				if ie == nil && (version(info) == *r.Previous || r.Published) {
					r.Recovery = ""
				}
			}
		} else {
			return r, oe
		}
	}
	stage, se := openChild(parent, r.Stage, win.FILE_READ_ATTRIBUTES|win.DELETE, win.FILE_SHARE_READ|win.FILE_SHARE_WRITE, win.FILE_OPEN)
	if se == nil {
		si, ie := fileInfo(stage)
		if ie != nil || identity(si) != r.StageIdentity {
			win.CloseHandle(stage)
			return r, ErrOwnedFileChanged
		}
		e = deleteHeld(stage)
		win.CloseHandle(stage)
		if e != nil {
			return r, e
		}
	} else if !notExist(se) {
		return r, se
	}
	if !r.Published {
		r.Phase = "discarded"
	}
	return r, nil
}
func CleanupPublishedRecovery(r FileRecord) (FileRecord, error) {
	if !r.Published || r.Recovery == "" || r.Previous == nil {
		return r, nil
	}
	// Reuse the identity checks without marking or deleting the final file.
	checked, e := RecoverFile(r)
	if e != nil {
		return checked, e
	}
	r = checked
	if !r.Published || r.Recovery == "" {
		return r, nil
	}
	p, e := win.UTF16PtrFromString(r.Parent)
	if e != nil {
		return r, e
	}
	parent, e := win.CreateFile(p, win.FILE_READ_ATTRIBUTES|win.FILE_TRAVERSE, win.FILE_SHARE_READ|win.FILE_SHARE_WRITE, nil, win.OPEN_EXISTING, win.FILE_FLAG_BACKUP_SEMANTICS|win.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if e != nil {
		return r, e
	}
	defer win.CloseHandle(parent)
	pi, e := fileInfo(parent)
	if e != nil || identity(pi) != r.ParentIdentity {
		return r, ErrDestinationChanged
	}
	h, e := openChild(parent, r.Recovery, win.FILE_READ_ATTRIBUTES|win.DELETE, win.FILE_SHARE_READ, win.FILE_OPEN)
	if e != nil {
		return r, e
	}
	defer win.CloseHandle(h)
	i, e := fileInfo(h)
	if e != nil || version(i) != *r.Previous {
		return r, ErrOwnedFileChanged
	}
	// Verify the new final still denotes this transaction before deleting old.
	final, e := openChild(parent, r.Name, win.FILE_READ_ATTRIBUTES, win.FILE_SHARE_READ|win.FILE_SHARE_WRITE, win.FILE_OPEN)
	if e != nil {
		return r, e
	}
	defer win.CloseHandle(final)
	fi, e := fileInfo(final)
	if e != nil || identity(fi) != r.StageIdentity {
		return r, ErrDestinationChanged
	}
	if e = deleteHeld(h); e != nil {
		return r, e
	}
	r.Recovery = ""
	return r, nil
}
