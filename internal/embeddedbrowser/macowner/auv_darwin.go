//go:build darwin && cgo

package macowner

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/supervisor"
)

const auvOwnerVersion = "owned-auv:" + automation.AUVVersion

// AUVAdapter owns one pinned desktop adapter and its foreground AUV server in
// the guard's LaunchAgent group. It does not admit arbitrary desktop commands.
type AUVAdapter struct {
	publication                      Publication
	node, dependencies, driver, root string
	driverDevice, driverInode        uint64
}

func NewAUVAdapter(ctx context.Context, p Publication, node, dependencies, root string) (*AUVAdapter, error) {
	if !p.verified || !filepath.IsAbs(node) || !filepath.IsAbs(dependencies) || !filepath.IsAbs(root) {
		return nil, errors.New("MAC_AUV_CONFIGURATION_REQUIRED")
	}
	node, err := filepath.EvalSymlinks(node)
	if err != nil {
		return nil, err
	}
	driver, err := automation.AUVExecutable(dependencies)
	if err != nil {
		return nil, err
	}
	locks, err := automation.DependencyFiles(automation.AUV)
	if err != nil {
		return nil, err
	}
	for filename, expected := range locks {
		actual, err := os.ReadFile(filepath.Join(dependencies, filename))
		if err != nil || !bytes.Equal(actual, expected) {
			return nil, errors.Join(errors.New("MAC_AUV_LOCK_REJECTED"), err)
		}
	}
	name, _, err := automation.AUVNativePackage()
	if err != nil {
		return nil, err
	}
	for _, pkg := range []string{"@auv-js/sdk", name} {
		data, err := os.ReadFile(filepath.Join(dependencies, "node_modules", pkg, "package.json"))
		if err != nil {
			return nil, err
		}
		var info struct{ Version string }
		if json.Unmarshal(data, &info) != nil || info.Version != automation.AUVVersion {
			return nil, errors.New("MAC_AUV_PUBLICATION_REJECTED")
		}
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	device, inode, err := fileIdentity(driver)
	if err != nil {
		return nil, err
	}
	bounded, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	if err := exec.CommandContext(bounded, "/usr/bin/codesign", "--verify", "--strict", "-R="+automation.MacAUVCodeRequirement, driver).Run(); err != nil {
		return nil, errors.Join(errors.New("MAC_AUV_PUBLISHER_REJECTED"), err)
	}
	if !pinnedJobFile(driver, driver, device, inode) {
		return nil, errors.New("MAC_AUV_COMPONENT_REJECTED")
	}
	return &AUVAdapter{
		publication: p, node: node, dependencies: dependencies, driver: driver, root: root,
		driverDevice: device, driverInode: inode,
	}, nil
}

func (a *AUVAdapter) Start(ctx context.Context, plan supervisor.LaunchPlan, raw supervisor.RawOutputHandler) (supervisor.Worker, error) {
	if err := plan.Validate(); err != nil {
		return nil, err
	}
	dir, err := filepath.EvalSymlinks(plan.WorkingDirectory)
	if err != nil {
		return nil, err
	}
	if filepath.Dir(dir) != a.root || !strings.HasPrefix(filepath.Base(dir), "desktop-") {
		return nil, errors.New("MAC_AUV_RESOURCE_ROOT_REJECTED")
	}
	script := filepath.Join(dir, "adapter.mjs")
	if len(plan.Args) != 1 || !sameJobFile(plan.Executable, a.node) || !sameJobFile(plan.Args[0], script) {
		return nil, errors.New("MAC_AUV_LAUNCH_PLAN_REJECTED")
	}
	for name, value := range plan.Env {
		upper := strings.ToUpper(name)
		if value != "" && (strings.HasPrefix(upper, "AUV_") || strings.HasPrefix(upper, "DSH_WORK_OWNED_AUV_") ||
			upper == "NODE_OPTIONS" || upper == "NODE_PATH" || strings.HasPrefix(upper, "DYLD_")) {
			return nil, errors.New("MAC_AUV_CONFIG_OVERRIDE_REJECTED")
		}
	}
	if err := privateDir(dir); err != nil {
		return nil, err
	}
	sidecar, err := readPrivate(filepath.Join(dir, "native-connection.json"))
	if err != nil {
		return nil, err
	}
	var connection struct{ NativeBridge string }
	host, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if json.Unmarshal(sidecar, &connection) != nil || !sameJobFile(connection.NativeBridge, filepath.Join(filepath.Dir(host), "workeripc-mac.node")) {
		return nil, errors.New("WORKER_IPC_MAC_BRIDGE_MISSING")
	}
	device, inode, err := fileIdentity(a.node)
	if err != nil {
		return nil, err
	}
	if !pinnedJobFile(a.driver, a.driver, a.driverDevice, a.driverInode) {
		return nil, errors.New("MAC_AUV_COMPONENT_REJECTED")
	}
	if err := a.recoverPrevious(ctx, dir); err != nil {
		return nil, err
	}
	ownerDir := filepath.Join(dir, ".dsh-auv-owner")
	if err := privateDir(ownerDir); err != nil {
		return nil, err
	}
	inner := &Adapter{publication: a.publication, profile: dir, directory: ownerDir, mcp: &mcpConfig{
		Node: a.node, NodeDevice: device, NodeInode: inode, Adapter: script, Dependencies: a.dependencies,
		Bridge: connection.NativeBridge, AUVDriver: a.driver, AUVDevice: a.driverDevice, AUVInode: a.driverInode,
	}}
	plan.GenerationID = "cef-AUV" + rand.Text()
	worker, err := inner.Start(ctx, plan, raw)
	if inner.worker != nil {
		return &auvWorker{Worker: inner.worker}, err
	}
	return worker, err
}

// RecoverResource retires only an authenticated AUV generation. The caller
// must hold its desktop lease before attempting recovery or a new launch.
func (a *AUVAdapter) RecoverResource(ctx context.Context, resource string) error {
	dir, err := filepath.EvalSymlinks(resource)
	if err != nil || filepath.Dir(dir) != a.root || !strings.HasPrefix(filepath.Base(dir), "desktop-") {
		return errors.Join(errors.New("MAC_AUV_RESOURCE_ROOT_REJECTED"), err)
	}
	ownerDir := filepath.Join(dir, ".dsh-auv-owner")
	if info, err := os.Lstat(ownerDir); err != nil || !info.IsDir() {
		return errors.Join(ErrCleanupPending, err)
	}
	if err := privateDir(ownerDir); err != nil {
		return err
	}
	key, err := readPrivate(filepath.Join(ownerDir, "journal.key"))
	if err != nil || len(key) != 32 {
		return errors.Join(ErrCleanupPending, err)
	}
	if state, err := load(ownerDir, key); err == nil {
		if state.Component != "owned-auv" {
			return ErrCleanupPending
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.Join(ErrCleanupPending, err)
	}
	inner := &Adapter{publication: a.publication, profile: dir, directory: ownerDir}
	if err := inner.recoverAttempts(ctx, key); err != nil {
		return err
	}
	return recoverGeneration(ctx, ownerDir, key)
}

func (a *AUVAdapter) recoverPrevious(ctx context.Context, current string) error {
	entries, err := os.ReadDir(a.root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "desktop-") {
			continue
		}
		dir := filepath.Join(a.root, entry.Name())
		if dir == current {
			continue
		}
		_, err := os.Lstat(filepath.Join(dir, ".dsh-auv-owner"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if err := a.RecoverResource(ctx, dir); err != nil {
			return err
		}
	}
	return nil
}

type auvWorker struct{ *Worker }

func (w *auvWorker) PrivateConnectionProcess() (uint32, uint64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.state.Component != "owned-auv" || !w.state.RuntimeReady {
		return 0, 0
	}
	return w.state.Broker.PID, w.state.Broker.Started
}
