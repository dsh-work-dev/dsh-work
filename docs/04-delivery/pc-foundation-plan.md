# PC foundation and first-run plan

Status: approved execution plan. This document plans the work; it does not indicate that the repository or application skeleton already exists.

## Outcome

The first implementation objective is a thin but real end-to-end path:

```text
launch Work
  → show trusted startup state
  → start one pinned local DSH Worker
  → validate its loopback endpoint and readiness
  → display the real DSH workspace
  → quit Work
  → verify the managed Worker process boundary is empty
```

This path is the tracer bullet for the PC application. It proves the toolchain, desktop window, Host／frontend communication, DSH launch contract, embedded WebView and process cleanup before the project invests in broad UI work, browser automation or release packaging.

## Priority order

1. create a reproducible repository and build skeleton;
2. launch a minimal trusted desktop shell;
3. run and display a real DSH Worker on Windows;
4. harden the Windows lifecycle, errors, cancellation and cleanup;
5. establish the minimum operational UI states;
6. converge Work-owned UI with DSH's visual language;
7. continue into Tool permission and browser vertical slices on Windows;
8. implement macOS and Linux against the same three-platform contracts before release.

DSH style reuse is a UI convergence task, not a prerequisite for the first real run. Before convergence, the shell only needs to be legible, keyboard operable and structurally compatible with the [UI guidelines](../02-design/pc/ui-guidelines.md).

## Scope of this plan

This plan covers the detailed execution of roadmap milestones M0 through M2:

- repository and toolchain foundation;
- desktop Host and frontend skeleton;
- minimal lifecycle state machine;
- real DSH discovery, start, readiness, embed and stop;
- production Windows process supervision;
- three-platform Supervisor Interface, invariants, error vocabulary and test-contract design;
- baseline tests and continuous integration;
- minimum startup, ready and failure presentation;
- DSH-aligned visual convergence after the functional path works.

The following are intentionally deferred to later roadmap milestones:

- DSH Tool registration and approval integration;
- browser connection or automation;
- full Settings, Activity and Diagnostics features;
- deep visual customisation;
- installers, update flows and release polish.

## Fixed architectural decisions

- Go and Wails 3 form the desktop Host baseline; the frontend uses TypeScript.
- DSH remains an out-of-process, pinned external runtime.
- The Host is the final authority for process and native effects.
- The real DSH workspace is embedded only after active readiness validation.
- Windows, macOS and Linux share one Supervisor Interface and require separate native Adapter Implementations; Windows is implemented first.
- Test fakes may implement an Interface inside tests; no production platform placeholder may report success without establishing a real native process boundary.
- Local-only research and confidential material remains excluded from version control. Public planning and engineering documentation lives under `docs/`.
- Visual polish cannot delay correct startup, shutdown, cancellation, containment or recovery.

## Three-platform normative discipline

Windows-first describes implementation order, not architecture scope.

- Shared lifecycle, Supervisor, DSH, browser and storage Interfaces MUST be designed for Windows, macOS and Linux from their first committed version.
- Shared value types MUST NOT contain Windows handles, Unix signals, platform paths or platform-only error values.
- Each platform's ownership primitive, startup sequence, cleanup sequence and known escape condition MUST be documented before the shared Interface is accepted.
- Shared contract tests and hostile fixtures are written once; the Windows Adapter runs them first, and later macOS／Linux Adapters must pass the same suite plus their native tests.
- Production code MUST NOT include fake macOS or Linux Adapters. Until their native Implementations land, those platforms are planned targets rather than claimed working builds.
- Windows conditionals and system calls stay inside the Windows Adapter. Callers depend on the Supervisor Interface and platform-neutral results.
- A Windows implementation choice that cannot be satisfied correctly on macOS or Linux requires an Interface redesign or ADR before it spreads into callers.
- macOS and Linux implementation is a later roadmap milestone, but it remains release-blocking for the declared three-platform PC release.

## Execution stages

### F0 — Lock the implementation baseline

Purpose: make the scaffold reproducible before generated files or dependencies enter the repository.

Work:

1. record supported Go, Node, package-manager and Wails versions;
2. pin the DSH version or revision used by the first-run fixture;
3. record the initial Windows development target and the macOS／Linux validation targets;
4. decide the frontend framework and keep it behind generated narrow Host commands;
5. define formatting, unit-test, frontend-test, build and documentation-check commands;
6. verify that all local-only material and secrets remain ignored before the first commit.

Outputs:

- version manifest or equivalent toolchain declarations;
- initial build and verification command inventory;
- dependency and licence inventory entry points;
- recorded DSH compatibility baseline.

Exit gate:

- a clean environment can identify every required tool and exact supported version without relying on global implicit state;
- no implementation file has to guess which DSH or Wails contract it targets.

### F1 — Initialise the repository and application skeleton

Purpose: establish one buildable composition root without creating empty abstraction packages.

Work:

1. initialise Git in the existing documentation directory;
2. create the Go module and Wails 3 desktop application;
3. create the TypeScript frontend through the selected Wails-compatible template;
4. preserve the existing `docs/`, ignore rules and public／private documentation separation;
5. add development, test and production build entry points;
6. add basic continuous-integration jobs for Windows, macOS and Linux;
7. add application metadata and placeholder assets only where the scaffold requires them.

Initial repository shape:

```text
cmd/work/                 desktop composition root
internal/app/             Host wiring and application lifecycle owner
internal/lifecycle/       state and transition rules once required by F2
frontend/                 trusted Work shell
build/                    platform build and packaging inputs created by Wails
docs/                     public project documentation
```

Do not create every directory from the target architecture during this stage. A Module is added when a vertical slice needs its Interface and Implementation.

Exit gate:

- the application builds and opens a trusted window on the current development machine;
- frontend and Go tests can run independently;
- Windows, macOS and Linux CI jobs at least validate the portions that can run on their native runners;
- the generated scaffold contains no unrestricted native JavaScript bridge.

### F2 — Establish the minimum Host shell

Purpose: prove Host／frontend communication and visible lifecycle projection without waiting for DSH integration.

Work:

1. define the smallest lifecycle state set needed by the tracer bullet: `Starting`, `Ready`, `Stopping` and `Failed`;
2. make one lifecycle Module own transition ordering and terminal results;
3. expose a narrow read model and allowlisted commands to the frontend;
4. render minimal startup, workspace placeholder and failure surfaces;
5. add cancellation and shutdown contexts even before all operations use them;
6. add correlation ID and structured event foundations;
7. use a test-only Worker fake to drive transition tests and UI state tests.

The shell at this stage is functional scaffolding. It does not need final colours, typography, illustrations, Settings routes or Activity detail.

Exit gate:

- tests cover every legal transition in the minimum state set and reject an illegal or duplicate terminal transition;
- the visible state always follows Host state rather than frontend-local guesses;
- the frontend can be refreshed without changing Worker lifecycle state;
- a fake startup reaches `Ready`, and fake failure reaches an actionable stable surface rather than a blank WebView.

### F3 — Run a real DSH tracer bullet on Windows

Purpose: expose integration assumptions early using the current primary development platform.

Work:

1. locate an explicitly configured, compatible local DSH runtime;
2. build the launch plan inside the first concrete DSH Adapter;
3. start DSH as an out-of-process Worker on an explicit loopback port;
4. capture bounded stdout and stderr for readiness diagnosis;
5. recognise the pinned DSH readiness signal and confirm it with an active health probe;
6. validate scheme, host, port and trusted application origin;
7. navigate the embedded workspace only after readiness succeeds;
8. request graceful shutdown on Work exit and verify the initial Worker has terminated;
9. map missing runtime, incompatible version, early exit and readiness timeout into stable failures.

The first tracer bullet may use the Windows Adapter directly behind the already-required Supervisor Seam. It must not place Windows calls in shared lifecycle or DSH Modules.

Exit gate:

- a clean configured Windows environment can launch Work and reach the real pinned DSH workspace;
- external top-level navigation is blocked or delegated to the system browser;
- missing and incompatible DSH versions produce stable Work failures;
- normal Work exit stops the real Worker and leaves no known managed child alive;
- repeating the run does not require manual port or process cleanup.

### F4 — Harden the Windows lifecycle against the shared contract

Purpose: turn the Windows tracer bullet into deterministic product behaviour without allowing Windows details to redefine the three-platform Interface.

Work:

1. finalise immutable Worker generation IDs and ignore old-generation events;
2. implement bounded readiness, graceful-stop and force-stop phases;
3. make each startup and shutdown attempt publish exactly one terminal result;
4. drain output concurrently into bounded, redacted buffers;
5. implement cancellation through lifecycle, Supervisor and DSH Adapter Interfaces;
6. introduce bounded automatic retry only for classified transient failures;
7. implement the Windows Job Object Adapter with kill-on-close;
8. create the Worker suspended and assign it to the Job before resume;
9. add descendant, Host-loss, handle, task and buffer leak fixtures;
10. keep trusted failure and quit controls responsive throughout cleanup.

Every Interface change in this stage is reviewed against the documented macOS guardian／process-group and Linux guardian／process-group／prctl Implementations. Those Adapters are not implemented in this foundation plan, but the Windows code may not make their later Implementation impossible or force platform conditions into callers.

Exit gate:

- late, duplicate and reordered fixture events cannot change the current generation;
- timeouts and cancellation terminate predictably;
- the Windows Worker is assigned before execution and the Job is killed after graceful-stop expiry;
- normal quit, forced Host termination and ignored graceful stop leave no process in the Windows managed boundary;
- repeated start／stop tests show stable process and handle counts;
- `Stopped` is never reported before the Windows Adapter verifies its Job boundary or reports cleanup failure;
- shared contract tests contain no Windows-only types or expectations.

### F5 — Complete the Windows first-run user path

Purpose: make the proven Windows lifecycle understandable without expanding into the complete product UI.

Work:

1. display named startup steps: configuration, runtime, Worker, readiness and workspace;
2. show stable error code, summary and one safe primary recovery action;
3. implement Windows window, tray, close and full-quit behaviour through the platform Adapter;
4. keep Diagnostics entry visible in failures, initially backed by a bounded in-memory snapshot;
5. verify keyboard focus, live status announcements and reduced-motion behaviour;
6. ensure no sensitive Worker output appears in primary UI;
7. confirm that UI state consumes platform-neutral lifecycle projection rather than Windows events.

Exit gate:

- the Windows portions of AC-002, AC-003, AC-004, AC-005, AC-006 and AC-023 pass;
- startup never presents a blank embedded view;
- users can identify the current step, cancel or quit safely, and understand a failure without reading raw logs;
- later platform Adapters can project the same lifecycle states without changing the frontend Interface.

### F6 — Align the functional shell with DSH styling

Purpose: make the already-functional Work surfaces feel visually continuous with the real embedded DSH workspace.

This stage starts after F5 provides stable real states to style. It is part of the main Windows foundation sequence, but it cannot move ahead of lifecycle correctness.

Work:

1. capture the pinned DSH light and dark workspace states used as visual references;
2. inventory the relevant DSH semantic theme roles and shared visual primitives;
3. choose between consuming an official reusable package and maintaining a versioned Work token Adapter;
4. map Work shell semantics to the selected DSH-compatible theme roles;
5. align typography, density, borders, focus, icon family and motion without copying unrelated DSH feature code;
6. compare startup, ready and failure screenshots against the agreed visual direction;
7. verify that platform adaptations remain theme inputs rather than separate visual systems;
8. record attribution and licence obligations for any vendored style or asset.

Exit gate:

- Work chrome and DSH content read as one restrained desktop workspace while their ownership remains visible;
- light, dark, keyboard focus and minimum window states pass the UI review checklist on Windows;
- macOS and Linux window adaptations are documented without hard-coding Windows chrome into shared UI;
- upgrading the pinned DSH dependency has one documented place to review theme compatibility;
- Host lifecycle correctness does not depend on a running DSH theme service.

### F7 — Windows foundation completion and handoff

Purpose: close the foundation plan and make the next vertical slice independently executable.

Work:

1. run formatting, unit, contract, native integration, documentation and public-content checks;
2. update architecture and ADRs for any implementation decision that differs from the current specification;
3. record exact supported toolchain and DSH versions;
4. record known platform limitations without hidden workarounds;
5. map completed acceptance evidence into delivery traceability;
6. prepare M3 work around the DSH Tool bridge and permission classifier.

Exit gate:

- M0, M1 and M2 roadmap gates are satisfied for the Windows-first foundation;
- the Windows Adapter has native evidence, and the macOS／Linux Adapter contracts and implementation plans have passed architecture review;
- the real DSH first-run path is reproducible from a clean documented setup;
- the repository contains no private research, credentials or internal metrics;
- later browser work can depend on a deterministic Worker generation and shutdown contract.

## Dependency graph

```text
F0 baseline
  └─► F1 repository skeleton
        └─► F2 minimum Host shell
              └─► F3 real DSH tracer bullet on Windows
                    └─► F4 Windows lifecycle hardening
                          └─► F5 Windows first-run UI
                                └─► F6 DSH visual convergence
                                      └─► F7 Windows foundation handoff
```

macOS and Linux native Implementations follow in the platform-parity roadmap milestone. Their documented primitives and constraints shape every shared Interface above even though their code is not on the Windows foundation critical path.

## Suggested change sequence

Each change should leave the repository buildable and should be reviewable through one primary Interface:

1. toolchain and repository scaffold;
2. Host window plus frontend command／event handshake;
3. lifecycle state Module with test Worker fake;
4. Windows real-DSH start, readiness, embed and graceful stop;
5. generation, cancellation, timeout and forced-cleanup hardening;
6. Windows Job Object completion and hostile-child tests;
7. Windows first-run error, tray and accessibility surfaces;
8. DSH-compatible UI convergence and visual regression baseline;
9. Windows foundation acceptance evidence and M3 handoff.

Do not combine the Windows Adapter, complete UI styling and DSH integration into one change. The shared Interface stabilises through the Windows tracer bullet, but every review checks its macOS and Linux feasibility before accepting it.

## Early risks and containment

| Risk | Earliest containment |
|---|---|
| Wails 3 surface changes | Pin the toolchain in F0 and keep Wails types at the application edge |
| DSH launch or Web UI changes | Pin DSH and centralise launch, readiness and compatibility in one Adapter |
| Worker appears ready before it is usable | Require structured signal plus active loopback probe before navigation |
| WebView HTTP or WebSocket mismatch | Prove real DSH loading in F3 before building surrounding features |
| Windows-only assumptions leak into shared code | Establish the Supervisor Seam before the Windows tracer bullet and keep native calls inside its Adapter |
| macOS／Linux constraints are discovered too late | Document their guardian, lease, process-group and escape semantics before accepting the shared Supervisor Interface |
| Scaffold grows into empty abstraction packages | Add a Module only when a vertical slice needs its Interface and Implementation |
| UI styling delays runtime correctness | Keep styling after the complete Windows first-run path and give lifecycle failures priority |
| Sensitive data enters logs during early integration | Use bounded redaction before any output reaches persistence or UI |

## Foundation definition of done

The foundation is complete only when:

- a clean checkout builds through pinned commands;
- the trusted Work shell opens without DSH;
- a configured compatible DSH starts and becomes visible only after validation;
- startup, readiness failure, quit and forced cleanup are deterministic;
- Windows uses a tested native process Adapter and shared code contains no Windows-only contract assumptions;
- macOS and Linux native Adapter Implementations remain explicit release work rather than production stubs;
- the Host remains usable when the Worker fails;
- the minimum UI is keyboard operable, satisfies the baseline UI guidelines and is visually aligned with DSH;
- all managed processes and resources are accounted for after shutdown;
- public documentation contains only publishable project material;
- M3 can begin without revisiting application composition or Worker lifecycle ownership.
