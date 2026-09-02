# PC architecture

## System context

```text
User
  │
  ▼
Work desktop Host (Go + native WebView)
  ├── Settings window ────────────── flat Work settings + DSH manager
  ├── Workspace window ───────────── DSH Web UI through Worker gateway
  ├── Settings ───────────────────── versioned Work preferences and close policy
  ├── Notification policy ────────── preferences, routing and deduplication
  ├── DSH event bridge ◄──────────── structured DSH session events
  ├── Native notifier ────────────── OS desktop delivery adapters
  ├── Native application menu + tray ─ Wails composition-edge lifecycle controls
  ├── Worker gateway ───────────► DSH Web UI on loopback
  ├── Supervisor ───────────────► DSH Worker process
  │                                  ├── Work Tool classifier
  │                                  ├── DSH approval UI/service
  │                                  └── Work tool plugin
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
  DSH home/profile; Work does not embed the DSH Web UI or mutate profiles
  during GUI startup.
- The Settings window is Host-owned and contains a flat, shallow rail: a
  read-only Overview, top-level General and Notifications pages, and DSH resource pages
  for profiles/plugins, runtimes and homes. The application menu exposes only
  Settings and Help (with update check and About commands). The Workspace
  window is DSH-owned and receives no injected Host markup; application menu
  and tray handlers remain at the Host composition edge.
- DSH owns `ui-theme.preference` in the selected DSH home. `dshmanager`
  projects that read-only value in its snapshot; Work consumes it for trusted
  surfaces and never writes a duplicate appearance setting. `system` is
  resolved by the platform/webview through the operating-system preference;
  the Settings window observes DSH changes while it remains open.
- Work owns one persisted `locale` preference for English, Simplified Chinese
  and Japanese. Settings writes it; a typed application event updates the
  startup surface, Settings surface, native application menu and tray. The
  DSH workspace is not translated or modified by Work.
- Work owns desktop notification preferences and delivery policy. DSH owns
  contextual in-page notices. The first notification slice has a flat
  top-level Settings route and no persistent notification panel.
- Host startup output is served through a bounded, redacted projection of the
  supervisor diagnostics. The frontend may display and copy it, but it never
  receives an unbounded process stream or raw process metadata.
- Work-owned DSH plugin attached to the selected profile through a supported
  profile or patch seam.
- Private Host–plugin IPC; loopback HTTP only for the embedded DSH Web UI.
- Browser adapters for Chrome personal-browser auto-connect, signed extension／native messaging compatibility and managed-profile fallback.

Wails and DSH versions must be pinned. Both integrations are isolated because their public surfaces may evolve.

## Modules

| Module | Responsibility | Must not know |
|---|---|---|
| `app` | composition root, lifecycle wiring | DSH CLI grammar, browser protocol details |
| `lifecycle` | Host state machine and commands | UI toolkit, operating-system calls |
| `settings` | versioned global Work preferences and persistence contract | Wails, window handles, DSH profile data |
| `notifications` | notification event vocabulary, preferences, routing and deduplication | Wails, DSH DOM, native handles |
| `notificationbridge` | structured DSH event identity, source context and verified focus targets | notification visuals and policy decisions |
| `platform notification adapters` | native desktop notification delivery | product event classification |
| `supervisor` | process ownership, readiness, restart, cleanup | DSH plugin internals, approval UI |
| `dshadapter` | supported versions, command construction, readiness parsing | tray, browser driver details |
| `dshmanager` | explicit DSH runtime catalog, home/profile selection and CLI-backed management; delegates profile/plugin semantics to DSH | Wails, lifecycle state, process handles |
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
4. Work browser Tools must pass their Work-owned DSH pre-execute classifier before their bodies may request a Host operation.
5. Storage implementations own migration; callers use versioned repositories.
6. Logging is an observer, never the source of lifecycle truth.

## Primary runtime sequence

1. The Host obtains the single-instance lock and opens trusted UI.
2. The runtime manager resolves the selected compatible local DSH runtime, DSH home and profile without network access; the settings module loads the versioned Work close policy, locale and notification preferences with tray-safe, language and notification defaults.
3. Supervisor prepares a Work-owned profile overlay, private IPC endpoint and loopback port candidate.
4. The selected platform adapter creates the Worker inside its managed process boundary.
5. DSH adapter reads structured process events, validates the announced origin, then performs an active readiness probe.
6. Worker gateway establishes the per-generation trusted application session and validates upstream HTTP／WebSocket behaviour.
7. Lifecycle enters `Ready`; only then may the Workspace window navigate through the gateway. The Settings window remains on trusted Work content.
8. The notification bridge accepts only structured, versioned DSH events after its generation handshake.
9. Notification policy evaluates source class, persisted preferences, window state and deduplication before the native notifier delivers an eligible event.
10. Work Tool classifier returns `allow`, `ask` or `deny`; `ask` reuses DSH's approval service and official UI.
11. After DSH policy permits execution, the Work plugin sends the typed operation over private IPC.
12. Host hard-policy guard revalidates schema, browser connection and assigned tab before Browser service acts.
13. Result, DSH call ID and redacted Host execution event share a correlation ID.
14. Quit cancels operations, detaches browser control, requests Worker shutdown and verifies the process tree is gone.

Window close is a separate composition-edge policy: the shared WindowLedger
tracks visible Work windows and decides between hide-to-tray and full quit. The
Wails SystemTray API realizes that decision on each supported desktop platform;
there are no fake macOS or Linux production tray adapters.

## Concurrency model

- One lifecycle owner serialises Host state transitions.
- Each Worker generation has an immutable generation ID; events from old generations are ignored.
- Each browser task owns a cancellable context, explicit tab assignment and exactly one terminal result.
- DSH owns one-shot approval ordering; the Host revalidates after approval and before browser action.
- Notification deduplication is bounded to the Work session and keyed by a structured event identity; reconnects cannot replay a desktop delivery.
- Unbounded goroutines, channels, log buffers and retry loops are prohibited.

## Failure containment

- Worker crash does not terminate the Host.
- Browser or extension disconnect fails its task but does not change Worker state or close the user's browser.
- UI refresh does not restart the Worker.
- Audit write failure blocks high-risk action and reports a diagnostic event.
- Native notification delivery failure does not change the source event outcome; it produces a bounded diagnostic result.
- Invalid Worker or page data is rejected at the edge before entering domain state.

## Planned repository layout

```text
cmd/work/
cmd/dsh-work/
internal/app/
internal/lifecycle/
internal/supervisor/
internal/dshadapter/
internal/dshmanager/
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
cmd/work-process-guardian/
cmd/work-browser-bridge/
frontend/
browser-extension/
docs/
```

The layout is a target for implementation, not permission to create empty abstraction packages before a vertical slice needs them.
