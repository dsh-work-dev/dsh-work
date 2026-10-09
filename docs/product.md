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
- the appearance (theme and light/dark mode) of its own startup and Settings
  windows.

DSH owns:

- agents, prompts, models and conversations;
- profiles and their plugin associations;
- Workspace identity, directories and sessions;
- its Web UI, contextual notices and the appearance of DSH content.

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
- Current-profile plugins can be inspected, installed, upgraded and removed.
  While the profile is running, third-party plugins and individual official
  loader entries can be switched on and off. DSH's own plugin manager saves and
  applies the change. A disabled plugin stays installed. DSH distribution
  packages cannot be disabled as a whole. Non-current profiles are read-only.
- Plugin update availability comes from a registry check that runs whenever
  the current profile is running. A failed check is retried with backoff (2 s
  doubling to 60 s). Plugins dsh-work installed show their recorded source
  (official, mirror, custom registry) before the check completes.
- Upgrade all applies every available plugin upgrade in one Run-context
  change: one Worker stop, ordered upgrades, one health check, and rollback of
  the whole batch on failure.
- Successful normal startup records the last successful version snapshot.
  Settings Overview presents a compact record summary and save action, with
  history and selected-record management in a dedicated dialog.
- Settings General offers automatic recovery of the last successful snapshot or
  user choice. The failed-startup surface offers retry, snapshot recovery and
  safe mode. When the failure output names installed third-party plugins, it
  also offers to disable or remove each one and then starts again; a failure
  caused by damaged DSH session data names no plugin. In Settings Overview,
  “启动安全模式” sits beside “切换环境”.
- Snapshot recovery reinstalls exact DSH and plugin versions through the package
  manager, then verifies startup. Safe mode uses a separate clean data directory
  and preserves normal-environment success records. Once a normal environment
  is running again, that data directory is discarded and no longer selectable.
- Removing a DSH runtime deletes its installed files. A runtime used by the
  configured, current, known-good or safe-mode return environment cannot be
  removed.
- DSH's DeepSeek account sign-in works in the Workspace. Starting sign-in
  opens the authorization page in the system browser. After authorization,
  the browser shows a result page with a link back to dsh-work.
- dsh-work handles `dsh://open` and its own `dsh-work://open` links by
  focusing or opening the Workspace. The installer takes over the `dsh://`
  scheme and restores the previous handler on uninstall.
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
- dsh-work persists the appearance of its own windows (General → Theme and
  Appearance). A theme offers only the light/dark modes it supports; choosing
  a theme without the current mode switches to one it supports. It does not change DSH's appearance, and DSH
  content windows keep DSH's own light/dark preference.
- The Host can display a selected Desktop Pet in a transparent, always-on-top
  surface outside the Workspace window. Pets Settings exposes discovery,
  preview, visibility and direct 50%–300% size control.
- Closing a desktop window hides it and retains its WebView for the next open;
  closing or crashing the UI client leaves background tasks running. 文件 → 退出
  in the workbench menu (“停止后台并退出” in the tray) explicitly stops the Worker
  and its children, releases locks and exits the tray and remaining UI.
- The workbench is a single window without a system title bar. DSH runs inside
  it below a dsh-work top bar that follows DSH's surface colour and holds the
  menus 文件 (new session, workspace, terminal and browser; close, quit), 视图
  (sidebars, zoom, full screen, desktop pet), 运行 (refresh, restart DSH, safe
  mode), 设置 (each Settings section and DSH settings) and 帮助 (docs, keyboard
  shortcuts, feedback, diagnostics, updates, about); DSH commands show their
  current DSH shortcuts. The bar ends with native-behaving minimise, maximise
  (with Windows 11 Snap layouts) and close buttons. Alt or F10 focuses the menus, also while typing in DSH.
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
  packaging task and the CI artifact workflow. About can check for, download
  and install a signed update through the daemon, but the flow stays inactive
  until an update feed and public key are configured
  (`DSH_WORK_UPDATE_FEED_URL`, `DSH_WORK_UPDATE_PUBLIC_KEY` or
  `DSH_WORK_UPDATE_PUBLIC_KEY_FILE`). No release feed, signing identity or
  public key is configured yet.
- Mobile clients and remote access are deferred. The daemon and desktop UI run
  as separate invocations of the same executable; the local IPC endpoint is not
  a remote access service. Login startup and suspend/logoff behavior still need
  qualification.
