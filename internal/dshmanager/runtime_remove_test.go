package dshmanager

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

type removingRuntimeInstaller struct {
	removed []string
	err     error
}

func (i *removingRuntimeInstaller) Install(context.Context, string) (RuntimeInfo, error) {
	return RuntimeInfo{}, errors.New("not used")
}

func (i *removingRuntimeInstaller) Remove(_ context.Context, runtime RuntimeInfo) error {
	if i.err != nil {
		return i.err
	}
	i.removed = append(i.removed, runtime.ID)
	return nil
}

func newRemovableRuntimeManager(t *testing.T, installer RuntimeInstaller) *Manager {
	t.Helper()
	manager := newTestManager(t)
	manager.config.RuntimeInstaller = installer
	if _, err := manager.RegisterRuntime(context.Background(), RuntimeInfo{
		ID: "dsh-9.9.9", Version: "9.9.9", Path: filepath.Join(t.TempDir(), "dsh.cmd"), Source: RuntimeSourceManaged,
	}); err != nil {
		t.Fatal(err)
	}
	return manager
}

func TestRemoveRuntimeDeletesManagedInstallation(t *testing.T) {
	installer := &removingRuntimeInstaller{}
	manager := newRemovableRuntimeManager(t, installer)
	snapshot, err := manager.RemoveRuntime(context.Background(), "dsh-9.9.9")
	if err != nil {
		t.Fatal(err)
	}
	if len(installer.removed) != 1 || installer.removed[0] != "dsh-9.9.9" {
		t.Fatalf("installation removals = %#v", installer.removed)
	}
	if _, exists := findRuntime(snapshot.Runtimes, "dsh-9.9.9"); exists {
		t.Fatalf("runtime still listed: %#v", snapshot.Runtimes)
	}
}

func TestRemoveRuntimeKeepsCatalogWhenInstallationRemovalFails(t *testing.T) {
	installer := &removingRuntimeInstaller{err: errors.New("file in use")}
	manager := newRemovableRuntimeManager(t, installer)
	if _, err := manager.RemoveRuntime(context.Background(), "dsh-9.9.9"); err == nil {
		t.Fatal("RemoveRuntime() error = nil, want removal failure")
	}
	snapshot, err := manager.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := findRuntime(snapshot.Runtimes, "dsh-9.9.9"); !exists {
		t.Fatalf("failed removal dropped runtime: %#v", snapshot.Runtimes)
	}
}
