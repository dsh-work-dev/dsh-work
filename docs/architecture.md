# Architecture

## System context

```text
User
  |
  v
Desktop UI client (Go + Wails)
  |-- workbench window: trusted shell (top bar, menus, startup, Settings panel)
  |     `-- DSH frame on its own origin --> Wails HTTP handler or bounded byte streams
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
| `internal/desktopapp` | daemon/client process assembly, window/menu lifetime, the daemon stream reader and per-window command delivery |
| `internal/daemon` | current-user IPC, control dispatch, the UI state stream and Worker forwarding |
| `internal/hostevents` | transport-neutral daemon state topics and expiring one-shot commands |
| `internal/presence` | per-surface focus used by notification and navigation decisions |
| `internal/desktopclient` | typed UI service proxies to the daemon |
| `internal/app` | Host commands, manager operations and process locks |
| `internal/lifecycle` | window policy, quit flow and lifecycle value types |
| `internal/supervisor` | platform-neutral Worker process contract |
| `internal/platform` | production platform selection and native adapters |
| `internal/platform/windows` | process creation, Job Object cleanup, atomic file replacement and explicit runtime installation |
| `internal/dshadapter` | minimum-version launch, readiness, profile and plugin command grammar, startup-failure plugin attribution |
| `internal/dshmanager` | runtime catalog, profiles, plugin commands, startup-failure bundle deselection, version snapshots and serialized recovery state |
| `internal/workspacecontext` | per-generation DSH Workspace context |
| `internal/workerchannel` | per-generation channel, authentication cookies and activity lifetime |
| `internal/workeripc` | current-user authenticated OS pipe carrying upstream HTTP bytes |
| `internal/desktopbridge` | Worker resource delivery and HTTP forwarding, the Gateway WebSocket, upload and session-export carriers over Wails byte streams, external-link opening |
| `internal/accountcallback` | daemon-owned loopback OAuth callback listener, callback-origin rewrite and result pages |
| `internal/hostplugins` | dsh-work's own DSH plugins (`@dsh-work/shell`, `@dsh-work/account`, `@dsh-work/pet`), installed per application version, and the per-launch core overlay that mounts them |
| `internal/dshactivity` | DSH session activity projection for the desktop pet, fed by `@dsh-work/pet` |
| `internal/dshinteraction` | pending DSH questions, approvals and plan reviews reported by each workbench window's DSH page |
| `internal/settings` | versioned Host preferences and persistence contract |
| `internal/notifications` | notification vocabulary, preference evaluation, routing and bounded deduplication |
| `internal/nativeui` | native menus, notifications and window-geometry persistence wiring |
| `internal/pet` | data-only Pet catalog adapters, bounded cache, runtime, renderer and overlay projection |
| `internal/redact` | secret removal shared by Worker output and log records |
| `internal/logging` | per-process structured log files, rotation, crash capture and the log file layout |
| `internal/diagnostics` | daemon-side problem list, issue report and diagnostics bundle derived from the log files |
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
selects the allowed role before forwarding management calls. Every UI request to
the daemon is a `/control` service call that carries the calling window as its
surface (`workspace`, or `workspace-<n>` for further workbench windows); daemon
services read it with `daemon.SurfaceFrom` and never take a window name from
arguments. Window focus and closing, zoom, window geometry, Worker boot reports
and update actions are `WindowService` and `HostService` methods.

The daemon pushes to each UI process over one long-lived `GET /stream` of
newline-delimited JSON; a new connection replaces the previous one. A line is a
state topic (`lifecycle`, `worker-url`, `update`, `preferences`, `acquisition`),
a one-shot command for all windows or one named window (`notification-failure`,
`background-stopping`, `acquisition-log`, `dsh-open-session`,
`dsh-answer-approval`), or a ping every 10 seconds. A new
connection first receives the current value of every topic, then the commands
staged while no UI was connected (at most 64, discarded once expired). Each
topic publishes its source's current value, read under one lock, whenever that
value changes; download progress is throttled to 250 ms with a trailing update.
A connection that falls 256 messages behind is closed. The UI process emits
topics and commands as Wails events, holds a window's commands until that
window's `@dsh-work/pet` component is ready (16 per window, oldest dropped), and
exits when the stream ends or stays silent for 30 seconds, so a lost or replaced
daemon ends the connected UI client. `/snapshot` remains a one-shot read at UI
start. The daemon remains the source of lifecycle truth.

The stream replaced 350 ms snapshot polling with a replayed event ring. Polling
delayed every change, mixed state with one-shot events in one ring, and needed a
replay cursor although a reconnecting UI is always a new process. Topics and
commands are defined in `internal/hostevents`, which depends on neither IPC nor
Wails, so another authenticated transport can carry the same vocabulary. The
local link has no version negotiation because the UI and daemon ship as one
executable; revisit that when a remote surface consumes the stream.

The Host creates a fresh authenticated named pipe before each Windows Worker
launch. `go-winio` enforces a current-user SID ACL and rejects remote pipe
clients. Per-launch random credentials authenticate both ends with HMAC. The
Node carrier resolves DSH's `WebServer` and `Service` classes from the selected
DSH runtime executable, then reuses the published WebServer route, index and
upgrade contracts without calling TCP listen. The profile directory is not a
module-resolution base because it may contain stale or incomplete copies of DSH
core packages. Core DSH packages are not copied into profiles; the selected
runtime remains their module source for each launch. The carrier exposes logical
port `1` so `WebServer.getOrigin()` produces `http://127.0.0.1:1`; this is an
internal routing identity, and the transport can only dial the pipe. No TCP
listener is opened. Credentials and carrier modules live in an owned temporary
directory and are removed with the generation. Revisit this boundary if DSH
publishes a stable, version-independent carrier interface.

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
`http://wails.localhost`; startup and the Settings panel belong to that document. DSH runs in an
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

dsh-work extends DSH only through DSH's own plugin and page interfaces, in three
layers:

| Layer | What | Loaded |
|---|---|---|
| Transport core | The pipe carrier and the carriers above | Always |
| Host plugins | `@dsh-work/shell`, `@dsh-work/account`, `@dsh-work/pet` | Always |
| Profile plugins | Third-party bundles in the user's profile, managed by DSH's PluginManager | Only in the selected profile |

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
| `@dsh-work/shell` | Injects the page bootstrap through DSH's `webserver/index-inject`: the generation, `__DSH_TRANSPORT__`, and the boot reporter and transport bridge scripts, ahead of DSH's entry | Shell protocol (command catalog and results, sidebar state, Alt/F10 and window keys), link policy, dsh-work theme, modal mask reports |
| `@dsh-work/account` | Injects the `dshDesktop` marker so DSH mounts its account UI | Opens a waiting authorization URL in the system browser; reports sign-out progress to the shell's status line |
| `@dsh-work/pet` | Streams the session activity snapshot the desktop pet uses, for one generation | Reports session titles and parent sessions when the session list changes, and opens the conversations its workbench window is asked to open |

`__DSH_TRANSPORT__` declares `ownsHost: true`. DSH otherwise treats the
non-loopback `wails.localhost` page as a remote browser and keeps every setting
form in memory, neither loading nor saving it. DSH's own desktop shell declares
the same for its `dsh-app://` page.

The link policy sends links and `window.open` calls to other origins to the
system browser and drops same-origin new-window requests, so DSH never runs as
an unmanaged top-level page outside the shell. Revisit this if DSH starts
relying on same-origin pop-ups.

dsh-work's appearance reaches DSH through DSH's public theme service, with a
separate path for the default DSH theme and for themes that restyle DSH. The
shell computes the title-bar frame from the workbench theme in either path.
When `data-theme-id="dsh"`, `frontend/src/shell.ts` sends the resolved
light/dark mode with empty tokens and returns before computing DSH theme
tokens, decorations or control samples. The shell plugin registers that empty
token set with the mode through `ctx.theme.register` and selects it, so DSH
uses its native surfaces, controls and fonts. DSH's own Appearance row remains
hidden, and its stored `ui-theme.preference` is unchanged. DSH's presenter
controls its dark-mode attribute; the plugin does not write it directly.

For other themes, the shell maps computed workbench colours, radii, shadows and
the interface font to DSH `--dsw-*` tokens (`frontend/src/dsh-theme.ts`),
registers them with the resolved mode and selects the page-only theme. Each
change registers the next theme before releasing the previous one. Unmapped
values stay native. DSH conversation text keeps DSH's font; the plugin pins its
markdown font tokens while changing the interface font. Surfaces become opaque
because DSH composes dialogs and menus over live content and inverts colours in
some components. Text-bearing accents are adjusted to contrast on their actual
surfaces. The shell resolves values in the browser, including `color-mix()` and
transparency.

Those themes also send decoration derived from their group, dialog and button
borders and shadows: DSH has no border-width token, so a border becomes a
box-shadow ring. The shell injects the decoration stylesheet only while
`--dsh-work-raised` is present, overriding DSH's elevation values and styling
its semantic surface hooks. When the token is absent, the plugin removes the
body's `data-dsh-work-decor`, every `data-dsh-work-control` marker and that
stylesheet. This makes switching to the empty-token DSH theme clean and keeps
other themes' styling intact. For non-DSH themes, hidden workbench samples send
switch, button, field, select and tag values to DSH; the plugin classifies only
known switch structures and visibly framed/filled buttons and fields. Native
checkboxes and radios use the theme accent, option lists use its surface, and
question-card selections and markers follow its selection tokens. Theme checks
cover workbench surfaces, controls, focus and selected states in both modes.

The public theme service preserves DSH's stored preference while allowing the
page to select an appearance. Directly toggling `data-ds-dark-theme` fights
DSH's presenter; `overrideTokens` alone cannot select a light/dark base palette,
and writing `ui-theme.preference` would change DSH outside dsh-work. Stylesheet
injection through `webserver/index-inject` bypasses DSH's theme composition.
Until the plugin receives the appearance, DSH paints its stored preference,
so the first paint can differ from the workbench mode. Revisit token mappings
when DSH changes its public `--dsw-*` names; `exportInspectTokens()` lists the
current names.

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
the next generation. The process supervisor remains the authority for graceful stop, forced stop and process-tree cleanup.

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

## Storage layout

dsh-work keeps app data and user data in separate places. `config.toml` records where they are, and Settings → Storage can move either one without touching the other.

| | Windows | macOS | Linux |
|---|---|---|---|
| `config.toml` (never moves) | `%LOCALAPPDATA%\dsh-work` | `~/Library/Application Support/dsh-work` | `$XDG_CONFIG_HOME/dsh-work` |
| App data | `%APPDATA%\dsh-work` | `~/Library/Application Support/dsh-work` | `$XDG_DATA_HOME/dsh-work` |
| User data (DSH home) | `%USERPROFILE%\.dsh-work` | `~/.dsh-work` | `~/.dsh-work` |
| Cache and logs | `%LOCALAPPDATA%\dsh-work` | `~/Library/Caches`, `~/Library/Logs` | `$XDG_CACHE_HOME`, `$XDG_STATE_HOME` |

The user data folder is dsh-work's managed DSH home, laid out exactly as official DSH lays out `$DSH_HOME`. It holds profiles with their plugins and patch layers, sessions, storages, attachments, `.credentials.yaml`, and any directories plugins create. dsh-work patches none of these paths, so the profiles in one home share its sessions and credentials, as they do in DSH and dsh-tauri. Each profile also has a `.dsh-work/` folder that only dsh-work reads. It holds the profile's version recovery points (`recovery.json`), plugin provenance (`provenance.json`) and backups. These records travel with the profile, and cloning, backing up and exporting a profile all skip that folder.

App data holds what dsh-work can rebuild: runtimes and Node, host plugins, the bootstrap directory, `settings.json`, update files, and `state.json`. `state.json` stores the runtime catalog, release caches, registered data directories, the selected Run context and the in-flight recovery operation. The managed data directory's path always comes from `config.toml`; a path saved in `state.json` never overrides it.

This follows the convention that DSH shells keep DSH data in a home dot-folder (`~/.dsh` for official DSH and dsh-tauri) and their own state in the platform application directory. dsh-work uses its own home, `~/.dsh-work`, so it never shares `~/.dsh` with the CLI. An earlier layout kept the DSH home inside app data and patched DSH's session, storage, attachment and credential paths out of it. That missed every directory a plugin or newer DSH release adds, and it rewrote user-registered homes too. `config.toml` uses TOML so people can edit it by hand, and `github.com/pelletier/go-toml/v2` reads and writes it: the library is the maintained mainstream choice and emits comments from struct tags. Revisit this layout if DSH itself starts separating data from its environment.

## Runtime catalog

dsh-work keeps an external catalog of DSH runtimes and explicit
Node, data-directory and profile selections. Normal startup resolves local
files only. Version recovery is the separate path that force-reinstalls the
recorded DSH version into its managed directory through npm or pnpm and may
download packages; a matching version alone does not skip repair.
Each launch runs the selected runtime's `--version`. That observed version is
authoritative. The catalog's recorded version is a cache, refreshed when it
differs, and never a reason to refuse a launch. dsh-work refuses only runtimes
older than `MinimumVersion` in `internal/dshadapter`, currently `0.2.0-rc.2`.
That is the first release whose Worker carrier contract
(`WebServer.listenedPort` and `getOrigin`) dsh-work supports. An explicit
install of a version must still report that exact version. Revisit the minimum
when the carrier contract changes.
Releases and installed runtimes older than that minimum stay listed but are
marked not supported and cannot be selected; Settings offers only removal for
such a runtime, startup never preselects one, and an install request below the
minimum is refused before any download.

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

## DSH Work profile

dsh-work's managed DSH data directory runs the profile `dsh-work`, which the UI titles "DSH Work". dsh-work does not use DSH's `web` profile. DSH auto-initializes only its template names, so `internal/dshadapter` creates the profile files itself: before startup, before preparing a Run context, and before cloning, it writes whichever of the three `initProfile` files are missing. It writes `package.json` last and never touches files that already exist. The bundles are `@deepseek-ai/dsh-base` and `@deepseek-ai/dsh-web-app`, the same layers official DSH Desktop uses. The profile cannot be renamed or deleted, and its name is reserved case-insensitively in the managed directory. User-registered data directories never get this profile.

"Import from DSH…" in Settings → Profiles brings existing DSH data into DSH Work. The user picks a folder and dsh-work checks it. The folder can be a DSH home such as `~/.dsh`, or an earlier dsh-work app-data folder whose `environment/` is the home; in that case dsh-work reads `user-data.patch.json` to find where sessions, storage, attachments and credentials were redirected. dsh-work then lists what can come along: the chosen profile's registry plugins and patch layer, the home's session history (session folders, projection-cache records, which carry the titles, and attachments), its workspace list, and its credentials. The user ticks items. This is the only way data from another DSH comes in; the Profiles page shows profiles without kinds or data-directory labels, so the user either keeps using DSH Work or imports what they had. Merges only add and DSH Work wins every conflict: existing sessions, cache records and attachment files are skipped, workspaces merge by path, patch entries merge by `id`, plugins DSH Work already has are skipped, and credentials merge entry by entry, with per-home `grant` records never copied. Nothing is backed up because nothing is replaced. The chosen folder is never registered or changed, so importing again brings only what is new. Everything runs in one Host switch on the Ready DSH Work context: data first, then plugins.

Revisit the bundle list when the pinned DSH changes `PROFILE_TEMPLATES.web`.

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
catalog records in `state.json` in app data. This manager State is a
field-based contract without a schema-version marker. Loading decodes the
fields this build understands, ignores unknown fields, and validates the
identities required to use the configuration. It does not translate historical
file layouts. Runtime, Node, DSH-release and plugin records retain their own
business version fields; those are data, not the manager State schema.
The optional `versionRecovery` field has its own schema version (currently 1)
and keeps only the app-level part: the last-running point and the in-flight
recovery operation. Records that describe a profile live with that profile in
`<profile>/.dsh-work/`: `recovery.json` holds its version points and its
last-successful pointer, and `provenance.json` holds its plugin sources. The
manager loads `state.json` and every profile's records, drops pointers to points
that no longer exist, and validates the combined state. Each save validates the
combined state first, then writes the changed profile records and `state.json`,
each atomically, and creates no `.dsh-work/` folder for a profile without
records.

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

A failed start has no Worker to call. The same holds when a ready Worker exits
unexpectedly, for example after a plugin was enabled while it ran. The startup
window then lists every installed third-party bundle of the failed profile and can disable or remove
one, or disable all enabled ones, with the switch lock held, no Run context
current, and each package confirmed as an installed, non-`@deepseek-ai` bundle
of that profile. Disabling removes the package from the profile manifest's
`dsh.profile.bundles`, keeping the installed files and loader patches, and
starts again; this is the same persisted choice as
`setBundleEnabled(name, false)`. The DSH adapter reads the packages named in
the captured output and the list puts those first, marked. A failure caused by
DSH rejecting its own stored session data offers no plugins, because the
plugin that reported it did not cause it.

This is the whole startup-recovery surface for plugins; there is no separate
safe mode. A broken plugin is the common cause, and disabling it persistently
from the failure page fixes it without a second environment. When the output
names no plugin, disabling all of them and re-enabling one at a time in Plugins
finds the cause with the normal running-Worker controls. A safe mode (a
temporary home without third-party plugins, with a return target) is rejected:
it adds a second state machine (fault target, return target, fallback) yet
cannot repair stored-data or runtime failures, which use the same data or
runtime in any mode. Revisit if a failure class appears that a persistent disable cannot
reach, such as a broken profile manifest or home patch.

Using DSH's API keeps dsh-work in step with DSH's deselection, dependency and
unload semantics as they change. The cost is that activation changes need a
running Worker, apart from the startup-failure path.

## Version recovery and window geometry

`internal/dshmanager/restore_points.go` records verified version inputs in each
profile's `.dsh-work/recovery.json` and persists recovery stages in `state.json`. `internal/app`
owns the stop/install/start boundary and failure-policy selection.
`internal/dshadapter/version_profile.go` owns the DSH dependency-input contract;
the Windows runtime installer and DSH plugin CLI perform package installation.
The Host does not remove or move `node_modules`. See
[Version recovery](version-recovery.md) for capture, retention and retry rules.

`internal/settings` stores the workbench window geometry.
`internal/nativeui/window_geometry.go` restores options before window creation,
observes native move/resize/maximise events and debounces persistence by 300 ms.
Close hooks and UI shutdown flush captured state through `WindowService.SaveGeometry`. Only normal-window
sizes replace the saved dimensions; maximised state is stored separately and
minimised/fullscreen observations are ignored. The screen position is saved as
`X`/`Y` (negative values allowed) with a `Positioned` flag, so `(0, 0)` is a
valid saved position. On creation the window opens at the saved position when
its 40-pixel title-bar strip overlaps some screen's work area by at least
100×40 pixels; otherwise, and for settings saved before positions existed, it
opens centred. Only the `workspace` window's geometry is persisted.

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

## DSH interaction notifications

A DSH question, approval or plan review that waits while the user looks
elsewhere raises a native notification. Approvals carry 允许一次 and 拒绝
buttons; the answer is given by DSH itself.

Each workbench window's DSH page reports its pending interactions through the
Client half of `@dsh-work/pet`. It reads `ctx.uiSession.sessionStatus` (each
session's `pendingInteraction`) and the main-view session (the session whose
`retainedBy.mainView` is positive) and reports only changes: an interaction key
that changes within 250 ms is reported once, a cleared interaction at once, and
the page resets its state when it unloads. Reports go from the frame to the
shell (`dsh-work/interaction`, `dsh-work/interaction-reset`) and on to
`DSHPageService`, which takes the window from the authenticated `/control`
surface. `internal/dshinteraction` keeps one page instance per window; an
instance belongs to the window that reported it first, and a reloaded page
replaces the old instance and withdraws its notifications.

A new interaction key notifies unless the owning window is focused, its DSH
page is visible and that page shows the waiting session. A focused window
does not suppress an interaction held by another window. The notification
preference is the interaction-required class, on by default. The notification
ID is the page instance plus the interaction key, so the bounded ring in
`internal/notifications` (1,024 IDs) sends each interaction once; a resolved
interaction withdraws its notification. Copy names only the tool for an
approval and nothing for a question or plan review: no reason, command,
arguments, paths or conversation titles.

An approval button sends `dsh-answer-approval` (30-second lifetime) to the
window that owns the page instance. The page answers only when the session
still waits on the same key and DSH marks it `answerable`, then calls
`PendingApproval.answer()` with a one-time outcome; DSH records its own
approval audit event. An answer already given on the card, a reloaded page or
a restarted Worker therefore produces no second result. Clicking the body opens
the conversation in the owning window through `dsh-open-session`; when the
owner is unknown, the workbench is shown. Questions and plan reviews have no
buttons. The Pet bubble and the notification alert independently, without
deduplication, as in dsh-tauri.

The answer goes through DSH's own `PendingApproval.answer()` because that is
the client path DSH and dsh-tauri use. Holding the approval in the host's
waterfall was rejected: the Gateway forwards waterfalls only to owner peers in
registration order, so the host could not hold one without taking the decision
from DSH. Polling page state was rejected for the reasons given under
[Desktop communication](#desktop-communication). Revisit when DSH publishes a
host-level approval API, or when a remote surface must answer approvals
without a workbench page; with no workbench page running, no interaction
notification is sent.

### Notification identity

Windows routes notification buttons through the toast activator registered for
the app's AppUserModelID. Wails reads that CLSID from
`HKCU\Software\Classes\AppUserModelId\<name>\CustomActivator`, generates a new
one when it cannot read it, and points `CLSID\{…}\LocalServer32` at the running
executable on every start. A changed CLSID breaks button callbacks, so the
daemon writes a fixed CLSID and display name before Wails starts
(`nativeui.EnsureToastActivator`). Identities follow the build tag:

| Build | AppUserModelID | Display name | CLSID |
|---|---|---|---|
| Release (`production`) | `dsh-work` | DSH Work | `{ca84fd06-433b-4c75-84ba-c74220f1f290}` |
| Development | `dsh-work-dev` | DSH Work Dev | `{2b99be78-db90-4fa9-9880-9221bd0a6cb8}` |

Stable, beta and nightly share the release identity because a channel is a
subscription inside one installation (see `docs/release-channels.md`); one
identity per channel would leave old notifications pointing at an unregistered
CLSID after a channel switch. Development builds use their own identity so they
do not rewrite the installed build's activation registration. They still share
the data directory and daemon pipe with an installed build, so the two cannot
run at the same time. The uninstaller removes the release AppUserModelID and
CLSID keys. When the daemon was not running, Windows starts the executable with
`-Embedding`, which opens the workbench; the button's action is not replayed.

A process started from inside another app's MSIX package context, such as the
Codex desktop app, writes `HKCU\Software\Classes` into that package's private
registry. Windows then accepts its notifications into history without showing
them. Start development builds from an ordinary terminal when checking
notifications.

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
   reopening reuses that window and page state. The first close shows a
   one-time notice; its acknowledgement is the `backgroundNoticeAcknowledged`
   setting. Explicit background Quit cancels owned work, verifies Worker/child
   cleanup, closes remaining UI, releases locks and exits the tray. Legacy close
   preferences cannot invoke it.
9. Quit has two entries. `HostService.Quit` stops at once and drains the IPC
   server on receipt; the CLI `stop` command and the installer use it.
   `HostService.RequestQuit` is the workbench menu's entry and, with the tray
   item, goes through a confirmation: the daemon asks `@dsh-work/pet` for
   `/__dshwork/quit-inspection` (running agents, queued messages or live jobs;
   armed `schedule` session activity), the same check as the official desktop
   app. Nothing to interrupt quits at once; otherwise a native Yes/No question
   names what would be lost, and the drain starts only after Yes. An inspection
   that fails or takes over 2 seconds counts as running work. Wails' Windows
   dialogs are the system message box, so the buttons follow the Windows
   display language; a custom task dialog was not worth its native code.
10. Pet size and position changes are applied through the Host. A failed native
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

dsh-work decides the window frame: the title bar and the DSH sidebar beneath it
are one surface, so the bar and the sidebar frame DSH's content together. The
frame is the theme's navigation fill, or the first quieter fill the theme's text
reads on, made opaque (`windowFrame` in `frontend/src/dsh-theme.ts`); the shell
paints its bar with it and sends the same fill to DSH as its sidebar colour (see
[Page bootstrap and host plugins](#page-bootstrap-and-host-plugins)). DSH never
decides the bar. On the startup page there is no sidebar, so the bar is
transparent over the page's own background and its text takes the first theme
colour that reads on the canvas (`pageFrame`). The bar's menu and window buttons
take hover and open fills that belong to the surface they sit on: the theme's
navigation hover and active fills only when the frame is the navigation fill,
otherwise the theme's general hover and selection. A fill is used only when the
bar's text reads on it and it stands out from the bar; otherwise the bar's
text is laid over it as a light tint, or the bar is darkened or lightened away
from the text where that tint would cost the text contrast. Bauhaus is the case
this guards: its navigation fill is dark, so the frame falls back to a light
fill, and the dark navigation hover would hide the menu labels. Menus and the
status line use the theme's dialog tokens (font, radius, border and shadow). While DSH
shows a modal dialog (`role="dialog"` with `aria-modal` or
`data-shortcut-modal`), `@dsh-work/shell` reports the colour and blur of its
mask, and the shell draws the same mask over the top bar so the window reads
as one modal layer. The mask covers the menus, and Alt or F10 return focus to
DSH. A click on it is passed to DSH's mask, so it closes the dialog when DSH's
own mask does. The window buttons sit above the mask, and Windows hit-tests the
caption area before the page, so dragging, Snap layouts and the window buttons
keep working. Upstream's Electron build leaves its caption unmasked; dsh-work
masks the bar so its menus cannot act behind a DSH dialog. The plugin watches
dialogs being mounted and the attributes above, not every style change, to stay
cheap while DSH streams output.

The shell owns every host command (restart, quit, update, Settings), so none is
reachable from DSH plugins, and the chrome and recovery entry stay usable when
DSH hangs or fails. Settings is a panel in the same document and shares that
trust. DSH loses its Electron caption layout and runs as a plain
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
| 文件 | 新会话*, 搜索会话*, 添加工作区* · 新终端*, 新浏览器* · 刷新, 重启 DSH · 关闭窗口, 退出 |
| 视图 | 左侧栏*, 右侧栏* · 放大, 缩小, 实际大小, 全屏 · 显示桌面宠物 |
| 设置 | 概览 · 通用, 通知, 宠物 · 运行环境, 配置, 插件 · 存储位置 · DSH 设置* |
| 帮助 | 文档, 键盘快捷键* · 桌面版反馈, DeepSeek 反馈 · 复制诊断信息, 打开调试窗口 · 检查更新 (or 有新版本可安装 / 更新中…), 关于 DSH Work |

Host items call trusted bindings. `ShellService` runs in the UI process: it
opens Settings at a section, and reads or sets Pet visibility on behalf of the
workbench shell, the only surface the daemon grants Pet controls.
Restart and quit keep the availability rules of the tray. 刷新 reloads only the DSH frame; the Worker keeps running. Zoom, full
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

Settings is a panel in the workbench, below the top bar and over the DSH frame,
which stays loaded and inert while the panel is open. The tray, the CLI
(`--ui <section>`) and the 设置 menu open it at a section; opening the
workbench without a section closes it, as do 返回工作台 at the top of its navigation, Esc and running
a DSH command from the menu. The daemon accepts management calls only from the
workbench window, and the DSH frame cannot reach them (see
[Desktop communication](#desktop-communication)). The tray belongs to the daemon and can open Settings or the workbench, restart DSH,
and explicitly stop the background. Pet and native notification adapters remain
in the daemon, so closing the UI preserves them.

Assign the initial window URL before creating the window to avoid a second
initial navigation. On Windows, child launch uses CREATE_NO_WINDOW to
suppress the console; STARTF_USESHOWWINDOW/SW_HIDE would override the first
native ShowWindow request and must not be used for the UI launch.

## Logging and diagnostics

Logging has three layers with one dependency direction:
`redact` ← `logging` ← `diagnostics` ← daemon services ← `desktopclient` ←
frontend.

| Layer | Module | Owns | Does not |
|---|---|---|---|
| Record | `internal/redact`, `internal/logging` | one JSONL file per process, rotation and retention, secret removal at write time, crash stacks | read logs or know the UI |
| Diagnose | `internal/diagnostics` (daemon) | problem list, Markdown issue report, diagnostics bundle, anonymization on export | write logs or depend on Wails |
| Present | frontend (Settings › Diagnostics, Help menu, failed-startup panel, About) | problems with actions, copy report, open GitHub, export | parse log files |

**Files.** Logs live in the local, non-migrating log root
(`%LOCALAPPDATA%\dsh-work\logs` on Windows, `~/Library/Logs/dsh-work` on macOS,
`${XDG_STATE_HOME:-~/.local/state}/dsh-work/logs` on Linux), directory 0700 and
files 0600. Each process writes only its own file, and only after it holds its
single-instance lock; earlier records are buffered and, if the process exits
first, written to `<proc>-early.log`.

| File | Writer | Content | Size × backups | Max age |
|---|---|---|---|---|
| `daemon.log` | daemon | Host, lifecycle, update, storage, plugins, notifications, IPC, Wails | 5 MB × 5 | 14 days |
| `worker.log` | daemon | DSH Worker stdout (INFO) and stderr (WARN), one record per line | 10 MB × 3 | 7 days |
| `ui.log` | UI client | UI process events and batched frontend reports | 2 MB × 3 | 14 days |
| `crash/` | each process | `runtime/debug.SetCrashOutput` target, rotated to a timestamped file on the next start | newest 10 | 30 days |

Backups are gzip-compressed; the worst case is about 35 MB. The CLI keeps its
console output and writes no log file; operations it sends to the daemon are
logged there.

**Records.** Each line is a `slog` JSON object with `time`, `level`, `msg` (a
stable English phrase), `proc`, `run` (per-process-run id) and `mod` (a fixed
module vocabulary), plus `gen`, `op` (the UI operation id, shared across
processes) and `code` when they apply. ERROR means an operation failed or a
feature is unavailable, WARN a degraded continuation, INFO state transitions and
user-initiated operations, DEBUG step detail. Every user-visible failure carries
a `lifecycle.ErrorCode`; a WARN or ERROR record with a `code` is what the problem
list reads. Packages obtain loggers through `logging.Module(name)` at the call
site; `logging.Init` also routes the standard `log` package and Wails system
messages into the same file. The default level is INFO. The Host preference
`verboseLogging` switches both processes to DEBUG at once through a
`slog.LevelVar`; `DSH_WORK_LOG_LEVEL` overrides it for development, and
development builds, desktop probes or `DSH_WORK_LOG_STDERR=1` also write text to
stderr. The frontend reports through `LogService.Write` in batches, capped at
50 entries a minute, and records uncaught script errors as `UI_SCRIPT_ERROR`.

**Diagnostics.** `DiagnosticsService` serves only the trusted workbench window.
Problems come from the last 7 days of `daemon.log` and `ui.log` including
backups; records with the same code within 10 minutes merge into one counted
problem, newest first, at most 20. Log files are read into memory and closed
before parsing so a concurrent rotation is never blocked, and an unreadable
backup is skipped. The report is Markdown of at most 12 KB: the problem, an
environment table, related records around it and the Worker stderr tail of its
generation. The bundle is a zip of at most 24 MB with a manifest, the
environment, all logs, crash files and recent DSH startup logs; when it is too
large the oldest Worker backups are dropped first and listed as omitted.
Problem messages, reports and bundles replace the home directory with `~` and
the host name with `<host>`.

**Dependency decision.** Records use `log/slog` from the standard library.
Rotation uses `gopkg.in/natefinch/lumberjack.v2`: it is the most widely used
size-rotating `io.Writer` with backup count, age and gzip, and its
one-writer-per-file assumption matches one file per process.
`github.com/DeRuina/timberjack` was rejected because time-based rotation is not
needed; a shared multi-process file was rejected because neither library
supports concurrent writers. Revisit if logs need time-based rotation, a
process must share another process's file, or lumberjack stops being
maintained.

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
