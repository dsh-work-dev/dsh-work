# Version records and recovery

Version records (called snapshots in the internal API) let users return a
profile to a previously working DSH and plugin version set. This document describes when a point is recorded, how to
restore it and the constraints on recovery.

The daemon owns the manager lock, recovery transaction and Worker process.
Settings sends operations to that daemon; closing the UI does not cancel an
accepted recovery. Reopen Settings to inspect its state. Explicit background
shutdown or daemon interruption follows the interruption policy below.

## Recording and visible actions

Normal startup captures dependency inputs before launching the Worker and
compares them again after readiness. Matching inputs establish the automatic
last-successful point for that profile and the last-running recovery pointer.
A newly initialised built-in profile with no third-party plugins can establish
its first point after DSH creates the initial inputs.

Repeated success with the same content refreshes the existing point. Automatic
retention keeps two recent points per profile, plus points required by active
references. Manual points remain until explicitly deleted; points referenced by
success pointers or an unfinished recovery are protected. Manual saving requires
a current verified normal environment whose dependency inputs have not changed.

Settings Overview shows a compact latest-verified summary, record count, save
action and history entry. History uses newest-first rows; selecting a record
opens metadata, plugin versions and restore/rename/delete actions in the same
dialog. Restore confirmation replaces those details. Closing returns focus to
the originating control. “启动安全模式” appears beside “切换环境”.

Startup failure places retry, safe mode, version recovery and visible bounded
diagnostics beside the failing step, including a direct copy action. Version
selection and confirmation use a focused dialog. Recovery shows its current
installation/startup stage and cancellation beside the active operation.

Recording failure is visible but does not stop a healthy Worker or replace the
previous durable success point. Safe-mode startup never advances normal success
pointers. After a failure, safe mode prefers the known-good return environment
when no current Worker exists.

Each safe-mode entry creates a fresh data directory under the app's
`safe-mode` folder; nothing is copied from the failed environment. When a
normal environment becomes healthy again — by returning or by any other
switch — the safe-mode data directory is removed from the catalog and its
folder is deleted. Startup also removes a stale safe-mode entry and any
session folder not owned by an active safe mode. Deletion is best effort: a
locked folder is retried on the next start.

Removing a DSH runtime does not invalidate version records that name its
version, because restore always reinstalls the recorded DSH version.

## Stored contract

The manager's optional `versionRecovery` payload uses schema version 1. Each
point records its ID, kind, label, timestamps, target Run context, exact DSH
version, installed plugin versions/sources, Node and package-manager versions,
platform, content digest and any restoration restriction.

The private payload contains an explicit plugin list (name, installed exact
version, original source specifier and dependency group), ordered DSH bundle
associations, `pnpm-lock.yaml` and required `pnpm-workspace.yaml` inputs. A lock
is retained even for a zero-plugin profile. It does not store an opaque copy of
the package manifest. Restoration reconstructs dependency fields while
preserving current non-dependency settings. Original source specifiers match
the frozen lock; recaptured installed versions and digest verify the result. Installed package trees,
conversation data and Workspace data are not snapshot contents. Inputs containing
recognised credentials are rejected; inputs are validated again before applying.

Snapshot metadata reuses the existing atomic manager file. Individual input
files are limited to 4 MiB, aggregate snapshot inputs to 32 MiB, and the manager
file to 64 MiB on read/write. Manual saving is rejected when the point collection
already contains 128 entries; state validation accepts at most 1,024 entries.
The frontend receives summaries, not private dependency input contents.

## Restore transaction

1. Preview validates the point, content digest, platform, matching Node version,
   available installation adapters and the target data directory/profile.
2. The Host stops the current Worker and verifies cleanup before installation.
3. Persist the recovery stage and force-install the recorded DSH version in its
   managed runtime directory, even if that version is already installed.
4. Through DSH's plugin CLI, ask the package manager to remove current direct
   dependencies; reapply the snapshot inputs and force-install with a frozen
   lock when present. Host code does not delete or move `node_modules`.
5. Recapture installed versions and dependency inputs and compare the digest.
   Start the Worker and commit success only after readiness verification.

The supported pnpm path uses `--force`, `--frozen-lockfile` when a lock is present,
and `--config.optimistic-repeat-install=false`. Package-manager removal before
installation is needed because the supported hoisted linker can otherwise skip
same-version damaged files.

For a runtime published from acquisition staging, pnpm first performs a forced
install at the final location to reconcile its own virtual-store metadata,
then removes and reinstalls the runtime. A pristine profile with neither
plugins nor lock needs no profile installation step. Runtime installation uses
the resolved native npm or pnpm toolchain; recovery does not relocate caches or
stores.

## Failure and interruption policy

The existing `automaticRuntimeRollback` preference retains its stored boolean:
true means automatic recovery, false means user choice. Cold-start failure uses
the selected profile's last-successful point. A failed environment switch uses
the previously successful last-running point. An explicit restore uses the
selected point. An unavailable target leaves recovery to the user.

Automatic fallback is bounded to one attempt. An unfinished running operation
left by process interruption may resume once when automatic recovery is enabled.
A second interruption, a failed/cancelled recovery or user-choice mode requires
user action. An explicit retry starts a fresh retry budget. Safe-mode startup
bypasses unfinished normal recovery.

Successful recovery retains a completed result for display; that result does not
pin an automatic point indefinitely and is cleared when its point is removed.
Persisted success and completed recovery are historical records, not evidence of
a currently live Worker.

## Compatibility and recovery limits

The supported reference environment is Windows with DSH 0.1.7-rc.2 (the
version pinned as `SupportedVersion` and in `toolchain.lock.json`) and its pnpm
profile workflow. Compatibility is validated against
exact DSH versions; this is not a promise that future releases work unchanged.
Registry dependencies with usable lock inputs are supported. Local or
Git sources, advanced manifest dependency configuration (such as overrides or
peer configuration), and auxiliary dependency files are not promised historical
reconstruction. Unsupported records expose a restoration restriction. A profile with plugins but no pnpm lock is marked unavailable before the Worker is stopped. The recorded Node version must match;
this operation does not reinstall Node. Package acquisition can fail when sources
or caches are unavailable, and saved points remain available for a later retry.
