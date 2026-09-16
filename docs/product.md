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
- the authenticated IPC channel between the Workspace WebView and DSH;
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

- The Host starts one exact-version DSH runtime through an authenticated local
  channel and exposes the Workspace only after readiness validation.
- Runtime, DSH data-directory and profile switches are serialized. A candidate
  becomes current only when ready. Failure follows the automatic-recovery or
  user-choice preference; recovery requires an available version snapshot.
- Current-profile plugin associations can be inspected and changed through DSH
  commands. Non-current profiles are read-only.
- Successful normal startup records the last successful version snapshot.
  Settings Overview presents a compact record summary and save action, with
  history and selected-record management in a dedicated dialog.
- Settings General offers automatic recovery of the last successful snapshot or
  user choice. The failed-startup surface offers retry, snapshot recovery and
  safe mode. In Settings Overview, “启动安全模式” sits beside “切换环境”.
- Snapshot recovery reinstalls exact DSH and plugin versions through the package
  manager, then verifies startup. Safe mode uses a separate clean data directory
  and preserves normal-environment success records.
- Settings persist locale, recovery, Pet and notification preferences.
- Workspace and Settings windows separately remember normal dimensions and
  maximised state. Minimisation does not replace the saved normal dimensions;
  a missing or invalid size uses the window's defaults.
- A per-user daemon owns the Worker, tray, Pet and notifications independently
  of the desktop UI process. The tray can open or reconnect the UI.
  Completed, interaction-required and error notifications are enabled by
  default; routine lifecycle notifications are disabled.
- Routine completion delivery is suppressed while the Workspace is active.
  Notification preferences do not hide DSH-owned in-page notices.
- DSH owns the appearance preference; trusted Host surfaces consume it without
  persisting a second theme setting.
- The Host can display a selected Desktop Pet in a transparent, always-on-top
  surface outside the Workspace window. Pets Settings exposes discovery,
  preview, visibility and direct 50%–300% size control.
- Closing a desktop window hides it and retains its WebView for the next open;
  closing or crashing the UI client leaves background tasks running. “停止后台并退出”
  explicitly stops the Worker and its children, releases locks and exits the tray
  and remaining UI.
- The manager CLI shares the daemon for online operations and uses locked
  offline access when the daemon is unavailable.
- Windows uses a Job Object to own the Worker process tree.

See [Settings and startup](settings.md) for the accepted interaction layout.

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
- DSH activity integration supplies Pet reactions and conversation navigation.
  Overlay input, multi-monitor DPI, OS stacking and complete accessibility
  acceptance remain open.
- The Windows per-user installer is available through the local Wails/NSIS
  packaging task and the CI artifact workflow. Application self-update,
  release signing and update-feed credentials remain unconfigured placeholders.
- Mobile clients and remote access are deferred. The daemon and desktop UI run
  as separate invocations of the same executable; the local IPC endpoint is not
  a remote access service. Login startup and suspend/logoff behavior still need
  qualification.
