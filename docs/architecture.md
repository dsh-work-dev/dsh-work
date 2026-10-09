# Architecture

## System context

```text
User
  |
  v
Desktop UI client (Go + Wails)
  |-- workbench window: trusted shell (top bar, menus, startup)
  |     `-- DSH frame on its own origin --> Wails HTTP handler or bounded byte streams
  |-- trusted Settings window
  `-- current-user local IPC --------------------+
Manager CLI --> current-user local IPC ----------|
                                                v
Per-user daemon (Go + Wails, same executable)
  |-- Host and runtime manager --> Supervisor --> DSH Worker
  |-- Worker forwarding --> authenticated generation pipe --> DSH routes
  |-- tray and desktop notifications
  `-- Pet catalog/runtime/renderer --> native Pet overlay
```

The daemon owns Host authority, manager state and Worker lifetime. The UI owns
its windows and projects daemon state. A UI process crash therefore leaves the
Worker and its tasks running. The DSH Worker and rendered Workspace content
cannot grant themselves Host capabilities.

## Implemented modules

| Module | Responsibility |
|---|---|
| `main.go` | embedded resources and desktop entrypoint |
| `internal/desktopapp` | daemon/client process assembly, window/menu lifetime and typed event replay |
| `internal/daemon` | current-user IPC, control dispatch, snapshots and Worker forwarding |
| `internal/desktopclient` | typed UI service proxies to the daemon |
| `internal/app` | Host commands, manager operations and process locks |
| `internal/lifecycle` | window policy, quit flow and lifecycle value types |
| `internal/supervisor` | platform-neutral Worker process contract |
| `internal/platform` | production platform selection and native adapters |
| `internal/platform/windows` | process creation, Job Object cleanup, atomic file replacement and explicit runtime installation |
| `internal/dshadapter` | exact-version launch, readiness, profile and plugin command grammar, startup-failure plugin attribution |
| `internal/dshmanager` | runtime catalog, profiles, plugin commands, startup-failure bundle deselection, version snapshots and serialized recovery state |
| `internal/workspacecontext` | per-generation DSH Workspace context |
| `internal/workerchannel` | per-generation channel, authentication cookies and activity lifetime |
| `internal/workeripc` | current-user authenticated OS pipe carrying upstream HTTP bytes |
| `internal/desktopbridge` | resource delivery, Fetch and WebSocket over bounded Wails byte streams, desktop renderer marker and external-link opening |
| `internal/accountcallback` | daemon-owned loopback OAuth callback listener, callback-origin rewrite and result pages |
| `internal/dshactivity` | per-launch DSH plugin for activity events, conversation navigation and account-browser opening |
| `internal/settings` | versioned Host preferences and persistence contract |
| `internal/notifications` | notification vocabulary, preference evaluation, routing and bounded deduplication |
| `internal/nativeui` | native menus, notifications and window-geometry persistence wiring |
| `internal/pet` | data-only Pet catalog adapters, bounded cache, runtime, renderer and overlay projection |
| `frontend` | trusted Host state projection and interaction |

## Dependency direction

Lifecycle and policy modules depend on values and narrow interfaces. Wails, DSH,
filesystem and operating-system primitives implement those interfaces at the
edge. The frontend uses the generated allowlisted Host bindings and has no
direct process or filesystem authority. Logs and UI projections observe state;
they do not define lifecycle truth.

## Desktop communication

Opening the application connects to the existing daemon or starts it with
`--daemon`. The UI singleton has a separate IPC endpoint: subsequent opens focus
the existing client, while reopening after UI exit creates a fresh client of
the same daemon. Both endpoints use go-winio with a current-user SID ACL and
remote-client rejection. The daemon protocol marker is checked before use.
The online CLI uses the same control endpoint; offline access still requires
the manager lock. These local endpoints open no TCP or UDP listener. The only
loopback listener is the account sign-in callback described under
[Account sign-in](#account-sign-in).

The UI binds generated `internal/desktopclient` services. Native window identity
selects the allowed role before forwarding management calls. Snapshot polling
projects lifecycle, preferences and bounded events; event replay decodes the
registered Go payload type before emitting to Wails. A lost or replaced daemon
ends the connected UI client. The daemon remains the source of lifecycle truth.

The Host creates a fresh authenticated named pipe before each Windows Worker
launch. `go-winio` enforces a current-user SID ACL and rejects remote pipe
clients. Per-launch random credentials authenticate both ends with HMAC. The
Node carrier connects to that pipe and reuses the published DSH WebServer route,
index and upgrade contracts without calling TCP listen. The HTTP authority
`http://127.0.0.1:1` is an internal routing identity; the transport can only dial
the pipe. Credentials and carrier modules live in an owned temporary directory
and are removed with the generation.

The DSH process still uses its normal Node HTTP server and route semantics. The
carrier changes the server's listening transport to the authenticated generation
pipe; it does not replace DSH routes with a second application protocol. The
`127.0.0.1:1` authority is therefore an origin used for relative URLs and request
routing, not a TCP listener. A WebView cannot dial the named pipe directly.
The daemon is the resident Host and forwarding owner; each DSH launch is a
separately supervised Worker generation behind that boundary.

The Worker WebView has two Host-side request paths, selected by route semantics:

| Request shape | WebView to Host | Host to daemon and Worker | Contract |
|---|---|---|---|
| Ordinary finite HTTP | Wails' internal HTTP asset handler and `fetch` | Existing authenticated generation pipe | Normal HTTP request and response bodies |
| Streaming or cancellable HTTP | Wails bounded byte stream (`worker-fetch`) | Existing authenticated generation pipe | Backpressure, early chunks and cancellation |
| WebSocket upgrade | Wails bounded byte stream (`worker-websocket`) | Existing authenticated generation pipe | Bidirectional WebSocket frames |

The standard HTTP path is an opt-in diagnostic path (`DSH_WORK_STANDARD_HTTP=1`).
It removes the injected Worker fetch bridge for ordinary requests and lets the
Wails HTTP handler call the same daemon transport used by the stream path. This
does not add a port and does not change DSH or daemon pipe authentication. The
regular application keeps the stream path as its default so that all existing
streaming and WebSocket behavior remains available.

On Windows, the Wails 3 AssetServer response writer buffers its output until the
handler finishes and does not expose `http.Flusher`. Consequently the standard
HTTP path cannot provide early response chunks, cancellation before end-of-body,
or WebSocket upgrades on that platform. Long-running DSH output, tool streams and
WebSocket routes must continue to use the bounded byte streams until Wails offers
a stream-capable HTTP handler. The Go proxy still validates relative paths,
removes hop-by-hop and credential headers, sets the internal Origin and preserves
the same generation checks as the stream path.

The workbench window (`workspace`) hosts the trusted shell document at
`http://wails.localhost`; Settings has its own trusted window. DSH runs in an
iframe inside the shell on the fixed authority `http://wails.localhost:48217`.
That port is an origin label only: WebView2 requests to any `wails.localhost`
authority are intercepted by the Wails asset server, so nothing listens on it.
It stays fixed so DSH's browser storage (drafts, shortcut preferences) survives
Worker generations.

Every request from the frame carries the workbench window's native ID, so the
request authority selects the role instead. Requests whose Host is the DSH
authority get the Worker role: only `/wails/runtime.js`, `/wails/custom.js` and
the two Stream endpoints pass, other `/wails/*` routes are denied, and the
generation is checked. On the shell authority, requests whose `Origin` or
`Referer` belongs to the DSH authority are refused, the shell may not open
Streams (a frame reload would retire the shell's Stream session), and every
response carries `Content-Security-Policy: frame-ancestors 'none'` so the
frame cannot navigate itself into a same-origin shell page. Messages a framed
document posts through `chrome.webview` do not reach Wails, so the frame has no
native drag, resize or invoke channel. Worker service-worker registration is
blocked.

Wails' host-prefix interception (including the port) is an implementation
detail, not a documented contract; `TestRealShellFrame` (`DSH_WORK_SHELL_TEST=1`)
exercises the framed Worker through the real bridge and must pass after Wails
upgrades. `DSH_WORK_SHELL=0` still selects the earlier two-window workbench, in
which the DSH window (`worker`) is told apart by its native window ID.
Resources use the native asset handler and daemon Worker forwarding. The default
Fetch and WebSocket bodies use Wails Streams with 64 KiB chunks, upload
acknowledgements and download credits. The daemon forwards traffic to the
generation pipe; the optional finite-request HTTP path is described below.
HTTP full duplex and an independent bounded upload writer keep download credits
and cancellation live while an upload is in progress.
Every stream names its document generation. Worker authentication cookies stay
in the Host cookie jar. External HTTP(S) links open through a narrow callback.

The daemon owns the Worker independently of UI visibility and process lifetime.
Restart closes streams and the pipe, verifies the process boundary, then starts
the next generation. Safe mode uses the same channel with its own profile and
omits optional activity and user-data overlays. The process supervisor remains
the authority for graceful stop, forced stop and process-tree cleanup.

The OS transport uses `go-winio`, HTTP uses the Go/Node standard libraries and
WebSocket uses `coder/websocket` plus the selected profile's upstream routes.
Application code adapts those libraries to Wails and DSH ownership contracts;
it does not implement HTTP, WebSocket framing or named-pipe security itself.

DSH Remote calls and streams use DSH's own published `/api/remote.mux`
WebSocket route, carried by the `worker-websocket` stream like any other
upgrade. DSH owns the frame format, bidirectional streams, peer admission and
stream lifetime. dsh-work does not install a private stream adapter or depend
on version-specific internal stream signatures, and it does not parse Remote
frames to drive Host behaviour.

### Account sign-in

DSH mounts its account UI only in a desktop renderer, so the Worker bridge
defines the `dshDesktop` marker before DSH's entry modules load. Sign-in then
spans three owners:

| Step | Owner | Behaviour |
|---|---|---|
| Start | DSH account UI | Calls `account/startSignIn` with a loopback callback origin. The call returns before an authorization URL exists. |
| Open the browser | dsh-work client plugin | Follows the official `account/watch` stream. When an attempt reaches `waiting-browser`, it calls `window.open` with that attempt's HTTPS `authorizeUrl` once. The Worker bridge routes the call to the system browser. |
| Return | Daemon callback listener | Receives the browser redirect, relays it to the current Worker generation and shows a dsh-work result page. |

The browser must reach the callback, and DSH accepts only an
`http://localhost|127.0.0.1|[::1]:<port>` origin. The daemon therefore owns one
listener on `127.0.0.1` with an ephemeral port for its whole lifetime, so a UI
client restart does not invalidate an authorization URL that is already open. It
serves only `GET /` (a waiting page) and `GET /oauth/callback` with `state` and
`code` or `error`. The request's `Host` header must match the listener and the
peer must be loopback. When the daemon forwards `POST /api/account/startSignIn`
to the Worker, it replaces `callbackOrigin` with the listener origin and
records the Worker generation. A callback for another generation, or with no
generation, gets an expired page.

The callback is relayed through the same daemon Worker route as WebView
requests. DSH holds that response until the code exchange finishes and answers
`302` on success. The listener treats any 2xx or 3xx without an `error` query
as success and never follows DSH's redirect or buffers its body. The result page
links to `dsh-work://open`, which reopens the workspace.

The attempt is opened from `account/watch` because the URL arrives only there.
An attempt that is already waiting when the page connects is not reopened,
which covers page reloads. DSH's desktop shells open the browser the same way.

The installer registers `dsh://` and `dsh-work://` for the current user and
overwrites any existing `dsh://` handler. It backs up the previous values once
and restores them on uninstall only if the registration still points at this
installation. `dsh-work://open` is the reliable return link even if another
application later claims `dsh://`. Both `dsh://open` and `dsh-work://open`
focus or open the workspace. macOS and Linux declare the same schemes through
the Wails build configuration.

## Launch adapter and byte semantics

The desktop carrier preserves upstream opaque resource queries, including DSH
plugin bootstrap URLs; parsing and re-encoding those queries changes their
meaning. Readiness uses the owned authenticated channel rather than probing a
printed loopback URL. Production readiness has a bounded 60-second deadline.
Candidate and recovery diagnostics retain separate failure
causes with bounded, redacted output.

Byte transport describes the carrier, not a replacement for DSH application
protocols: HTTP, JSON, text and WebSocket payloads retain their upstream semantics.
The regular desktop executable uses this architecture directly. The former TCP
gateway and standalone prototype/comparison entry points are removed.

## Manager persistence

`internal/dshmanager` persists the configured Run context and its known local
catalog records in the application-data manager file. This manager State is a
field-based contract without a schema-version marker. Loading decodes the
fields this build understands, ignores unknown fields, and validates the
identities required to use the configuration. It does not translate historical
file layouts. Runtime, Node, DSH-release and plugin records retain their own
business version fields; those are data, not the manager State schema.
The optional `versionRecovery` field has its own schema version (currently 1),
covering snapshot inputs, per-profile success pointers and recovery progress.

Saving uses an atomic replace so a completed write contains one complete set of
known fields. A malformed required identity is reported as invalid manager
state instead of being used for a launch.

## Version recovery and window geometry

`internal/dshmanager/restore_points.go` records verified version inputs and
persists recovery stages in the existing atomic manager state. `internal/app`
owns the stop/install/start boundary and failure-policy selection.
`internal/dshadapter/version_profile.go` owns the DSH dependency-input contract;
the Windows runtime installer and DSH plugin CLI perform package installation.
The Host does not remove or move `node_modules`. See
[Version recovery](version-recovery.md) for capture, retention and retry rules.

`internal/settings` stores independent Workspace and Settings window geometry.
`internal/nativeui/window_geometry.go` restores options before window creation,
observes native resize/maximise events and debounces persistence by 300 ms.
Close hooks and UI shutdown flush captured state through the daemon settings API. Only normal-window
sizes replace the saved dimensions; maximised state is stored separately and
minimised/fullscreen observations are ignored. Position is still centred on
creation; this feature does not persist screen coordinates.

## Confined version-file access

Version input reads and writes remain confined to the selected data directory.
On Windows, `google/safeopen` opens files component by component beneath that
home, including profile path components. This addresses the observed Go 1.25
`os.Root` `OBJ_DONT_REPARSE` failure in the daily redirected AppData environment
while retaining traversal checks. `os.Root` remains in use for rooted publication
and removal, and on other platforms. Windows NTSTATUS errors are normalized so
missing optional inputs preserve ordinary filesystem semantics.

The dependency supplies no-follow file access; it was chosen over custom native
filesystem infrastructure. This is a scoped response to reproduced behavior,
not a general claim that `os.Root` fails on ordinary Windows filesystems.

## Desktop Pet boundary

The Pet follows the same boundary: the daemon Host owns package validation, selection,
visibility, persistence and the native window; `internal/pet` normalizes
supported data-only package formats; the trusted frontend only projects state
and sends allowlisted user intent. Pet package contents cannot create Host
capabilities.

## Runtime invariants

1. The daemon obtains the single-instance and manager locks before exposing
   mutable state. UI clients do not open a second writable manager.
2. Normal startup resolves a complete local Run context. Snapshot recovery is
   a separate installation path and may acquire recorded versions.
3. The Worker enters its platform process boundary before it can run.
4. A fresh generation ID scopes startup, readiness, IPC and Workspace
   events; obsolete-generation events are ignored.
5. Workspace navigation occurs only after authenticated readiness checks.
6. Run-context switching stops and verifies the old generation before starting
   a candidate. Candidate and previous Worker generations never overlap.
7. Readiness establishes the current Worker. It requires the authenticated
   channel checks and a terminal report from the current generation's WebView.
   The report says whether DSH replaced its boot placeholder
   (`data-dsh-boot`) with the mounted application. A plugin activation error,
   the 60-second deadline, cancellation or an obsolete generation is a failed
   start, not readiness. During this phase only the candidate page is exposed.
   When no UI client is open, the daemon uses a hidden Worker WebView to
   confirm. Verified version inputs advance
   the durable success record only after a successful save. Recorder failure
   keeps the healthy Worker running and retains the previous durable record.
   A failed candidate is cleaned up before policy-controlled, bounded recovery.
8. Closing a desktop window hides it while retaining its UI client and WebView;
   reopening reuses that window and page state. Explicit background Quit cancels
   owned work, verifies Worker/child cleanup, closes remaining UI, releases locks
   and exits the tray. Legacy close preferences cannot invoke it.
9. Pet size and position changes are applied through the Host. A failed native
   resize does not leave persisted dimensions claiming a state the native window
   did not reach.

## Windows and menus

The workbench is one frameless window using WebView2 composition hosting. The
shell document draws a 40px top bar: the app icon, the menu bar, an empty
caption area and the minimise, maximise and close buttons. The caption area and
the three buttons are marked with `--wails-non-client-region`, so Windows
hit-tests them natively: dragging, double-click maximise, Snap layouts on the
maximise button and edge resizing work even where the DSH frame covers the
window. Plain HWND hosting is not used: there Wails detects window edges from
mouse moves in the top document, which never sees moves over the frame.

The top bar takes DSH's `--dsw-specific-sidebar-fill` (the colour DSH's own
Windows caption uses) and text colour, reported by the injected bridge, so the
chrome continues DSH's surface in light and dark. Before DSH is ready it uses
dsh-work's own tokens.

The menu bar is drawn by the shell (`frontend/src/shell-menu*.ts`):

| Menu | Items (DSH commands marked *) |
|---|---|
| 文件 | 新会话*, 搜索会话*, 添加工作区* · 新终端*, 新浏览器* · 关闭窗口, 退出 |
| 视图 | 左侧栏*, 右侧栏* · 放大, 缩小, 实际大小, 全屏 · 显示桌面宠物 |
| 运行 | 刷新, 重启 DSH · 启动安全模式 / 退出安全模式 |
| 设置 | 概览 · 通用, 通知, 宠物 · 运行环境, 配置, 插件 · 存储位置 · DSH 设置* |
| 帮助 | 文档, 键盘快捷键* · 桌面版反馈, DeepSeek 反馈 · 复制诊断信息, 打开调试窗口 · 检查更新 (or 有新版本可安装 / 更新中…), 关于 dsh-work |

Host items call trusted bindings. `ShellService` runs in the UI process: it
opens Settings at a section, and reads or sets Pet visibility on behalf of the
Settings surface, which is the only surface the daemon grants Pet controls.
Restart and quit keep the availability rules of the tray; safe mode uses the
same `ManagerService` calls as Settings Overview and opens Overview when it
fails. 刷新 reloads only the DSH frame; the Worker keeps running. Zoom, full
screen and close use the Wails window API (close hides the window). Zoom
scales the whole window, shell bar included, and cannot go below actual size
(Wails clamps WebView2 zoom at 100%), so 缩小 and 实际大小 are off at 100%. 全屏
is a checked item; full screen hides the window buttons and is left through
the same item. Ctrl+= (or Ctrl++), Ctrl+-, Ctrl+0 and F11 run the same window
items, also while DSH has focus, unless DSH binds that chord itself. The zoom
factor is saved in Host settings and restored when the workbench opens. The
links open the dsh-work docs and issue tracker and the DeepSeek Harness issue
tracker in the system browser. 复制诊断信息 copies the same report as About. 检查更新
starts a check and reports the outcome in a short status line under the bar;
when an update is already found, downloading or not configured, it opens About
instead.
Release builds include the WebView developer tools (`devtools` build tag), so
打开调试窗口 works for every user, as in other desktop apps built on web views.

DSH commands run from a fixed list through the dsh-work DSH client
plugin (`internal/dshactivity/plugin/client.js`). The plugin reports each
command's current binding from `ctx.shortcuts.catalog`, and runs a command the
shell asks for by dispatching that binding through DSH's keyboard path, since
DSH offers plugins no public command invoke. A command is disabled and marked
未绑定 when it has no binding, or 不可用 when the menu cannot press it: a
conflicting, invalid or two-key binding, or DSH shortcut settings that are
still loading. DSH prevents default on the key press it consumes, so the
plugin reports whether DSH took the command; when DSH ignores it, the shell
shows that the command could not run. The plugin also forwards Alt pressed
alone and F10, which focus the menu bar, and the window keys above. Shell and plugin accept messages only from each other's
window and origin.

Settings is a separate window with its own locale-aware title and no menu. The
tray belongs to the daemon and can open Settings or the workbench, restart DSH,
and explicitly stop the background. Pet and native notification adapters remain
in the daemon, so closing the UI preserves them.

Assign the initial Settings URL (and, in the two-window workbench, the Worker
URL) before creating the window to avoid a
second initial navigation. On Windows, child launch uses CREATE_NO_WINDOW to
suppress the console; STARTF_USESHOWWINDOW/SW_HIDE would override the first
native ShowWindow request and must not be used for the UI launch.

## Platform boundary

The shared Supervisor contract compiles for Windows, macOS and Linux. Windows
has the production process-ownership implementation and daemon named-pipe
transport. The daemon transport returns an unsupported-platform error on other
targets. Other targets must not be
described as production-equivalent until native ownership and packaging tests
exist.
