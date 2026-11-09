# Linux supervised launch candidate

`SystemdAdapter` implements the existing Supervisor contract. It is not yet
selected by the product's Linux dependency factory; actual facility tests must
pass before the CEF/desktop integration is admitted.

The maintained `coreos/go-systemd/v22` client owns transient-unit creation,
state and stop/kill requests; the existing `godbus/dbus/v5` dependency carries
native FD properties. This avoids implementing D-Bus, a service manager, or a
process-tree reaper. POSIX process groups cannot own a descendant that calls
`setsid`, and root-process exit cannot establish tree-empty.

The unit contains a fixed system `cat` holding a private EOF lease. The actual
application is launched with standard Go `os/exec`, `UseCgroupFD`, and atomic
`PidFD`: containment is installed by `clone3` before child code executes, and
the launch keeps the exact `LaunchPlan.Env`. Direct systemd `ExecStart` would
implicitly inherit the user manager's environment; credentials must not be
reintroduced after the caller filtered them.

Normal CEF protocol shutdown happens before `WaitEmpty`. This method waits for
the application and all descendants to exit naturally, then releases only the
anchor's EOF lease and observes the kernel cgroup-empty barrier. Explicit
`RequestStop`/`ForceStop` address this unit only. Failed cleanup retains the
worker or the adapter's failed-start owner for retry. Daemon death closes the
EOF lease and lets systemd reclaim the unit's whole cgroup.

This candidate requires a real systemd user manager, a delegated cgroup-v2 unit
and kernel support for atomic cgroup/pidfd launch. Unsupported facilities are
reported as unavailable; they do not narrow the project's Linux support goal.
Other Linux resource-owner implementations remain a separate coverage gap.

Run on Linux without replacing the current ordinary-terminal environment:

```sh
DSH_WORK_LINUX_SUPERVISOR_TEST=1 CGO_ENABLED=0 \
  go test -count=1 -v -timeout=60s ./internal/platform/linux \
  -run 'TestLiveSystemdAtomicCgroupOwner|TestLiveSystemdFailedLaunchCleanup'
```

The tests execute the actual Adapter and self-owned Go helper processes. They
check the exact launch environment, immutable start ticks/pidfd, root exit with
a live independent-session descendant, non-destructive natural waiting, forced
tree cleanup, and failed-launch unit cleanup. A Windows cross-compile is only a
compile check, not evidence for these Linux lifecycle guarantees.
