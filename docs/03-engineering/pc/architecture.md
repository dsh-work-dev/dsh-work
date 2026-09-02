# PC architecture

## System context

```text
User
  │
  ▼
Work desktop Host (Go + native WebView)
  ├── Manager window ─────────────── trusted Work management UI
  ├── Workspace window ───────────── DSH Web UI through Worker gateway
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
- The Manager window is Host-owned; the Workspace window is DSH-owned and
  receives only the native Work menu, not injected Host markup.
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
| `platform/windows` | Job Object, tray, credential and path adapters | product policy |
| `platform/darwin` | guardian, process group, tray, credential and path adapters | product policy |
| `platform/linux` | guardian, process group／prctl, tray, credential and path adapters | product policy |
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
2. The runtime manager resolves the selected compatible local DSH runtime, DSH home and profile without network access.
3. Supervisor prepares a Work-owned profile overlay, private IPC endpoint and loopback port candidate.
4. The selected platform adapter creates the Worker inside its managed process boundary.
5. DSH adapter reads structured process events, validates the announced origin, then performs an active readiness probe.
6. Worker gateway establishes the per-generation trusted application session and validates upstream HTTP／WebSocket behaviour.
7. Lifecycle enters `Ready`; only then may the Workspace window navigate through the gateway. The Manager window remains on trusted Work content.
8. Work Tool classifier returns `allow`, `ask` or `deny`; `ask` reuses DSH's approval service and official UI.
9. After DSH policy permits execution, the Work plugin sends the typed operation over private IPC.
10. Host hard-policy guard revalidates schema, browser connection and assigned tab before Browser service acts.
11. Result, DSH call ID and redacted Host execution event share a correlation ID.
12. Quit cancels operations, detaches browser control, requests Worker shutdown and verifies the process tree is gone.

## Concurrency model

- One lifecycle owner serialises Host state transitions.
- Each Worker generation has an immutable generation ID; events from old generations are ignored.
- Each browser task owns a cancellable context, explicit tab assignment and exactly one terminal result.
- DSH owns one-shot approval ordering; the Host revalidates after approval and before browser action.
- Unbounded goroutines, channels, log buffers and retry loops are prohibited.

## Failure containment

- Worker crash does not terminate the Host.
- Browser or extension disconnect fails its task but does not change Worker state or close the user's browser.
- UI refresh does not restart the Worker.
- Audit write failure blocks high-risk action and reports a diagnostic event.
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
