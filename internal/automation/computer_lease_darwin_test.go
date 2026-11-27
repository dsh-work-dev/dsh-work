//go:build darwin && cgo

package automation

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMacComputerDesktopLeaseFencesSessionAndRejectsSubstitution(t *testing.T) {
	root, uid := t.TempDir(), uint32(os.Geteuid())
	acquire := func(session uint32) func() error {
		t.Helper()
		release, err := acquireMacComputerLease(root, uid, session)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := release(); err != nil {
				t.Error(err)
			}
		})
		return release
	}
	release := acquire(1)
	if duplicate, err := acquireMacComputerLease(root, uid, 1); duplicate != nil || err == nil || !strings.Contains(err.Error(), "COMPUTER_BUSY") {
		t.Fatalf("second owner of one desktop: release=%t error=%v", duplicate != nil, err)
	}
	acquire(2)
	if err := release(); err != nil {
		t.Fatal(err)
	}
	acquire(1)
	target := filepath.Join(root, "untouched")
	if err := os.WriteFile(target, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, fmt.Sprintf("dsh-work-computer-%d", uid), "session-00000003.lock")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if substituted, err := acquireMacComputerLease(root, uid, 3); substituted != nil || err == nil {
		t.Fatalf("substituted lock accepted: release=%t error=%v", substituted != nil, err)
	}
}
