# Linux supervised launch candidate

`SystemdAdapter` implements the existing Supervisor contract. It is not yet
selected by the product's Linux dependency factory. Real lifecycle tests must
pass before the CEF/desktop integration is admitted.

The maintained `coreos/go-systemd/v22` client owns transient-unit creation,
state and stop/kill requests. Existing `godbus/dbus/v5` transfers the native FD.
This avoids implementing D-Bus, a service manager, or a process-tree reaper.
POSIX process groups cannot own a descendant that calls `setsid`.

The user manager launches the same application at a fixed internal bootstrap
entry **inside** the owned unit. No executable, arguments, environment or
credential is passed through bootstrap argv. A parent-created private socket
carries the bounded LaunchPlan and stdout/stderr FDs. Before sending the plan,
the parent checks the manager's MainPID, executable, immutable start ticks,
held pidfd and actual message SCM_CREDENTIALS. The bootstrap checks the creator
peer and same executable. It clears its inherited manager environment, then
uses standard `os/exec` with exactly `LaunchPlan.Env` for the real application.
No caller cgroup migration or special pre-migrated terminal is required.

An atomic child pidfd is transferred through SCM_RIGHTS. The parent derives
the application PID from kernel fdinfo and verifies its creation and unit
membership before acknowledging adoption. Its normal exit result comes from
the trusted bootstrap's actual `Wait`; the bootstrap itself is not reported
as the browser or Worker IPC peer. Unadopted descriptors and partial starts
remain owned until they are closed and the unit's cleanup is confirmed.

The bootstrap remains as the private EOF lease. Normal CEF protocol shutdown
precedes `WaitEmpty`, which waits for the actual application and all descendants
to exit naturally. Once only the bootstrap remains, an explicit finish lets it
exit zero and the kernel cgroup population becomes empty. EOF/invalid finish
is abnormal owner loss: the bootstrap terminates its root, exits abnormally,
and systemd reclaims all remaining processes. Explicit `RequestStop`/`ForceStop`
address this unit only; failures retain a retryable owner. No unit-killed process
is presented as a clean CEF protocol shutdown.

The candidate requires a real user manager, cgroup v2 and process-instance
facilities. Unsupported facilities report unavailable; they do not narrow the
project's Linux support goal. Other Linux resource-owner implementations remain
a coverage gap.

```sh
DSH_WORK_LINUX_SUPERVISOR_TEST=1 CGO_ENABLED=0 \
  go test -count=1 -v -timeout=60s ./internal/platform/linux \
  -run 'TestLiveSystemdAtomicCgroupOwner|TestLiveSystemdFailedLaunchCleanup|TestLiveSystemdCancelBeforeAdoption'
```

Tests use the actual Adapter, self-owned application processes, and an
independent-session descendant. They cover natural/forced cleanup, exact Env,
kernel instance handoff, ENOENT and cancellation after a real pidfd arrives but
before adoption. A Windows cross-compile is only a compile check.
