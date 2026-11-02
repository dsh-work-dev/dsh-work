package main

import (
	"context"
	_ "embed"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

//go:embed verify-registration.ps1
var registrationProbe []byte

// verifyNativePathChange tests the real helper/registry contract before any
// browser connects. It restores the original executable path on every exit.
func verifyNativePathChange(ctx context.Context, root, evidence string) (err error) {
	ctx, stop := context.WithTimeout(ctx, 30*time.Second)
	defer stop()
	original := filepath.Join(root, "dsh-work.exe")
	dir, err := os.MkdirTemp(root, "moved-host-")
	if err != nil {
		return err
	}
	moved := filepath.Join(dir, "dsh-work.exe")
	in, err := os.Open(original)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(moved, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	closeErr := out.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	probe := filepath.Join(root, "verify-registration.ps1")
	if err = os.WriteFile(probe, registrationProbe, 0600); err != nil {
		return err
	}
	helper := func(run context.Context, exe, command string) error {
		c := exec.CommandContext(run, exe, command)
		c.Env = append(os.Environ(), "DSH_WORK_DESKTOP_ROOT="+root, "DSH_WORK_DESKTOP_REPORT="+filepath.Join(root, "desktop-report.json"))
		body, err := c.CombinedOutput()
		if err != nil {
			return fmt.Errorf("%s %s: %w: %s", exe, command, err, body)
		}
		return nil
	}
	defer func() {
		recovery, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if restoreErr := helper(recovery, original, "browser-host-refresh"); restoreErr != nil {
			err = fmt.Errorf("path test: %v; original path recovery: %w", err, restoreErr)
		}
	}()
	probePath := func(name, expected string) error {
		body, err := exec.CommandContext(ctx, "pwsh", "-NoProfile", "-File", probe, "-Root", root, "-ExpectedExecutable", expected).CombinedOutput()
		_ = os.WriteFile(filepath.Join(evidence, name+".json"), body, 0600)
		if err != nil {
			return fmt.Errorf("live path registration probe %s: %w: %s", name, err, body)
		}
		return nil
	}
	if err = helper(ctx, moved, "browser-host-refresh"); err != nil {
		return err
	}
	if err = helper(ctx, original, "browser-host-remove"); err != nil {
		return err
	}
	if err = probePath("registration-moved-survives-old-remove", moved); err != nil {
		return err
	}
	if err = helper(ctx, original, "browser-host-refresh"); err != nil {
		return err
	}
	if err = helper(ctx, moved, "browser-host-remove"); err != nil {
		return err
	}
	return probePath("registration-original-survives-moved-remove", original)
}
