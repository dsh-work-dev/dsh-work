# Architecture

## System context

```text
User
  |
  v
Desktop UI client (Go + Wails)
  |-- trusted startup and Settings WebViews, application menu
  |-- Worker WebView --> Wails byte streams
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
| `internal/dshadapter` | exact-version launch, readiness, profile and plugin command grammar |
| `internal/dshmanager` | runtime catalog, profiles, version snapshots and serialized recovery state |
| `internal/workspacecontext` | per-generation DSH Workspace context |
| `internal/workerchannel` | per-generation channel, authentication cookies and activity lifetime |
| `internal/workeripc` | current-user authenticated OS pipe carrying upstream HTTP bytes |
| `internal/desktopbridge` | resource delivery, Fetch and WebSocket over bounded Wails byte streams |
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
the manager lock. These local endpoints open no TCP or UDP listener.

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

The startup window (`workspace`) and Settings have fixed trusted roles.
The DSH window (`worker`) has an immutable role. Native-stamped window identity
selects resource handlers and denies its management runtime requests. Worker
service-worker registration is blocked to protect the shared WebView origin.
Resources use the native asset handler and daemon Worker forwarding; Fetch and
WebSocket bodies use Wails Streams with 64 KiB chunks, upload acknowledgements
and download credits. The daemon forwards traffic to the generation pipe.
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
7. Readiness establishes the current Worker. Verified version inputs advance
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

## Native windows and menus

The UI client installs the application menu on both workbench windows. Settings
has its own locale-aware title and no application menu. Native menu actions use
the daemon services; the tray belongs to the daemon and can open Settings or
the workbench, restart DSH, and explicitly stop the background. Pet and native
notification adapters remain in the daemon, so closing the UI preserves them.

Assign the initial Worker/Settings URL before creating the window to avoid a
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
