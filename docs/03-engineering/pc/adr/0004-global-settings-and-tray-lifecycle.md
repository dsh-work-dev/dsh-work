# ADR-0004: Global settings and tray-aware window lifecycle

- Status: Accepted for the initial Settings and nested DSH manager slice
- Date: 2026-09-02

## Context

Work has more than one native window: the external DSH Workspace and the
trusted Settings window. The DSH manager lives inside Settings. Closing one
window must not accidentally stop the DSH Worker,
but leaving every window closed must have an explicit, user-configurable
meaning. The default should preserve the tray workflow; users who do not want
background Work must be able to make closing the last window a full quit.

The preference is Work-global. It is not a DSH runtime, DSH home, profile or
plugin property, and it must remain available from the separate Settings
window. Explicit `Quit DSH Work` must always perform managed Worker cleanup,
regardless of the close preference.

Settings persistence also needs to publish a complete JSON document. The
standard library's `os.Rename` does not replace an existing file on Windows,
while the commonly considered `google/renameio` package explicitly does not
export its atomic writer on Windows. A platform-specific replace primitive is
therefore required for the Windows slice.

## Decision

1. Add a versioned `internal/settings` Module with a small `Values` contract.
   The first setting is `closeToTray`, defaulting to `true` when no document is
   present. Invalid or unreadable persisted state is reported as a stable
   Settings error and the composition root uses the tray-safe default.
2. Persist settings under the user configuration directory using a temporary
   file, bounded JSON, restrictive file permissions and a completed-file
   replacement. The shared `settings.FileReplacer` interface contains no
   operating-system types.
3. Use the native Windows `MoveFileEx` replace/write-through operation inside
   `internal/platform/windows` through the already-pinned `golang.org/x/sys/windows`
   dependency. On POSIX targets, the file store uses the native rename
   operation. No fake macOS or Linux production adapter is introduced.
4. Keep window semantics in the platform-neutral `lifecycle.WindowLedger`:

   - a close request marks only the requested window hidden;
   - a non-last window close is always realized as hide and event cancel;
   - a last-window close returns `tray` when `closeToTray` is enabled;
   - a last-window close returns `quit` when it is disabled;
   - `BeginQuit` makes all subsequent close events pass through so Wails can
     destroy windows during the real application shutdown.

5. Use Wails' native `SystemTray` API at the composition edge for the tray
   icon and menu on supported desktop platforms. The tray offers Workspace,
   Settings, Restart DSH and Quit DSH Work. The application menu contains only
   Settings and Help; Help contains update-check and About commands.
6. Expose Work Settings and nested DSH manager controls only through the
   trusted Settings window. Its Overview is read-only and its single General
   page contains launch target and close policy. Saving the close preference
   updates the shared ledger immediately; it does not restart DSH or mutate DSH
   data.
7. Realize explicit quit as a pre-exit `QuitFlow`: mark the window ledger as
   quitting, hide every existing Work window synchronously, run bounded Host
   cleanup in the background, and call the native application quit only after
   cleanup succeeds. A cleanup failure clears the quit gate, leaves the tray
   available and opens the trusted Settings overview for retry. The Wails
   `OnShutdown` hook still marks the ledger before its final fallback cleanup so
   native window destruction cannot be mistaken for a tray close.

## Consequences

Positive:

- Closing the last window has a visible, persistent and reversible policy.
- The default tray behaviour keeps the managed DSH session available without
  keeping a window on screen.
- Full quit remains an unambiguous cleanup command.
- A slow or active DSH shutdown no longer leaves a visible WebView looking
  frozen, and cleanup failures remain recoverable without a second Host.
- Shared tests cover the decision without importing Wails or Windows handles.
- Wails owns native tray realization for Windows, macOS and Linux; Work does
  not create placeholder platform implementations.

Costs and risks:

- A corrupted settings document is surfaced as unavailable Settings while the
  application remains tray-safe; a later settings recovery flow is still
  needed.
- Windows replacement can still fail when another process holds the settings
  file open; the user receives a retryable operation error.
- Wails beta lifecycle and tray behavior remain part of the desktop smoke-test
  matrix.

## Rejected alternatives

- **Stop on every window close:** loses the expected tray workflow and can
  terminate DSH while another Work surface is being opened.
- **Put the preference in a DSH profile:** makes a Work application policy
  incorrectly depend on DSH data ownership.
- **Put all lifecycle actions in the application menu:** duplicates the tray
  surface and makes Settings harder to find; lifecycle actions remain in the
  tray while Settings and Help stay top-level application commands.
- **Use `google/renameio` for all platforms:** its own documentation says the
  package cannot provide a correct Windows implementation, so it does not meet
  this release's cross-platform persistence contract.
- **Write a Windows implementation in shared settings code:** leaks native
  details into the shared contract; the replace operation remains in the
  existing Windows Adapter boundary instead.

## Follow-up

Add additional global settings only with a versioned schema migration, an
explicit Settings route and acceptance cases covering default, persisted,
invalid and recovery states. Re-evaluate a community atomic-file dependency if
one gains a maintained Windows implementation that preserves these semantics
without adding a weaker security or lifecycle boundary.
