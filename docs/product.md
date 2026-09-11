# Product

## Purpose

dsh-work provides the trusted desktop lifecycle around DeepSeek Harness. It
makes a versioned external DSH runtime startable, observable and recoverable
without turning the Host into another agent runtime.

## Ownership boundary

dsh-work owns:

- application windows, tray behavior and explicit Quit;
- trusted settings and native desktop integrations;
- the installed-runtime catalog and selected Run context;
- Worker process ownership, readiness, restart and cleanup;
- the trusted gateway between the Workspace WebView and DSH;
- desktop-notification preferences, routing and delivery.
- the Desktop Pet catalog, trusted Pet preferences and the native Pet surface.

DSH owns:

- agents, prompts, models and conversations;
- profiles and their plugin associations;
- Workspace identity, directories and sessions;
- its Web UI, contextual notices and appearance preference.

The Run context contains one DSH runtime, Node selection, DSH data directory and profile.
Workspace context is selected separately through DSH and is scoped to a Worker
generation. dsh-work must not infer a Workspace from its current directory or
silently mutate DSH-owned data.

## Current capabilities

- The Host starts one exact-version local DSH runtime on loopback and exposes a
  trusted Workspace session only after readiness and gateway validation.
- Runtime, DSH data-directory and profile switches are serialized. A candidate
  becomes current only when ready. Failure follows the automatic-recovery or
  user-choice preference; recovery requires an available version snapshot.
- Current-profile plugin associations can be inspected and changed through DSH
  commands. Non-current profiles are read-only.
- Successful normal startup records the last successful version snapshot.
  Settings Overview exposes the snapshot list and manual save, rename, version
  preview, restore and delete actions.
- Settings General offers automatic recovery of the last successful snapshot or
  user choice. The failed-startup surface offers retry, snapshot recovery and
  safe mode. In Settings Overview, “启动安全模式” sits beside “切换环境”.
- Snapshot recovery reinstalls exact DSH and plugin versions through the package
  manager, then verifies startup. Safe mode uses a separate clean data directory
  and preserves normal-environment success records.
- Settings persist locale, close-to-tray, recovery and notification preferences.
- Workspace and Settings windows separately remember normal dimensions and
  maximised state. Minimisation does not replace the saved normal dimensions;
  a missing or invalid size uses the window's defaults.
- The default close policy keeps the application available from the tray.
  Completed, interaction-required and error notifications are enabled by
  default; routine lifecycle notifications are disabled.
- Routine completion delivery is suppressed while the Workspace is active.
  Notification preferences do not hide DSH-owned in-page notices.
- DSH owns the appearance preference; trusted Host surfaces consume it without
  persisting a second theme setting.
- The Host can display a selected Desktop Pet in a transparent, always-on-top
  surface outside the Workspace window. Pets Settings exposes discovery,
  preview, visibility and direct 50%–300% size control.
- Closing the last window follows the close preference. Explicit Quit owns
  cancellation and managed Worker cleanup.
- Windows uses a Job Object to own the Worker process tree.

## Current limitations

- Normal startup requires a compatible runtime already registered locally.
  Snapshot recovery may download recorded packages.
- Recoverable plugin snapshots currently target registry dependencies managed
  by the supported DSH pnpm profile workflow. Local/Git sources, auxiliary
  dependency files and npm-only profiles without a pnpm lock are not covered by
  automatic historical reconstruction. Node must match the recorded version.
  See [Version recovery](version-recovery.md) for the exact limits.
- Native process-supervision and packaging parity for macOS and Linux are not
  implemented.
- The Pet task-event producer is not present in this repository, so task-driven
  reactions are not end-to-end complete.
- Native platform, packaging, visual and accessibility evidence for the Pet has
  not yet been collected beyond automated checks; Windows is the reference
  implementation path.
