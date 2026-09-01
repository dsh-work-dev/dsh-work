# Global technical guidance

Status: normative engineering guidance for Work. Product requirements and accepted ADRs remain authoritative when they make a more specific decision.

## Purpose

This document defines how engineering decisions are made across the desktop Host, DSH integration, browser control and the three operating-system implementations. It is deliberately broader than the [PC architecture](../03-engineering/pc/architecture.md): architecture records the current shape of the system, while this guide constrains how that shape may evolve.

The desired result is a small, dependable desktop Host whose complexity is concentrated behind narrow, testable interfaces. Work must add desktop lifecycle and controlled local capabilities without becoming a second agent runtime or a general-purpose automation platform.

## Engineering priorities

When two solutions satisfy the same product requirement, prefer them in this order:

1. safety and preservation of user-owned data;
2. deterministic lifecycle, cancellation and cleanup;
3. a smaller trusted surface;
4. explicit behaviour that can be tested at an interface;
5. locality of change and diagnosis;
6. platform-correct behaviour;
7. operational simplicity;
8. implementation convenience.

Performance work may change this order only when a measured requirement demands it. Security and data-preservation rules are never traded for cosmetic responsiveness.

## Module design

Use the following vocabulary consistently:

- A **Module** is a coherent capability with one **Interface** and a hidden **Implementation**.
- An **Interface** includes types, invariants, ordering, errors, configuration and performance expectations that callers must understand.
- A **Seam** is the location at which an Interface allows behaviour to vary.
- An **Adapter** is one concrete implementation of an Interface at a Seam.
- **Depth** is the leverage delivered through an Interface: a deep Module hides substantial behaviour behind a small surface.
- **Leverage** benefits callers; **Locality** keeps knowledge, fixes and verification concentrated for maintainers.

### Rules

- Modules SHOULD be deep: expose a small operation vocabulary and own validation, sequencing, cleanup and error translation internally.
- Callers MUST NOT reproduce an Adapter's command grammar, platform calls, protocol details or compatibility rules.
- The Interface is the normal test surface. Tests that repeatedly reach through it indicate that the Module or Seam is misplaced.
- Dependencies MUST be accepted through construction or explicit operation inputs; domain Modules MUST NOT discover concrete dependencies globally.
- Operations SHOULD return typed results. Side effects, retries and partial completion MUST be represented explicitly.
- Do not introduce a public Seam for a hypothetical implementation. One Adapter is usually an implementation detail; two materially different Adapters establish a real Seam.
- Internal Seams MAY support focused tests without increasing the public Interface.
- Apply the deletion test: removing a useful Module should force its hidden complexity back into multiple callers. A Module whose removal only removes forwarding code is too shallow.

## Dependency direction

Policy and lifecycle state belong at the centre. DSH, Wails, browser protocols, storage engines and operating-system primitives stay at the edges.

```text
UI projection ──► application commands ──► lifecycle and policy
                                              │
                                              ▼
                                    narrow Interfaces at Seams
                                              │
                    ┌─────────────────────────┼──────────────────────┐
                    ▼                         ▼                      ▼
               DSH Adapter             Browser Adapter       Platform Adapter
```

- Edge Adapters MAY depend on third-party libraries and platform facilities.
- Domain Modules MUST depend only on value types and Interfaces they need.
- Frontend code MUST call an allowlisted, generated Host Interface. It MUST NOT access processes, files, secrets or arbitrary native methods directly.
- Logging, analytics and UI projection are observers. They MUST NOT become lifecycle truth or grant authority.
- Cross-layer convenience imports that bypass the intended Seam are prohibited.

## Authority and trust

The desktop Host is the final authority for native effects. DSH may request an operation and may own its model-facing approval, but it cannot grant itself Host capability.

Treat all of the following as untrusted input:

- agent and Tool text;
- DSH plugins and Worker messages;
- embedded web content and browser pages;
- browser-extension and native-messaging payloads;
- process output, configuration files and imported diagnostics.

Every native operation MUST pass schema validation, generation validation, scope validation and applicable hard policy immediately before execution. A prior approval, handshake or validation does not make later payloads trusted.

## Lifecycle and state

- Host, Worker, browser connection and browser task lifecycles MUST be explicit state machines.
- One owner serialises transitions for each state machine.
- Each Worker, browser connection, tab assignment and task receives an immutable generation or identity. Late events from obsolete generations MUST be ignored.
- Every attempt reaches exactly one terminal result: success, cancellation or classified failure.
- Startup, shutdown, retry, network wait, process wait and browser wait MUST be bounded.
- Cancellation MUST propagate through all affected Modules and prevent new effects after acceptance.
- Cleanup is part of correctness. `Stopped` is not reported until the managed platform boundary is verified empty or cleanup failure is surfaced.
- A UI refresh or renderer crash MUST NOT implicitly restart the Worker or alter native lifecycle state.

## Three-platform implementation

Windows, macOS and Linux share product behaviour and the Supervisor Interface, not a pretend-universal process Implementation.

- Put platform-independent transition rules, error taxonomy and hostile-child fixtures in shared Modules.
- Put process ownership, signals, handles, paths, credential storage, tray integration and packaging in platform Adapters.
- Each Adapter MUST establish its process-management boundary before the Worker can run or become visible.
- A platform feature is complete only after its native integration tests pass on every declared target.
- Platform-specific limitations MUST be reported honestly and must not be hidden behind a successful shared return value.
- Conditional branches for operating-system behaviour SHOULD remain inside the platform Adapter rather than spread through callers.

Detailed lifecycle primitives and test obligations are defined in [three-platform process supervision](../03-engineering/pc/process-supervision.md).

## DSH integration

- Treat DSH as an out-of-process, versioned dependency.
- Pin and verify the supported DSH version range.
- Keep launch grammar, readiness parsing, profile overlays, compatibility and shutdown translation inside the DSH Adapter.
- Use supported DSH extension and approval Seams; do not modify DSH source or reproduce its agent, conversation, plugin or approval behaviour.
- An unrecognised DSH version, event or Work Tool fails closed unless an explicitly non-mutating diagnostic mode handles it.
- Protocol requests MUST be typed, versioned, correlated and idempotent where retries are possible.
- The Worker web endpoint stays on loopback and requires origin, session and route controls; loopback alone is not a security control.

See [DSH integration contract](../03-engineering/pc/dsh-integration.md).

## Browser automation

- Browser control is a capability Module, not a general browser protocol passthrough.
- The public operation vocabulary describes user-level intent such as inspect, navigate, activate, fill, submit and cancel.
- Arbitrary JavaScript, raw CDP methods, cookies, passwords, history and unrelated-tab access MUST NOT appear in the model-facing Interface.
- A browser connection and tab assignment are visible, revocable, generation-scoped capabilities.
- Ordinary operations may run automatically inside an active assignment; high-impact operations use DSH's official one-shot approval flow.
- The Host revalidates the operation after approval and immediately before execution. Unknown, stale or expanded scope fails closed.
- Disconnect and Work exit detach control without closing the user's browser, clearing its profile or silently changing browser settings.
- Browser-specific connection mechanisms remain behind Adapters and share semantic contract tests.

See [browser automation](../03-engineering/pc/browser-automation.md) and [permission ownership](../02-design/pc/permissions-and-safety.md).

## Data, storage and privacy

- User and DSH-owned data are preserved by default. Work-owned migrations are versioned, atomic and recoverable.
- Secrets use operating-system protected storage when persistence is required.
- Configuration and generated overlays have explicit ownership and schema versions.
- Research, competitive analysis, internal metrics and unpublished strategy are local-only material and MUST NOT enter public repository content.
- Logs and diagnostics use structured fields and redact before buffering, persistence or UI projection.
- Credentials, cookies, sensitive form values and full page contents MUST NOT be logged.
- Work MUST NOT upload user content, diagnostics or audit data without a separate explicit user action.
- Retention and deletion behaviour MUST be deterministic and report partial failure.

## Concurrency and resource ownership

- Every goroutine, task, process, pipe, socket, timer, subscription and browser attachment has one named owner and a defined release condition.
- Channels, queues, buffers, logs, retries and parallel operations MUST have explicit bounds.
- Do not start background work that cannot be cancelled or joined during shutdown.
- Avoid shared mutable state. When it is required, document the serialisation rule next to the owning Module.
- Retries occur only for classified transient failures, use a bounded budget and preserve the original error for diagnostics.
- Duplicate requests MUST return the prior terminal result or a protocol error; they MUST NOT repeat an external effect.

## Errors and observability

- Convert dependency-specific failures into stable Work error codes at the Adapter that understands them.
- An error result states category, safe user summary, retryability, whether an effect occurred and a correlation ID.
- Expected rejection, cancellation and unavailable approval are structured results, not crashes.
- Events use stable names and correlation IDs across Host, Worker, approval and browser execution.
- Diagnostic detail may be rich, but it remains bounded and redacted.
- Do not use log text as a control protocol when a structured event or active probe is possible.
- Audit failure blocks an operation whose safety contract requires a durable audit record.

## Testing strategy

- Domain tests cover complete transition tables, invariants, cancellation and terminal-result properties.
- Contract tests exercise each Interface against every Adapter at its Seam.
- Platform Adapters share hostile fixtures but run on their actual operating systems.
- Security tests use invalid origins, stale generations, replayed requests, changed payloads, prompt injection and synthetic secrets.
- Browser tests use deterministic local sites; release correctness MUST NOT depend on public websites.
- End-to-end tests prove vertical slices through real Host, Worker and platform integrations.
- A flaky release-blocking test is a failure to repair, not a reason to add retries.
- Tests MUST verify cleanup and absence of leaked processes, handles, attachments and sensitive output.

The full matrix lives in the [PC test plan](../04-delivery/test-plan.md).

## Dependency and release discipline

- Pin fast-moving or security-sensitive dependencies and record integrity and licence information.
- Wrap an unstable dependency only at the narrowest useful Seam; do not leak its types throughout the codebase.
- Upgrades require affected Adapter contract tests and an explicit compatibility decision.
- Ordinary startup MUST NOT perform implicit package downloads or mutate global developer tooling.
- Installation, update, rollback and uninstall behaviour are part of each platform's product contract.
- A release candidate is portable only when lifecycle, WebView, credential storage and packaging tests pass on every declared target.

## Prohibited shortcuts

- unrestricted native JavaScript bridges;
- duplicated DSH command or output parsing;
- one cross-platform process implementation containing scattered platform branches;
- unclassified Work Tools or permissive unknown-operation fallbacks;
- arbitrary browser-protocol execution exposed to the model;
- unbounded waits, retries, queues, logs or background work;
- hidden network listeners, downloads or global configuration mutation;
- secrets or private research in repository content, logs, fixtures or diagnostics;
- reporting readiness, cancellation or shutdown before the underlying state is verified.

## Engineering review checklist

- [ ] The change maps to a product requirement or records a deliberate contract change.
- [ ] The responsible Module has a small Interface and owns its invariants.
- [ ] A new Seam corresponds to real variation and each Adapter has contract tests.
- [ ] Trust, authority and failure behaviour are explicit at every native effect.
- [ ] Cancellation, timeout, retry and cleanup paths are bounded and tested.
- [ ] Windows, macOS and Linux implications are handled by their Adapters.
- [ ] Persisted and wire formats are versioned and migration-safe.
- [ ] Logs, diagnostics and fixtures contain no sensitive or private material.
- [ ] The change preserves keyboard access and user-visible lifecycle state.
- [ ] Documentation and acceptance criteria change with observable behaviour.

