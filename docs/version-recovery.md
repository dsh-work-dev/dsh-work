# Version recovery

Accepted 2026-09-11 from the verified implementation in `5948d41` and the
Settings layout/window follow-up in `bb85f9c`.

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

Settings Overview always exposes version snapshots and manual save, rename,
version details, restore preview and delete actions. “启动安全模式” appears beside
“切换环境”; the former explanatory row and its separators are removed. Startup
failure exposes snapshot recovery, retry and safe mode. Recovery displays its
installation/startup stage and offers cancellation.

Recording failure is visible but does not stop a healthy Worker or replace the
previous durable success point. Safe-mode startup never advances normal success
pointers. After a failure, safe mode prefers the known-good return environment
when no current Worker exists.

## Stored contract

The manager's optional `versionRecovery` payload uses schema version 1. Each
point records its ID, kind, label, timestamps, target Run context, exact DSH
version, installed plugin versions/sources, Node and package-manager versions,
platform, content digest and any restoration restriction.

The private payload contains dependency-related manifest fields, DSH bundle
associations, pnpm lock and workspace inputs. User configuration outside these
dependency fields is preserved during restoration. Installed package trees,
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
installation is needed because the tested hoisted linker can otherwise skip
same-version damaged files. Runtime installation uses the resolved native npm
or pnpm toolchain; recovery does not relocate caches or stores.

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

## Supported boundary and evidence

The production recovery path is Windows with DSH 0.1.2-alpha.3's pnpm profile
workflow. Registry dependencies with usable lock inputs are supported. Local or
Git sources and auxiliary dependency configuration files are not promised
historical reconstruction. An npm-only profile without a pnpm lock is marked
unavailable before the Worker is stopped. The recorded Node version must match;
this operation does not reinstall Node. Package acquisition can fail when sources
or caches are unavailable, and saved points remain available for a later retry.

Windows integration tests exercised real DSH forced installation, plugin version
and set restoration, same-version file repair, preservation of a `node_modules`
sentinel and nondependency profile fields, normal startup and safe-mode isolation.
Host/manager regressions cover failure policy, cross-profile recovery targets,
recording failure, persistence reload and interrupted retry limits. The window
follow-up covers resize/maximise/minimise/close observations and settings reload;
its UI layout was checked in a browser fixture. Automated native-window event
coverage does not constitute a manual desktop resize/relaunch test.
