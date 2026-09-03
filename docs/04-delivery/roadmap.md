# PC implementation roadmap

The roadmap uses end-to-end slices. Each milestone must leave the application in a testable state and satisfy its exit gate before dependent work begins.

The detailed execution sequence for M0 through M2 is defined in the [PC foundation and first-run plan](pc-foundation-plan.md). Development proves the product on Windows first, while shared Interfaces and contracts are designed for Windows, macOS and Linux from the start. DSH visual convergence follows the functional first-run path in the same main sequence.

## M0 — Repository and contract foundation

Deliver:

- Go module, Wails 3 application and TypeScript frontend skeleton;
- pinned toolchain and dependency versions;
- automated formatting, unit test, build and documentation checks;
- minimum lifecycle and error value types;
- narrow Host command and event projection;
- `dsh-work` runtime-manager Interface for a Launch target (exact runtime, DSH
  data directory and profile) plus profile-scoped plugin operations, with the
  initial CLI/catalog implementation;
- test-only Worker fake for deterministic shell-state tests.

Exit gate: clean Windows, macOS and Linux checkouts build the trusted shell; the current development platform launches it, and CI runs deterministic minimum lifecycle tests.

## M1 — Real DSH tracer-bullet slice

Deliver:

- explicitly selected runtime discovery and pinned compatibility for the initial
  DSH baseline;
- `dsh-work` CLI path for registering/installing catalog runtimes and DSH data
  directories, selecting profiles, and delegating plugin operations to DSH per selected
  profile;
- first concrete DSH and Windows process Adapters behind the planned Seams;
- explicit loopback port and active readiness validation;
- embedded trusted-origin navigation to the selected external DSH workspace;
- graceful stop and initial child cleanup;
- stable missing-runtime, incompatible-version, early-exit and timeout failures.

Exit gate: on the primary Windows development environment, Work launches the
selected runtime/profile pair, displays the external DSH page only after
readiness, exits without manual process or port cleanup, and repeats the path
reliably. The selection seam does not require the Host to embed or install DSH.

## M2 — Windows desktop lifecycle and foundation UI

Deliver:

- single-instance, window, tray, open, close and quit behaviour, including the
  persisted default close-to-tray policy and explicit full quit;
- separate trusted Settings and external DSH Workspace windows, with a native
  application menu containing Settings and Help, plus nested DSH management
  inside the Settings window;
- complete Host lifecycle state machine with generation IDs;
- bounded startup, shutdown, cancellation and retry;
- structured logging, redaction and correlation IDs;
- three-platform Supervisor contract and shared hostile-child fixtures;
- hardened Windows Job Object adapter;
- graceful and forced cleanup;
- stable startup and process error mapping;
- minimum startup, failure and accessibility surfaces;
- versioned Work notification preferences and native desktop delivery for Work
  lifecycle and error events;
- DSH-aligned visual convergence after the real first-run path is stable.

Exit gate: on Windows, Work starts the pinned DSH Web profile from a clean environment, handles all defined fixture failure modes, and leaves no managed descendants after normal or forced Host exit; lifecycle tests reject stale generations and duplicate terminal results. The shared Interface contains no Windows-only types, and the documented macOS and Linux Implementations can satisfy it without changing callers.

## M3 — Tool bridge and permission slice

Deliver:

- Work-owned DSH plugin attached to the selected profile;
- private authenticated IPC handshake;
- versioned request and result schemas;
- exhaustive Work Tool `allow`／`ask`／`deny` classifier;
- DSH official one-shot approval integration without a duplicate Host prompt;
- browser-connection grant and Host hard-policy state;
- correlated DSH approval and Host execution events with redaction;
- structured DSH notification event bridge for action-required, completed,
  error and lifecycle events, with preference-aware desktop routing;

Exit gate: on Windows, every Work Tool variant has a deterministic classifier result; high-impact calls execute only after DSH `allowed-once`, while altered, replayed, unclassified and stale-generation requests are rejected. Notification fixtures prove preference filtering, foreground/background routing, deduplication and safe focus actions. The wire and policy contracts remain platform-neutral.

## M4 — User-browser vertical slice

Deliver:

- Chrome personal-browser auto-connect spike and adapter;
- signed extension and Windows native-messaging fallback behind a three-platform BrowserConnection Interface;
- connection to the user's installed browser and existing profile;
- explicit tab assignment, attach, visible control and detach lifecycle;
- navigate, snapshot, activate and fill operations;
- visible Activity progress;
- approval for submit, authentication transition, download and scope expansion;
- cancellation, timeout and browser-crash handling.

Exit gate: on Windows, the fixed local end-to-end site can be completed, denied and cancelled in an assigned existing-profile tab; unrelated tabs remain untouched, control detaches cleanly and every run reaches one terminal result. Platform-specific browser transport remains isolated behind its Adapter.

## M5 — Recovery and diagnostics slice

Deliver:

- repeated-failure detection;
- safe-mode generated overlay;
- Host-owned recovery surface;
- bounded rotating logs;
- redacted diagnostics preview and local export;
- configuration backup and verified migration path.

Exit gate: on Windows, a deliberately broken optional integration is recoverable without manual file editing or loss of user-owned DSH data; recovery state and storage contracts remain portable.

## M6 — macOS and Linux platform parity

Deliver:

- macOS guardian／process-group Supervisor Adapter;
- Linux guardian／process-group／prctl Supervisor Adapter;
- native window, tray, credential-store and path Adapters;
- macOS and Linux browser-connection／native-messaging Adapters;
- native packaging inputs and installation flows;
- all M0–M5 lifecycle, permission, browser and recovery slices on both platforms;
- shared contract suites plus platform-specific hostile fixtures.

Exit gate: macOS and Linux pass the same observable acceptance cases already proven on Windows, each native Adapter passes the shared Interface contract and its platform fixtures, and normal or forced Host exit leaves no managed descendants.

## M7 — Release hardening

Deliver:

- supported Windows, macOS and Linux test matrices;
- installer, uninstall and upgrade smoke tests;
- accessibility pass;
- threat-model test cases and dependency review;
- release notes, known limitations and support guidance.

Exit gate: all release-blocking acceptance cases pass from a clean install and an upgrade fixture; known limitations are documented.

## Dependency order

```text
M0 → M1 → M2 → M3 → M4 → M5 → M6 → M7
           └─────────────► M5 diagnostics groundwork may begin
```

Browser UI mockups may proceed earlier, but browser execution cannot bypass the M3 capability boundary.

## Non-commitments

- remote or mobile control;
- full desktop UI automation;
- browsers outside the declared Chromium compatibility matrix;
- automatic application update;
- additional local tools.

Items outside the PC release are not implied future commitments; each requires its own requirements and threat analysis before implementation.
