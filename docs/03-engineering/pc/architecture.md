# PC architecture

## System context

```text
User
  │
  ▼
dsh-work desktop Host (Go + native WebView)
  ├── Settings window ────────────── flat dsh-work settings + DSH manager
  ├── Workspace window ───────────── DSH Web UI through Worker gateway
  ├── Settings ───────────────────── versioned dsh-work preferences and close policy
  ├── Notification policy ────────── preferences, routing and deduplication
  ├── DSH event bridge ◄──────────── structured DSH session events
  ├── Native notifier ────────────── OS desktop delivery adapters
  ├── Native application menu + tray ─ Wails composition-edge lifecycle controls
  ├── Worker gateway ───────────► DSH Web UI on loopback
  ├── Supervisor ───────────────► DSH Worker process
  │                                  ├── dsh-work Tool classifier
  │                                  ├── DSH approval UI/service
  │                                  └── dsh-work tool plugin
  ├── Host hard-policy guard ◄──── private authenticated IPC
  ├── Browser service ────────────► BrowserConnection adapter
  │                                  ├── Chrome personal auto-connect
  │                                  ├── native helper + signed extension
  │                                  └── managed-profile fallback
  ├── Audit / diagnostics
  └── platform adapter
      ├── Windows Job Object
      ├── macOS guardian + process group
      └── Linux guardian + process group/prctl
```

The Host is the authority boundary. The Worker may request a capability but cannot grant it. The embedded Web UI and controlled pages are untrusted renderers.

## Technology baseline

- Go backend hosted by Wails 3.
- Native platform WebView for the application window.
- TypeScript frontend with framework choice isolated from Host contracts.
- DSH launched as an out-of-process Worker.
- `dsh-work` CLI and runtime manager resolve an installed DSH runtime plus a
  DSH data-directory/profile Run context; dsh-work does not embed the DSH Web UI
  or mutate profiles during GUI startup. `DSH home` remains the
  upstream/internal name for the same data directory.
- The Settings window is Host-owned and contains a flat, shallow rail: a
  read-only Overview, top-level General and Notifications pages, a profile
  resource page with selected-profile plugin actions, and resource pages for
  runtimes and DSH data directories. The application menu exposes only
  Settings and Help (with update check and About commands). The Workspace
  window is DSH-owned and receives no injected Host markup; application menu
  and tray handlers remain at the Host composition edge.
- DSH owns `ui-theme.preference` in the selected DSH data directory. `dshmanager`
  projects that read-only value in its snapshot; dsh-work consumes it for trusted
  surfaces and never writes a duplicate appearance setting. `system` is
  resolved by the platform/webview through the operating-system preference;
  the Settings window observes DSH changes while it remains open.
- dsh-work owns one persisted `locale` preference for English, Simplified Chinese
  and Japanese. Settings writes it; a typed application event updates the
  startup surface, Settings surface, native application menu and tray. The
  DSH workspace is not translated or modified by dsh-work.
- dsh-work owns desktop notification preferences and delivery policy. DSH owns
  contextual in-page notices. The first notification slice has a flat
  top-level Settings route and no persistent notification panel.
- Host startup output is served through a bounded, redacted projection of the
  supervisor diagnostics. The frontend may display and copy it, but it never
  receives an unbounded process stream or raw process metadata.
- dsh-work-owned DSH plugin attached to the current profile through a supported
  profile or patch seam; non-current profile plugin state is read-only.
- Private Host–plugin IPC; loopback HTTP only for the embedded DSH Web UI.
- Browser adapters for Chrome personal-browser auto-connect, signed extension／native messaging compatibility and managed-profile fallback.

Wails and DSH versions must be pinned. Both integrations are isolated because their public surfaces may evolve.

## Modules

| Module | Responsibility | Must not know |
|---|---|---|
| `app` | composition root, lifecycle wiring | DSH CLI grammar, browser protocol details |
| `lifecycle` | Host state machine and commands | UI toolkit, operating-system calls |
| `settings` | versioned global dsh-work preferences and persistence contract | Wails, window handles, DSH profile data |
| `notifications` | notification event vocabulary, preferences, routing and deduplication | Wails, DSH DOM, native handles |
| `notificationbridge` | structured DSH event identity, source context and verified focus targets | notification visuals and policy decisions |
| `platform notification adapters` | native desktop notification delivery | product event classification |
| `supervisor` | process ownership, readiness, restart, cleanup | DSH plugin internals, approval UI |
| `dshadapter` | supported versions, command construction, readiness parsing | tray, browser driver details |
| `dshmanager` | explicit DSH runtime catalog, Run context selection, current-profile mutation guard and CLI-backed management; delegates profile/plugin semantics to DSH | Wails, process handles, Workspace selection |
| `workspacecontext` | resolves or resumes a DSH-owned Workspace for an explicit session and exposes its current context | runtime catalog, profile/plugin composition, Wails window handles |
| `workergateway` | trusted-origin HTTP／WebSocket access to the Worker | DSH command grammar, approval policy |
| `toolbridge` | typed Host–DSH request/result protocol | visual UI components |
| `browserpolicy` | DSH Tool classification plus Host hard-deny revalidation | UI layout and raw browser transport |
| `browser` | extension connection, tab assignments and atomic browser operations | agent prompts, Host rendering |
| `browserbridge` | browser native-messaging registration and helper protocol | DSH and product policy |
| `audit` | structured security events and redaction | UI layout |
| `diagnostics` | health snapshot and export assembly | raw secret values |
| `platform/windows` | Job Object, credential and path adapters | product policy |
| `platform/darwin` | guardian, process group, credential and path adapters | product policy |
| `platform/linux` | guardian, process group／prctl, credential and path adapters | product policy |
| `frontend` | trusted Host views and state projection | direct process or filesystem access |

## Dependency rules

1. Domain state and policy packages depend only on interfaces and value types.
2. Wails, DSH, browser-adapter and operating-system packages implement interfaces at the edge.
3. Frontend code calls generated, narrow Host commands; it never invokes arbitrary Go methods.
4. dsh-work browser Tools must pass their dsh-work-owned DSH pre-execute classifier before their bodies may request a Host operation.
5. Storage implementations own migration; callers use versioned repositories.
6. Logging is an observer, never the source of lifecycle truth.

## Run context and Workspace context

The Run context is the smallest stable selection needed to define one DSH
execution environment:

```text
DSH runtime identity + DSH data-directory identity + profile name
```

The Configured Run context is the versioned triple persisted by dsh-work. The
current Run context is the triple used by the current `Ready` Worker
generation. The last context that reached `Ready` is the known-good Run
context. None of these contain a Workspace path or identifier. The
`workspacecontext` Module consumes DSH's Workspace Seam or an explicit session
action and keeps the resulting Workspace context in the per-generation Launch
context. A DSH Adapter Implementation may pass that context to the Worker,
but it must not write it into the Configured Run context. If no Workspace is
selected, the session presents DSH's selection/creation surface rather than
falling back to the process current directory.

## Primary runtime sequence

1. The Host obtains the single-instance lock and opens trusted UI.
2. The runtime manager resolves the Configured Run context's compatible local DSH runtime, DSH data directory and profile without network access; the settings module loads the versioned dsh-work close policy, locale and notification preferences with tray-safe, language and notification defaults.
3. The Workspace context Module obtains a current or explicitly requested DSH Workspace through the DSH Workspace seam. If none is available, the session enters Workspace selection instead of inferring one from a process directory.
4. Supervisor prepares a dsh-work-owned profile overlay, private IPC endpoint and loopback port candidate, carrying the Workspace context only in the per-generation launch context when the DSH adapter requires it.
5. The selected platform adapter creates the Worker inside its managed process boundary.
6. DSH adapter reads structured process events, validates the announced origin, then performs an active readiness probe.
7. Worker gateway establishes the per-generation trusted application session and validates upstream HTTP／WebSocket behaviour.
8. Lifecycle enters `Ready`; only then may the Workspace window navigate through the gateway. The Settings window remains on trusted dsh-work content and shows Workspace context read-only.
9. The notification bridge accepts only structured, versioned DSH events after its generation handshake.
10. Notification policy evaluates source class, persisted preferences, window state and deduplication before the native notifier delivers an eligible event.
11. dsh-work Tool classifier returns `allow`, `ask` or `deny`; `ask` reuses DSH's approval service and official UI.
12. After DSH policy permits execution, the dsh-work plugin sends the typed operation over private IPC.
13. Host hard-policy guard revalidates schema, browser connection and assigned tab before Browser service acts.
14. Result, DSH call ID and redacted Host execution event share a correlation ID.
15. Quit cancels operations, detaches browser control, requests Worker shutdown and verifies the process tree is gone.

## Atomic Run-context switching

The lifecycle owner treats the runtime, DSH data-directory identity and profile
reference as one replacement unit:

1. Resolve and validate the complete candidate Run context.
2. Capture the current known-good context and enter `Stopping`; reject new
   context and plugin mutations for the duration of the operation.
3. Stop the current Worker generation and verify its managed process boundary
   is clean before starting a new generation.
4. Launch the candidate with a fresh generation ID and resolve its Workspace
   context independently.
5. Commit the candidate as current and persist it as Configured only after the
   Worker and gateway reach `Ready`.
6. On compatibility, startup or readiness failure, discard the candidate and
   repeat the same bounded sequence for the known-good context. The candidate
   never becomes current, and two Worker generations never run concurrently.

The rollback context is retained until the replacement generation is ready.
If rollback also fails, dsh-work publishes one terminal failure with retryable
recovery actions and does not silently select the failed candidate.

Window close is a separate composition-edge policy: the shared WindowLedger
tracks visible dsh-work windows and decides between hide-to-tray and full quit. The
Wails SystemTray API realizes that decision on each supported desktop platform;
there are no fake macOS or Linux production tray adapters.

## Concurrency model

- One lifecycle owner serialises Host state transitions.
- Each Worker generation has an immutable generation ID; events from old generations are ignored.
- Each browser task owns a cancellable context, explicit tab assignment and exactly one terminal result.
- DSH owns one-shot approval ordering; the Host revalidates after approval and before browser action.
- Notification deduplication is bounded to the dsh-work session and keyed by a structured event identity; reconnects cannot replay a desktop delivery.
- Run-context switches are serialised; a candidate is not current until
  `Ready`, and plugin mutations are accepted only for the current Run
  context's profile.
- Unbounded goroutines, channels, log buffers and retry loops are prohibited.

## Failure containment

- Worker crash does not terminate the Host.
- Browser or extension disconnect fails its task but does not change Worker state or close the user's browser.
- UI refresh does not restart the Worker.
- Audit write failure blocks high-risk action and reports a diagnostic event.
- Native notification delivery failure does not change the source event outcome; it produces a bounded diagnostic result.
- Invalid Worker or page data is rejected at the edge before entering domain state.
- A failed Run-context switch restores the last known-good context or exposes a
  terminal recovery state without leaving an uncommitted candidate active.

## Planned repository layout

```text
cmd/dsh-work/
internal/app/
internal/lifecycle/
internal/supervisor/
internal/dshadapter/
internal/dshmanager/
internal/workspacecontext/
internal/workergateway/
internal/notifications/
internal/notificationbridge/
internal/toolbridge/
internal/browserpolicy/
internal/browser/
internal/browserbridge/
internal/audit/
internal/diagnostics/
internal/platform/windows/
internal/platform/darwin/
internal/platform/linux/
cmd/dsh-work-process-guardian/
cmd/dsh-work-browser-bridge/
frontend/
browser-extension/
docs/
```

The layout is a target for implementation, not permission to create empty abstraction packages before a vertical slice needs them.
