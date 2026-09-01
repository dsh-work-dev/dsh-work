# Three-platform process supervision

## Shared Supervisor contract

Windows, macOS and Linux expose the same lifecycle contract but do not share a pretend-universal process implementation.

```text
Prepare(launch plan) → Start() → WaitReady()
RequestStop() → WaitGracePeriod() → ForceStop() → WaitEmpty()
ObserveExit() / SnapshotDiagnostics() / Close()
```

Shared invariants:

1. One Host generation owns at most one active Worker process tree.
2. A Worker cannot become visible before readiness validation.
3. Events from a prior generation cannot mutate current lifecycle state.
4. Shutdown has bounded graceful and forced phases.
5. `Stopped` means the adapter has verified its managed boundary is empty or reported `PROCESS_CLEANUP_FAILED`.
6. A platform adapter must not report successful startup until its lifecycle boundary is active.

## Windows adapter

### Primitive

Use a Windows Job Object with `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`. Child processes normally inherit Job membership; Work must not enable silent breakaway. Nested-Job behaviour and DSH compatibility are verified on every supported Windows target.

Official reference: [Microsoft Job Objects](https://learn.microsoft.com/en-us/windows/win32/procthread/job-objects).

### Start sequence

1. Create stdout/stderr pipes with only required handles inheritable.
2. Create the Job Object and apply lifecycle limits.
3. Create the Worker suspended with a new process group.
4. Assign the Worker to the Job before any Worker code runs.
5. Start asynchronous output readers and process wait.
6. Resume the initial Worker thread.

If Job assignment fails, terminate the suspended child, close all handles and fail startup. Never resume an unmanaged Worker.

### Stop and Host-loss behaviour

- Normal stop asks the DSH adapter to shut down, waits, then terminates the Job if necessary.
- Forced Host termination closes its Job handle; kill-on-close terminates associated processes.
- The adapter waits on process／Job notifications and closes every process, thread, pipe and Job handle.

### Windows-specific tests

- assignment before resume;
- nested Job compatibility;
- child and grandchild inheritance;
- normal quit, ignored graceful stop and Host force-kill;
- handle-count stability across repeated generations.

## macOS adapter

### Primitive

macOS uses a dedicated process guardian plus a new Worker process group. The guardian is necessary because a process group alone does not automatically terminate when the GUI Host is force-killed.

The guardian receives a one-generation lease pipe from the Host. It creates the Worker group using `posix_spawn` process-group attributes, forwards lifecycle events, waits for the Worker and treats lease EOF as Host loss.

Official references:

- [Apple `posix_spawnattr_setpgroup`](https://developer.apple.com/library/archive/documentation/System/Conceptual/ManPages_iPhoneOS/man3/posix_spawnattr_setpgroup.3.html)
- [Apple `setpgid`](https://developer.apple.com/library/archive/documentation/System/Conceptual/ManPages_iPhoneOS/man2/setpgid.2.html)
- [Apple `waitpid`](https://developer.apple.com/library/archive/documentation/System/Conceptual/ManPages_iPhoneOS/man2/wait.2.html)

### Start sequence

1. Host creates control／lease and output channels.
2. Host starts the signed guardian helper and completes a generation handshake.
3. Guardian creates a new Worker process group before execution.
4. Guardian reports the group and Worker identity; Host verifies them.
5. Output capture and readiness begin only after the guardian confirms ownership.

### Stop and Host-loss behaviour

- Normal stop requests DSH shutdown, then guardian sends `SIGTERM` to the Worker group and waits.
- After the grace boundary, guardian sends `SIGKILL` to the group and reaps direct children.
- Host crash or force-kill closes the lease pipe; guardian performs the same bounded cleanup and exits.
- Work does not claim kernel-enforced containment for a descendant that deliberately creates a new session; the DSH tree must pass escape fixtures.

### macOS-specific tests

- signed／notarised helper launch from an application bundle;
- lease EOF after Host `SIGKILL`;
- process-group TERM／KILL escalation;
- Worker descendants and a deliberate `setsid` escape fixture;
- sleep／wake, logout and application-update interruption;
- file-descriptor and zombie-process stability.

## Linux adapter

### Primitive

Linux uses a dedicated guardian, a new Worker process group, `PR_SET_CHILD_SUBREAPER` and `PR_SET_PDEATHSIG` as layered controls. The guardian owns reaping; the Host lease detects GUI loss. Process group signals provide the baseline cleanup boundary.

Official references:

- [Linux `PR_SET_PDEATHSIG`](https://man7.org/linux/man-pages/man2/PR_SET_PDEATHSIG.2const.html)
- [Linux `PR_SET_CHILD_SUBREAPER`](https://man7.org/linux/man-pages/man2/PR_SET_CHILD_SUBREAPER.2const.html)
- [Linux process groups](https://www.man7.org/linux/man-pages/man2/getpgrp.2.html)

`PR_SET_PDEATHSIG` is defence in depth, not the only cleanup mechanism: its parent is the creating thread, it has fork／credential caveats, and it does not by itself collect a whole tree.

### Start sequence

1. Host creates control／lease and output channels.
2. Guardian starts, marks itself as a child subreaper and completes a generation handshake.
3. Guardian creates the Worker in a new process group and configures parent-death handling where valid.
4. Guardian reports ownership; Host verifies PID, process group and generation before readiness begins.
5. If an available cgroup-v2／systemd user-scope adapter is enabled, the full scope identity is also recorded; baseline correctness cannot require it.

### Stop and Host-loss behaviour

- Normal stop requests DSH shutdown, then escalates `SIGTERM` → bounded wait → `SIGKILL` for the Worker group.
- Guardian reaps reparented descendants as subreaper.
- Host loss closes the lease, triggering cleanup independently of the GUI event loop.
- A supported cgroup adapter may verify and terminate descendants that escape the process group; unsupported environments use the baseline and report any escape as cleanup failure.

### Linux-specific tests

- X11 and Wayland desktop sessions where supported by the release matrix;
- Host `SIGKILL`, guardian failure and parent-death race;
- double-fork, reparenting, ignored TERM and `setsid` escape fixtures;
- cgroup-enabled and cgroup-unavailable environments;
- zombie, file-descriptor and process leak repetition;
- package formats included by the release matrix.

## Shared startup algorithm

1. Lifecycle creates a new generation and cancellable context.
2. DSH adapter constructs a validated launch plan.
3. Platform adapter establishes its managed process boundary.
4. Supervisor starts bounded output capture and waits for platform ownership confirmation.
5. Readiness parser and process waiter race under the same generation.
6. On validated readiness, publish one `WorkerReady` event.
7. On timeout, cancellation or exit, clean the platform boundary and publish one terminal startup event.

Output can propose a readiness endpoint, but an active probe validates it before state changes.

## Shared shutdown algorithm

1. Atomically move lifecycle to `Stopping`; reject new tool requests.
2. Cancel browser tasks and expire queued approvals.
3. Request graceful Worker shutdown through the DSH adapter.
4. Wait for a bounded period while keeping trusted UI responsive.
5. Ask the platform adapter to force-stop its complete boundary.
6. Verify the boundary is empty, close resources and record the outcome.
7. Enter `Stopped`, or surface `PROCESS_CLEANUP_FAILED` with diagnostics.

## Output and restart rules

- stdout and stderr are drained concurrently into bounded buffers.
- Redaction occurs before persistence or UI projection.
- Parser state is generation-scoped and handles partial UTF-8 and line chunks.
- Automatic restart is limited to classified transient failures and a bounded budget.
- Every restart creates a new generation, process boundary, port, IPC credential and readiness probe.
- Raw process output never issues Host commands.

## Shared hostile-child fixtures

Every adapter runs equivalent fixtures for early exit, fragmented readiness, descendants, ignored graceful stop, large output, force-killed Host and resource leaks. Platform-specific escape mechanisms are added to, not substituted for, the shared suite.
