package app

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/local/dsh-work/internal/dshadapter"
	"github.com/local/dsh-work/internal/dshmanager"
	"github.com/local/dsh-work/internal/lifecycle"
)

// maxFaultPlugins bounds how many suspects the startup surface offers.
const maxFaultPlugins = 3

// failedPluginStart remembers which installed plugins one failed start's output
// pointed at, together with the launch that failed. The startup surface can
// then disable or remove one of them while no Worker is running.
type failedPluginStart struct {
	generation string
	launch     dshmanager.ResolvedLaunch
	plugins    []string
}

// PluginFaultManager is the manager surface for acting on a profile that
// failed to start. Both mutations refuse while a Run context is current.
type PluginFaultManager interface {
	ProfilePluginPackages(context.Context, dshmanager.ResolvedLaunch) ([]string, error)
	ApplyPlugin(context.Context, dshmanager.ResolvedLaunch, string, string) (dshmanager.PluginResult, error)
	ApplyPluginDisabled(context.Context, dshmanager.ResolvedLaunch, string, bool) (dshmanager.PluginResult, error)
}

// pluginDisableEnforcer re-applies recorded plugin disables before a launch,
// because DSH's own plugin commands re-add every installed plugin layer.
type pluginDisableEnforcer interface {
	EnforcePluginDisables(context.Context, dshmanager.ResolvedLaunch) error
}

// recordPluginFault attributes one early exit to installed third-party
// plugins. Output in which DSH rejects its own stored data names no plugin,
// because disabling the plugin that surfaced it would not help.
func (h *Host) recordPluginFault(run *generationRun, output string) {
	h.pluginFault.Store(nil)
	run.mu.RLock()
	launch := cloneResolvedLaunch(run.launch)
	run.mu.RUnlock()
	if launch == nil || strings.TrimSpace(output) == "" || dshadapter.StoredDataRejected(output) {
		return
	}
	manager, ok := h.deps.Manager.(PluginFaultManager)
	if !ok {
		return
	}
	installed, err := manager.ProfilePluginPackages(context.Background(), *launch)
	if err != nil || len(installed) == 0 {
		return
	}
	var plugins []string
	for _, candidate := range dshadapter.PluginFailureCandidates(output) {
		if slices.Contains(installed, candidate) {
			plugins = append(plugins, candidate)
		}
		if len(plugins) == maxFaultPlugins {
			break
		}
	}
	if len(plugins) == 0 {
		return
	}
	h.pluginFault.Store(&failedPluginStart{generation: run.generation, launch: *launch, plugins: plugins})
}

// withPluginFault projects the suspects onto the failure they explain; a later
// generation or a non-failed state never carries them.
func (h *Host) withPluginFault(status lifecycle.Status) lifecycle.Status {
	record := h.pluginFault.Load()
	if record == nil || len(record.plugins) == 0 || status.State != lifecycle.StateFailed || status.GenerationID != record.generation {
		status.PluginFault = nil
		return status
	}
	status.PluginFault = &lifecycle.PluginFault{Plugins: slices.Clone(record.plugins)}
	return status
}

// DisableFaultPlugin disables one plugin the last failed start pointed at.
func (h *Host) DisableFaultPlugin(ctx context.Context, packageName string) (lifecycle.Status, error) {
	return h.actOnFaultPlugin(ctx, packageName, func(manager PluginFaultManager, launch dshmanager.ResolvedLaunch) error {
		_, err := manager.ApplyPluginDisabled(ctx, launch, packageName, true)
		return err
	})
}

// RemoveFaultPlugin uninstalls one plugin the last failed start pointed at.
func (h *Host) RemoveFaultPlugin(ctx context.Context, packageName string) (lifecycle.Status, error) {
	return h.actOnFaultPlugin(ctx, packageName, func(manager PluginFaultManager, launch dshmanager.ResolvedLaunch) error {
		_, err := manager.ApplyPlugin(ctx, launch, packageName, "remove")
		return err
	})
}

// actOnFaultPlugin changes the failed profile only while that failure is still
// the Host's state, so no Worker can be reading the profile.
func (h *Host) actOnFaultPlugin(ctx context.Context, packageName string, act func(PluginFaultManager, dshmanager.ResolvedLaunch) error) (lifecycle.Status, error) {
	h.switchMu.Lock()
	defer h.switchMu.Unlock()
	if h.isShutdownRequested() || h.isSwitching() {
		return h.Status(), h.failureFor(errors.New("dsh-work is changing its Run context"), lifecycle.ErrorManagerOperationBusy, "dsh-work is finishing the current Run context.", true)
	}
	status := h.Status()
	record := h.pluginFault.Load()
	if status.PluginFault == nil || record == nil || !slices.Contains(record.plugins, packageName) {
		return status, h.failureFor(errors.New("the plugin is not named by the current startup failure"), lifecycle.ErrorPluginSpecInvalid, "This plugin is no longer part of the startup failure.", false)
	}
	manager, ok := h.deps.Manager.(PluginFaultManager)
	if !ok {
		return status, h.failureFor(errors.New("plugin fault manager is unavailable"), lifecycle.ErrorPluginCommandUnavailable, "Plugins cannot be changed here.", false)
	}
	if err := act(manager, record.launch); err != nil {
		return h.Status(), h.failureFor(err, lifecycle.ErrorPluginCommandFailed, "The plugin could not be changed.", true)
	}
	// The plugin is handled; the next attempt shows whatever fails next.
	remaining := slices.DeleteFunc(slices.Clone(record.plugins), func(name string) bool { return name == packageName })
	updated := *record
	updated.plugins = remaining
	h.pluginFault.CompareAndSwap(record, &updated)
	return h.Status(), nil
}
