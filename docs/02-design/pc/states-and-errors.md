# Runtime states and errors

## Host state machine

```text
Stopped
  └─ launch → Checking
Checking
  ├─ compatible → Starting
  ├─ incompatible → Failed
  └─ cancel → Stopping
Starting
  ├─ ready signal → Ready
  ├─ timeout / exit → Failed
  └─ cancel → Stopping
Ready
  ├─ task begins → Busy
  ├─ worker exits → Failed
  └─ quit / restart → Stopping
Busy
  ├─ approval needed → WaitingForApproval
  ├─ task ends → Ready
  ├─ worker exits → Failed
  └─ quit → Stopping
WaitingForApproval
  ├─ approve → Busy
  ├─ deny / close → Busy or Ready
  ├─ request expires → Busy or Ready
  └─ quit → Stopping
Failed
  ├─ retry → Checking
  ├─ safe mode → SafeModeStarting
  └─ quit → Stopping
SafeModeStarting
  ├─ ready → SafeMode
  ├─ timeout / exit → Failed
  └─ quit → Stopping
SafeMode
  ├─ retry normal → Stopping → Checking
  └─ quit → Stopping
Stopping
  ├─ process tree gone → Stopped
  └─ bounded fallback complete → Stopped with diagnostic event
```

Only the state machine owner may change Host state. UI components observe state and dispatch commands; they do not infer state from log text.

## Browser task states

`Queued → Preparing → Running ↔ WaitingForApproval → Cancelling → Succeeded | Failed | Denied | Cancelled`

Every task reaches exactly one terminal state. A late browser event after a terminal state is logged and ignored.

## Stable error taxonomy

| Code | Meaning | Default user action |
|---|---|---|
| `CFG_INVALID` | Host or runtime configuration is invalid | Open the affected setting |
| `RUNTIME_MISSING` | Compatible DSH runtime cannot be found | Choose a supported runtime action |
| `RUNTIME_UNSUPPORTED` | Detected version is outside the supported range | Change or update the runtime |
| `WORKER_START_FAILED` | Worker process could not be created | View details, then retry |
| `WORKER_READY_TIMEOUT` | Worker did not satisfy readiness in time | Retry or open Diagnostics |
| `WORKER_EXITED` | Worker exited after it was started | Retry; safe mode after repetition |
| `WORKER_BIND_FAILED` | Worker could not bind its local endpoint | Retry after cleanup |
| `WORKER_PROTOCOL_INVALID` | Readiness or protocol response was not valid | Check compatibility |
| `WORKER_ACCESS_REJECTED` | Worker HTTP or WebSocket access failed origin or session policy | Reload the trusted workspace or restart Worker |
| `PROCESS_CLEANUP_FAILED` | At least one managed process may remain | Show affected process details and retry cleanup |
| `NAVIGATION_BLOCKED` | Embedded content attempted an untrusted navigation | Offer system browser when safe |
| `TOOL_REQUEST_INVALID` | Browser tool request failed schema validation | Return structured failure to DSH |
| `CAPABILITY_DENIED` | User denied or policy blocked an operation | Continue without the capability |
| `APPROVAL_EXPIRED` | Approval was not answered in time | Return expired result |
| `TOOL_POLICY_UNCLAIMED` | A dsh-work Tool was not classified by the dsh-work DSH pre-execute policy | Deny the operation and report an integration fault |
| `BROWSER_START_FAILED` | Managed browser session could not start | Retry or inspect browser setup |
| `BROWSER_ACTION_FAILED` | Page operation failed | Show step and safe retry options |
| `TASK_CANCELLED` | User cancelled the task | Return cancelled result |
| `DIAGNOSTIC_EXPORT_FAILED` | Local report could not be written | Choose another destination |

## Error data contract

Each internal error maps to:

```text
code            stable public error code
summary         localisable user-facing sentence
operation       startup, shutdown, browser, permission, recovery, export
correlation_id  identifier shared by related structured events
retryable       whether repeating without configuration change is reasonable
safe_actions    ordered commands the UI may offer
details         redacted technical context
cause           wrapped internal error, never directly rendered
```

Unknown errors map to an operation-specific fallback code while preserving the internal cause in redacted diagnostics.
