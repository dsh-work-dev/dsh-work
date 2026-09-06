# Architecture

## System context

```text
User
  |
  v
dsh-work Host (Go + Wails)
  |-- trusted startup and Settings WebViews
  |-- Workspace WebView --> trusted loopback gateway --> DSH Worker
  |-- lifecycle and runtime manager --> Supervisor --> platform adapter
  |-- settings and notification policy --> native desktop adapters
  `-- DSH adapter --> versioned DSH commands and readiness
```

The Host is the native authority boundary. The DSH Worker and rendered Workspace
content cannot grant themselves Host capabilities.

## Implemented modules

| Module | Responsibility |
|---|---|
| `internal/app` | composition, service binding, process lock and Host commands |
| `internal/lifecycle` | window policy, quit flow and lifecycle value types |
| `internal/supervisor` | platform-neutral Worker process contract |
| `internal/platform` | production platform selection and native adapters |
| `internal/platform/windows` | process creation, Job Object cleanup, atomic file replacement and explicit runtime installation |
| `internal/dshadapter` | exact-version launch, readiness, profile and plugin command grammar |
| `internal/dshmanager` | runtime catalog, DSH data directories, profiles and transactional Run-context state |
| `internal/workspacecontext` | per-generation DSH Workspace context |
| `internal/workergateway` | trusted-origin HTTP and WebSocket access to the Worker |
| `internal/settings` | versioned Host preferences and persistence contract |
| `internal/notifications` | notification vocabulary, preference evaluation, routing and bounded deduplication |
| `internal/nativeui` | native menu localization and the optional Wails notification adapter |
| `frontend` | trusted Host state projection and interaction |

## Dependency direction

Lifecycle and policy modules depend on values and narrow interfaces. Wails, DSH,
filesystem and operating-system primitives implement those interfaces at the
edge. The frontend uses the generated allowlisted Host bindings and has no
direct process or filesystem authority. Logs and UI projections observe state;
they do not define lifecycle truth.

## Runtime invariants

1. The Host obtains the single-instance and manager locks before exposing
   mutable desktop state.
2. Startup resolves a complete local Run context without network acquisition.
3. The Worker enters its platform process boundary before it can run.
4. A fresh generation ID scopes startup, readiness, gateway and Workspace
   events; obsolete-generation events are ignored.
5. Workspace navigation occurs only after active readiness and gateway checks.
6. Run-context switching stops and verifies the old generation before starting
   a candidate. Candidate and previous Worker generations never overlap.
7. A successful candidate becomes current and known-good atomically. A failed
   candidate is discarded before bounded rollback.
8. Explicit Quit cancels owned work and verifies process cleanup before the Host
   reports completion.

## Platform boundary

The shared Supervisor contract compiles for Windows, macOS and Linux. Windows
has the production process-ownership implementation. Other targets must not be
described as production-equivalent until native ownership and packaging tests
exist.
