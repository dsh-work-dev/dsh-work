package selfupdate

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var (
	// ErrStagedPayloadChanged reports a staged installer whose bytes no longer
	// verify. The copy is discarded rather than run.
	ErrStagedPayloadChanged = errors.New("staged installer changed after verification")
	// ErrStagingUnavailable reports a staging directory this process cannot use.
	ErrStagingUnavailable = errors.New("update staging directory is unavailable")
)

// stagedFilename is the name one verified installer is stored under. A single
// release unit is updated at a time, so one stable name is enough and makes a
// leftover payload obvious.
const stagedFilename = "dsh-work-installer.exe"

// stagedDirMode and stagedFileMode keep the staging directory readable and
// writable by the current user only. The payload is an executable that this
// process will start, so no other account may be able to replace it.
const (
	stagedDirMode  os.FileMode = 0o700
	stagedFileMode os.FileMode = 0o600
)

// Staging owns the directory where one verified installer payload is kept
// between the download and the install handshake.
//
// The upstream updater stages its artifact in a temporary directory it deletes
// on the next download, so its copy cannot be the durable, verified copy the
// install step re-checks. The verified bytes are therefore copied here, and
// every install re-verifies the bytes on disk before any process is
// stopped.
type Staging struct {
	dir string
}

// NewStaging returns the staging area below the supplied application data
// directory, creating it with user-only access.
func NewStaging(baseDir string) (*Staging, error) {
	base := strings.TrimSpace(baseDir)
	if base == "" {
		return nil, fmt.Errorf("%w: no application data directory", ErrStagingUnavailable)
	}
	absolute, err := filepath.Abs(filepath.Join(base, "update-staging"))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrStagingUnavailable, err)
	}
	if err := os.MkdirAll(absolute, stagedDirMode); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrStagingUnavailable, err)
	}
	// MkdirAll applies the process umask; the directory mode is set again so
	// the payload cannot be observed or replaced by another account.
	if err := os.Chmod(absolute, stagedDirMode); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrStagingUnavailable, err)
	}
	return &Staging{dir: absolute}, nil
}

// Dir reports the staging directory. It is diagnostic information and is not
// part of any state projected to the user interface.
func (s *Staging) Dir() string {
	if s == nil {
		return ""
	}
	return s.dir
}

// Stage verifies a downloaded payload and keeps a durable copy of exactly the
// bytes that verified.
//
// The payload digest is checked before it is copied and again after the copy
// is written, the write only becomes visible under the final name once both
// checks pass, and the staged copy then passes the full installer checks,
// including its Authenticode publisher. A failure leaves no runnable payload
// behind.
func (s *Staging) Stage(sourcePath string, feed *FeedRelease) (*Installer, error) {
	if s == nil {
		return nil, fmt.Errorf("%w: no staging directory", ErrUnverifiedPayload)
	}
	if err := VerifyDigestFile(sourcePath, feed); err != nil {
		return nil, err
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInstallerUnavailable, err)
	}
	defer source.Close()

	// The copy lands in the same directory as its final name so the rename that
	// publishes it is atomic and cannot cross a volume boundary.
	target, err := os.CreateTemp(s.dir, ".staged-*.tmp")
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrStagingUnavailable, err)
	}
	temporary := target.Name()
	discard := func() {
		target.Close()
		os.Remove(temporary)
	}
	if err := target.Chmod(stagedFileMode); err != nil {
		discard()
		return nil, fmt.Errorf("%w: %v", ErrStagingUnavailable, err)
	}
	if _, err := io.Copy(target, source); err != nil {
		discard()
		return nil, fmt.Errorf("%w: %v", ErrStagingUnavailable, err)
	}
	if err := target.Sync(); err != nil {
		discard()
		return nil, fmt.Errorf("%w: %v", ErrStagingUnavailable, err)
	}
	if err := target.Close(); err != nil {
		os.Remove(temporary)
		return nil, fmt.Errorf("%w: %v", ErrStagingUnavailable, err)
	}
	if err := VerifyDigestFile(temporary, feed); err != nil {
		os.Remove(temporary)
		return nil, err
	}
	final := filepath.Join(s.dir, stagedFilename)
	if err := os.Rename(temporary, final); err != nil {
		os.Remove(temporary)
		return nil, fmt.Errorf("%w: %v", ErrStagingUnavailable, err)
	}
	return Prepare(final, feed, ProductionOptions())
}

// Recheck re-verifies the staged installer immediately before it is
// started. The bytes on disk are compared with the release the feed
// advertised, so a payload that was replaced after it was staged is refused
// while the old installation is still running.
func (s *Staging) Recheck(installer *Installer, feed *FeedRelease) error {
	if s == nil || installer == nil {
		return fmt.Errorf("%w: no staged installer", ErrInstallerUnavailable)
	}
	expected, err := filepath.Abs(filepath.Join(s.dir, stagedFilename))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrStagingUnavailable, err)
	}
	actual, err := filepath.Abs(installer.Path)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInstallerUnavailable, err)
	}
	if actual != expected {
		return fmt.Errorf("%w: %s is not the staged installer", ErrInstallerUnavailable, installer.Path)
	}
	if err := VerifyDigestFile(actual, feed); err != nil {
		return fmt.Errorf("%w: %v", ErrStagedPayloadChanged, err)
	}
	if _, err := Prepare(actual, feed, ProductionOptions()); err != nil {
		return fmt.Errorf("%w: %v", ErrStagedPayloadChanged, err)
	}
	return nil
}

// Clear removes any staged payload. It is called when the subscription, the
// release, or the trust configuration changes so a stale installer can never be
// installed under a newer identity.
func (s *Staging) Clear() error {
	if s == nil {
		return nil
	}
	removeErr := os.Remove(filepath.Join(s.dir, stagedFilename))
	if removeErr != nil && !os.IsNotExist(removeErr) {
		return removeErr
	}
	leftovers, err := filepath.Glob(filepath.Join(s.dir, ".staged-*.tmp"))
	if err != nil {
		return err
	}
	for _, leftover := range leftovers {
		if err := os.Remove(leftover); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
