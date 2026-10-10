package app

import (
	"context"
	"errors"
	"slices"

	"github.com/local/dsh-work/internal/dshadapter"
	"github.com/local/dsh-work/internal/dshmanager"
	"github.com/local/dsh-work/internal/lifecycle"
)

// maxSuspectedPlugins bounds how many plugins one failure's output can mark.
const maxSuspectedPlugins = 3

// failedPluginStart remembers the third-party plugins of the profile one
// failed start used, together with that launch. The startup surface can then
// disable or remove them while no Worker is running.
type failedPluginStart struct {
	generation string
	launch     dshmanager.ResolvedLaunch
	plugins    []lifecycle.FaultPlugin
}

// PluginFaultManager is the manager surface for disabling or removing a plugin
// from a profile that failed to start. Mutations refuse while a Run context is current.
type PluginFaultManager interface {
	ProfilePlugins(context.Context, dshmanager.ResolvedLaunch) ([]dshmanager.ProfilePlugin, error)
	ApplyPlugin(context.Context, dshmanager.ResolvedLaunch, string, string) (dshmanager.PluginResult, error)
	DisableFaultPlugin(context.Context, dshmanager.ResolvedLaunch, string) error
}

// recordPluginFault offers the failed profile's third-party plugins, the ones
// the output names first. Output in which DSH rejects its own stored data
// offers none, because disabling a plugin would not help.
func (h *Host) recordPluginFault(run *generationRun, output string) {
	h.pluginFault.Store(nil)
	run.mu.RLock()
	launch := cloneResolvedLaunch(run.launch)
	run.mu.RUnlock()
	if launch == nil || dshadapter.StoredDataRejected(output) {
		return
	}
	manager, ok := h.deps.Manager.(PluginFaultManager)
	if !ok {
		return
	}
	installed, err := manager.ProfilePlugins(context.Background(), *launch)
	if err != nil || len(installed) == 0 {
		return
	}
	plugins := make([]lifecycle.FaultPlugin, 0, len(installed))
	for _, candidate := range dshadapter.PluginFailureCandidates(output) {
		index := slices.IndexFunc(installed, func(plugin dshmanager.ProfilePlugin) bool { return plugin.Package == candidate })
		if index < 0 {
			continue
		}
		plugins = append(plugins, lifecycle.FaultPlugin{Package: candidate, Enabled: installed[index].Enabled, Suspected: true})
		if len(plugins) == maxSuspectedPlugins {
			break
		}
	}
	for _, plugin := range installed {
		if !slices.ContainsFunc(plugins, func(listed lifecycle.FaultPlugin) bool { return listed.Package == plugin.Package }) {
			plugins = append(plugins, lifecycle.FaultPlugin{Package: plugin.Package, Enabled: plugin.Enabled})
		}
	}
	h.pluginFault.Store(&failedPluginStart{generation: run.generation, launch: *launch, plugins: plugins})
}

// withPluginFault projects the plugins onto the failure they belong to; a
// later generation or a non-failed state never carries them.
func (h *Host) withPluginFault(status lifecycle.Status) lifecycle.Status {
	record := h.pluginFault.Load()
	if record == nil || len(record.plugins) == 0 || status.State != lifecycle.StateFailed || status.GenerationID != record.generation {
		status.PluginFault = nil
		return status
	}
	status.PluginFault = &lifecycle.PluginFault{Plugins: slices.Clone(record.plugins)}
	return status
}

// RemoveFaultPlugin uninstalls one plugin of the failed profile.
func (h *Host) RemoveFaultPlugin(ctx context.Context, packageName string) (lifecycle.Status, error) {
	return h.actOnFaultPlugins(func(manager PluginFaultManager, record *failedPluginStart) ([]lifecycle.FaultPlugin, error) {
		if !slices.ContainsFunc(record.plugins, func(plugin lifecycle.FaultPlugin) bool { return plugin.Package == packageName }) {
			return nil, errFaultPluginGone
		}
		if _, err := manager.ApplyPlugin(ctx, record.launch, packageName, "remove"); err != nil {
			return nil, err
		}
		return slices.DeleteFunc(slices.Clone(record.plugins), func(plugin lifecycle.FaultPlugin) bool { return plugin.Package == packageName }), nil
	})
}

// DisableFaultPlugin disables one plugin of the failed profile while the
// failed Worker has no live PluginManager connection.
func (h *Host) DisableFaultPlugin(ctx context.Context, packageName string) (lifecycle.Status, error) {
	return h.actOnFaultPlugins(func(manager PluginFaultManager, record *failedPluginStart) ([]lifecycle.FaultPlugin, error) {
		plugins := slices.Clone(record.plugins)
		index := slices.IndexFunc(plugins, func(plugin lifecycle.FaultPlugin) bool { return plugin.Package == packageName })
		if index < 0 || !plugins[index].Enabled {
			return nil, errFaultPluginGone
		}
		if err := manager.DisableFaultPlugin(ctx, record.launch, packageName); err != nil {
			return nil, err
		}
		plugins[index].Enabled = false
		return plugins, nil
	})
}

// DisableFaultPlugins disables every enabled plugin of the failed profile, so
// the next start runs without third-party plugins. A failure keeps the plugins
// already disabled and reports which state the list is in.
func (h *Host) DisableFaultPlugins(ctx context.Context) (lifecycle.Status, error) {
	return h.actOnFaultPlugins(func(manager PluginFaultManager, record *failedPluginStart) ([]lifecycle.FaultPlugin, error) {
		plugins := slices.Clone(record.plugins)
		for i := range plugins {
			if !plugins[i].Enabled {
				continue
			}
			if err := manager.DisableFaultPlugin(ctx, record.launch, plugins[i].Package); err != nil {
				return plugins, err
			}
			plugins[i].Enabled = false
		}
		return plugins, nil
	})
}

var errFaultPluginGone = errors.New("the plugin is not an enabled plugin of the failed profile")

// actOnFaultPlugins changes the failed profile only while that failure is still
// the Host's state, so no Worker can be reading the profile. act returns the
// plugin list after its change, also when it stops partway.
func (h *Host) actOnFaultPlugins(act func(PluginFaultManager, *failedPluginStart) ([]lifecycle.FaultPlugin, error)) (lifecycle.Status, error) {
	h.switchMu.Lock()
	defer h.switchMu.Unlock()
	if h.isShutdownRequested() || h.isSwitching() {
		return h.Status(), h.failureFor(errors.New("dsh-work is changing its Run context"), lifecycle.ErrorManagerOperationBusy, "dsh-work is finishing the current Run context.", true)
	}
	status := h.Status()
	record := h.pluginFault.Load()
	if status.PluginFault == nil || record == nil {
		return status, h.failureFor(errors.New("no startup failure offers plugin changes"), lifecycle.ErrorPluginSpecInvalid, "This plugin is no longer part of the startup failure.", false)
	}
	manager, ok := h.deps.Manager.(PluginFaultManager)
	if !ok {
		return status, h.failureFor(errors.New("plugin fault manager is unavailable"), lifecycle.ErrorPluginCommandUnavailable, "Plugins cannot be changed here.", false)
	}
	plugins, err := act(manager, record)
	if plugins != nil {
		updated := *record
		updated.plugins = plugins
		h.pluginFault.CompareAndSwap(record, &updated)
	}
	if errors.Is(err, errFaultPluginGone) {
		return h.Status(), h.failureFor(err, lifecycle.ErrorPluginSpecInvalid, "This plugin is no longer part of the startup failure.", false)
	}
	if err != nil {
		return h.Status(), h.failureFor(err, lifecycle.ErrorPluginCommandFailed, "The plugin could not be changed.", true)
	}
	return h.Status(), nil
}
