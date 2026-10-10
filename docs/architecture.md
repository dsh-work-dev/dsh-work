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

The Host is Go and Wails 3; DSH runs as a separate, supervised process of one
exact version. DSH, Wails and platform behaviour sit behind adapters, and
embedded content gets no broad native bridge.

The daemon is a separate process so that tasks survive a crash of the whole UI
process, not only a hidden window; portless communication alone would not
require the split. The cost is a local transport hop and typed projection of
state and events. The daemon is neither a remote access service nor a
system-wide Windows service.

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
| `internal/desktopbridge` | Worker resource delivery and HTTP forwarding, the Gateway WebSocket, upload and session-export carriers over Wails byte streams, external-link opening |
| `internal/accountcallback` | daemon-owned loopback OAuth callback listener, callback-origin rewrite and result pages |
| `internal/hostplugins` | dsh-work's own DSH plugins (`@dsh-work/shell`, `@dsh-work/account`, `@dsh-work/pet`), installed per application version, and the per-launch core overlay that mounts them |
| `internal/dshactivity` | DSH session activity projection for the desktop pet, fed by `@dsh-work/pet` |
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

The DSH document keeps the browser's own `fetch`, `WebSocket` and `Worker`.
Only four routes need more than a finite request, and each has its own carrier:

| Request | WebView to Host | Contract |
|---|---|---|
| Ordinary finite HTTP (RPC, `/api` routes, plugin bundles, assets) | Native `fetch` through Wails' asset handler; non-GET requests are proxied to the Worker | Complete request and response bodies |
| DSH Gateway stream `/api/remote.mux` | Wails byte stream (`worker-websocket`); only this exact same-origin route is adapted | Bidirectional WebSocket frames, carried unparsed |
| Attachment upload `/api/session/uploadFileBinary` | DSH's `dsh-file-upload` Worker, adapted to a Wails byte stream (`worker-fetch`) | Upload progress and cancellation as DSH's Worker reports them |
| Session export `/api/session.export` | DSH's download anchor is handed to the Host, which asks for a destination and writes the archive | The ZIP never enters the WebView |

Everything goes to the same daemon and generation pipe; no route opens a TCP
listener. The Wails WebSocket bridge refuses any route other than
`/api/remote.mux`.

On Windows, the Wails 3 AssetServer response writer buffers its output until the
handler finishes and does not expose `http.Flusher`, and requests carry no
WebView cancellation. The asset path is therefore used only for requests whose
complete bodies are acceptable; the Gateway stream, uploads and exports use the
carriers above. The Go proxy validates relative paths, rejects CONNECT and
TRACE, removes hop-by-hop and credential headers, sets the internal Origin and
checks the generation. Revisit the dedicated carriers when the Wails HTTP
handler can stream and upgrade.

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
upgrades.
The byte streams use 64 KiB chunks, upload acknowledgements and download
credits; HTTP full duplex and an independent bounded upload writer keep credits
and cancellation live during an upload. Every stream names its document
generation. Worker authentication cookies stay in the Host cookie jar.

Serving the DSH page from DSH's own loopback HTTP server is rejected. DSH's
`SameSite=Strict` session cookie is not sent from a cross-site iframe, and the
only working workaround, turning off DSH session authentication, would let any
local process drive the agent. Measured in the WebView, the stream path costs
about 1 ms per message round trip and carries about 116 MB/s; a direct
WebSocket is faster, but not visibly in the UI.

### Page bootstrap and host plugins

dsh-work extends DSH only through DSH's own plugin and page interfaces, in four
layers:

| Layer | What | Loaded |
|---|---|---|
| Transport core | The pipe carrier and the carriers above | Always |
| Host plugins | `@dsh-work/shell`, `@dsh-work/account`, `@dsh-work/pet` | Always, including safe mode |
| User-data overlay | DSH sessions, storage, attachments, settings and credentials in dsh-work's user-data folder | Not in safe mode |
| Profile plugins | Third-party bundles in the user's profile, managed by DSH's PluginManager | Only in the selected profile; safe mode uses a clean one |

The host plugins are ordinary DSH plugins (a Host half and a Client half each).
They are embedded in dsh-work, written below a directory named by application
version and content digest, and inserted by a per-launch `--patch` core overlay
rather than installed into a profile, so profile switches, version recovery and
uninstalls do not touch them. The daemon installs them once per process
(rewriting a damaged file and removing unused plugin versions); each launch
then writes only a small patch carrying that generation's configuration, which
replaces the previous launch's patch.
Their Client halves report readiness to the shell; Settings lists them under
内置组件 as Loading, Loaded, Failed to load (no report within 45 seconds of the
frame loading) or Not loaded, and offers no switches.

| Plugin | Host half | Client half |
|---|---|---|
| `@dsh-work/shell` | Injects the page bootstrap through DSH's `webserver/index-inject`: the generation, `__DSH_TRANSPORT__`, and the boot reporter and transport bridge scripts, ahead of DSH's entry | Shell protocol (command catalog and results, sidebar state, Alt/F10 and window keys), link policy, surface colours |
| `@dsh-work/account` | Injects the `dshDesktop` marker so DSH mounts its account UI | Opens a waiting authorization URL in the system browser; reports sign-out progress to the shell's status line |
| `@dsh-work/pet` | Publishes the session activity snapshot and navigation routes the desktop pet uses, for one generation | Synchronizes session activity and opens the conversations the pet asks for |

`__DSH_TRANSPORT__` declares `ownsHost: true`. DSH otherwise treats the
non-loopback `wails.localhost` page as a remote browser and keeps every setting
form in memory, neither loading nor saving it. DSH's own desktop shell declares
the same for its `dsh-app://` page.

The link policy sends links and `window.open` calls to other origins to the
system browser and drops same-origin new-window requests, so DSH never runs as
an unmanaged top-level page outside the shell. Revisit this if DSH starts
relying on same-origin pop-ups.

Each concern has one named, testable owner. Adapting routes by exact path
leaves every other plugin's `fetch`, `WebSocket`, `window.open` and link clicks
untouched, and because the host plugins load independently of user data, safe
mode keeps the menus, sign-in and the pet. Not chosen: starting DSH as a
library (`runProfile` offers no seam for the pipe carrier), and the full
Electron desktop runtime (`dshDesktop.keyboard`, shortcut storage and native
key capture), which may be revisited later.

The adapters and `ownsHost` depend on DSH 0.2.x page globals
(`__DSH_TRANSPORT__`, the `dsh-file-upload` Worker name, the export route) with
no stability promise; `TestRealShellFrame` and the host-plugin tests guard them
on each DSH upgrade.

The daemon owns the Worker independently of UI visibility and process lifetime.
Restart closes streams and the pipe, verifies the process boundary, then starts
the next generation. Safe mode uses the same channel and host plugins with a
clean profile and without the user-data overlay. The process supervisor remains
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

DSH mounts its account UI only in a desktop renderer, so `@dsh-work/account`
injects the `dshDesktop` marker before DSH's entry modules load. Sign-in then
spans three owners:

| Step | Owner | Behaviour |
|---|---|---|
| Start | DSH account UI | Calls `account/startSignIn` with a loopback callback origin. The call returns before an authorization URL exists. |
| Open the browser | `@dsh-work/account` | Follows the official `account/watch` stream. When an attempt reaches `waiting-browser`, it calls `window.open` with that attempt's HTTPS `authorizeUrl` once; the shell link policy opens it in the system browser. |
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

The callback listener is the one exception to "no TCP listener". It accepts no
Worker traffic and only local requests. Revisit it if DSH offers a callback
that does not need a loopback origin.

## Runtime catalog

dsh-work keeps an external catalog of exact-version DSH runtimes and explicit
Node, data-directory and profile selections. Normal startup resolves local
files only. Version recovery is the separate path that force-reinstalls the
recorded DSH version into its managed directory through npm or pnpm and may
download packages; a matching version alone does not skip repair.

Each runtime owns independent files. pnpm installs use
`package-import-method=clone-or-copy` instead of hard-linking from the user's
store, because Windows refuses to delete any link of a native module that a
running Worker has mapped, so a shared file would pin every other runtime.
Copy-on-write clones are used where the volume supports them; otherwise each
runtime costs a full copy on disk. The store location is left to the user's
pnpm configuration.

Removing a runtime first renames its directory aside inside the store, then
deletes it. The catalog is updated once the rename succeeds; a locked file
that blocks the rename fails the removal with the runtime intact. Deletion
after the rename is best effort: a file still held open stays aside and is
retried on later removals without blocking them. Directories not in the
catalog are never swept, because a lost or reset manager state would otherwise
delete every installed runtime.

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

Without a marker, startup asks whether the selected configuration is usable
rather than whether a marker matches the running build; the required identity
checks still keep an ambiguous or incomplete selection from reaching launch.
Global Host preferences are a separate, versioned `internal/settings` contract
stored apart from DSH data.

## Plugin activation

When a profile's Worker is Ready, enabling or disabling a plugin bundle or an
official loader entry calls DSH's own PluginManager Remote methods,
`setBundleEnabled` and `setPluginEnabled`, over the current Worker's
authenticated session. The Host checks that the session generation matches the
current Host generation and that the target is the running profile. DSH saves
and applies the change, including dependency retention and runtime unload.
Switches appear only where PluginManager reports an item can be toggled; a
non-running profile or a Worker that is not Ready shows none. dsh-work keeps no
disable ledger of its own. Install, upgrade and uninstall use DSH's plugin
commands through the manager.

A failed start has no Worker to call. The startup window can then disable a
third-party bundle that the failure output names, with the switch lock held,
no Run context current, and the package confirmed as an installed,
non-`@deepseek-ai` bundle of the failed profile. It removes the package from
the profile manifest's `dsh.profile.bundles`, keeping the installed files and
loader patches, and starts again; this is the same persisted choice as
`setBundleEnabled(name, false)`. The DSH adapter reads the packages named in
the captured output. Only third-party packages installed in the failed profile
are offered, and a failure caused by DSH rejecting its own stored session data
offers none, because the plugin that reported it did not cause it.

Using DSH's API keeps dsh-work in step with DSH's deselection, dependency and
unload semantics as they change. The cost is that activation changes need a
running Worker, apart from the startup-failure path.

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
   Manager operations are serialized across desktop and CLI. Normal plugin
   changes require the current healthy profile; recovery reapplies recorded
   inputs after the Worker has stopped. Startup-failure bundle deselection
   ([Plugin activation](#plugin-activation)) is the one exception.
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

The shell owns every host command (restart, quit, update, Settings), so none is
reachable from DSH plugins, and the chrome and recovery entry stay usable when
DSH hangs or fails. A later same-window Settings panel reuses the same
origin-based trust. DSH loses its Electron caption layout and runs as a plain
web page under the bar. Rejected: drawing the bar inside the DSH document (host
commands reachable from DSH plugins, chrome dies with DSH); two native WebViews
in one window (no Wails API, and shell menus could not overlay DSH); a coloured
native title bar (the menu stays on its own row); switching to Electron
(rewrites the host). Composition hosting is an experimental Wails 3 beta
option and is covered by the same `TestRealShellFrame` check after upgrades.

The menu bar is drawn by the shell (`frontend/src/shell-menu*.ts`). Menus are
grouped by what the user does, not by who implements an item. Per-session
actions (rename, fork, archive, stop) stay in DSH's own menus; environment
switching and version records stay in Settings Overview. Not offered: new
window, command palette, focus mode, always on top and recent workspaces.

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
Only development builds open a remote-debugging port, and only when
`DSH_WORK_WEBVIEW_DEBUG_PORT` names one.

DSH commands run from a fixed list through `@dsh-work/shell`. The plugin
reports each command's current binding from `ctx.shortcuts.catalog`; the
workspace and layout services are optional, so a profile without them still
gets the menu, with the commands they serve marked 不可用. 新会话 and
左侧栏 call DSH's public services (`ctx.uiWorkspace.startSession()`,
`ctx.layout.toggleSidebar()`), and the plugin reports the sidebar state so 左侧栏
shows a check. Other commands are run by dispatching their binding through
DSH's keyboard path, since DSH offers plugins no public command invoke. A command is disabled and marked
未绑定 when it has no binding, or 不可用 when the menu cannot press it: a
conflicting, invalid or two-key binding, or DSH shortcut settings that are
still loading. DSH prevents default on the key press it consumes, so the
plugin reports whether DSH took the command; when DSH ignores it, the shell
shows that the command could not run. The plugin also forwards Alt pressed
alone and F10, which focus the menu bar, and the window keys above. Shell and plugin accept messages only from each other's
window and origin.

DSH's official desktop menu path (`dshDesktop.keyboard`) is active only in its
Electron desktop runtime, so dispatching bindings is the available route. It
depends on DSH accepting script keyboard events; revisit it if DSH publishes a
plugin command invoke.

Settings is a separate window with its own locale-aware title and no menu. The
tray belongs to the daemon and can open Settings or the workbench, restart DSH,
and explicitly stop the background. Pet and native notification adapters remain
in the daemon, so closing the UI preserves them.

Assign the initial window URL before creating the window to avoid a second
initial navigation. On Windows, child launch uses CREATE_NO_WINDOW to
suppress the console; STARTF_USESHOWWINDOW/SW_HIDE would override the first
native ShowWindow request and must not be used for the UI launch.

## Platform boundary

The shared Supervisor contract compiles for Windows, macOS and Linux. Windows
has the production process-ownership implementation and daemon named-pipe
transport. It uses `golang.org/x/sys/windows` to create the Worker suspended,
assign it to a kill-on-close Job Object before resume, limit inherited handles
and verify bounded cleanup; the more focused libraries evaluated did not cover
that whole launch contract. Settings and manager files are written with a
native replace/write-through operation behind the same boundary. The daemon transport returns an unsupported-platform error on other
targets. Other targets must not be
described as production-equivalent until native ownership and packaging tests
exist.
