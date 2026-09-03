# PC test plan

## Test layers

| Layer | Runs | Purpose |
|---|---|---|
| Domain unit | every change | lifecycle, task, permission, retry and error mapping |
| Contract | every change | DSH adapter, tool protocol, browser driver and storage schemas |
| Component | every change | trusted UI states, approval details, focus and redaction |
| Windows integration | pull request and nightly | Job Object, named pipe ACL, WebView navigation, credential store |
| macOS integration | pull request and nightly | guardian lease, process group, WebView navigation, Keychain, bundle helper |
| Linux integration | pull request and nightly | guardian, process group／prctl, WebView, secret store and packages |
| End to end | pull request for affected slices | clean launch through DSH and local browser fixture |
| Packaging | release candidate | install, upgrade, uninstall and clean-machine launch |
| Security | pull request and release candidate | origin, replay, injection, secret and permission cases |
| Accessibility | pull request automation plus release manual pass | keyboard, roles, names, focus, contrast-independent states |

## Deterministic fixtures

### Worker fixture

The fixture can:

- announce readiness normally or in fragmented output;
- announce an invalid or non-loopback URL;
- bind late or never become ready;
- exit before and after readiness;
- create descendants;
- ignore graceful shutdown;
- emit large, malformed and secret-bearing output;
- connect to the tool bridge with valid and invalid handshakes.

### DSH Workspace fixture

The fixture can:

- expose one DSH data directory with multiple registered Workspaces;
- resume a Workspace or require explicit Workspace selection;
- return canonical and non-canonical directory identities;
- reject missing directories without creating them;
- unregister a Workspace while retaining its directory, files and sessions.

### Local browser site

The test server includes:

- accessible and ambiguous controls;
- same-origin and cross-origin redirects;
- normal and sensitive forms;
- authentication transition simulation;
- pop-up, download and external-protocol attempts;
- delayed operations for cancellation;
- page text containing prompt-injection attempts;
- TLS-error fixture for manual or isolated test environments.

No release-blocking browser test depends on a public website.

### Browser and extension fixture

The fixture matrix includes clean and signed-in Chrome／Edge／Chromium profiles, Chrome auto-connect enabled／disabled／denied states, valid and invalid extension IDs, missing native-host registration, multiple windows and unrelated tabs. Synthetic cookies and saved-password markers verify that automation can use an authenticated page without exporting credential material.

## Required automated suites

### Lifecycle

- legal and illegal transition table;
- duplicate, late and old-generation events;
- bounded timeout and cancellation;
- explicit quit hides all Work windows before background cleanup, suppresses
  duplicate quit/restart actions, and exits only after verified cleanup;
- cleanup failure leaves a retryable tray/recovery surface and does not create
  an overlapping Worker;
- quit during DSH startup, a ready conversation/session and an active approval
  cancels the Host boundary without granting or replaying the DSH action;
- retry budget exhaustion;
- single terminal result property.

### Supervisor

- command construction by supported DSH version;
- readiness parsing across chunk boundaries;
- active origin and health validation;
- HTTP／WebSocket Origin, CSRF and per-generation gateway-session rejection;
- stdout/stderr draining and bounded truncation;
- graceful shutdown and forced tree cleanup;
- process and handle leak repetition test.

### Launch target and Workspace context

- persisted launch target contains only runtime, DSH data directory and
  profile identity;
- Workspace selection and creation remain outside the General settings form;
- current, explicit and missing Workspace contexts resolve deterministically;
- process-directory, install-directory, operating-system-home and DSH
  data-directory fallbacks are rejected;
- a per-generation Workspace context is passed to the DSH Adapter without
  changing the persisted launch target;
- Workspace unregistration retains user-owned directory contents and session
  data.

### Permission service

- exhaustive Work Tool allow／ask／deny classification;
- DSH `allowed-once`, rejected, cancelled and unavailable outcomes;
- no duplicate Host approval after DSH approval;
- browser connection grant and revocation;
- changed-payload and replay rejection;
- DSH call ID correlation and UI-close semantics;
- audit failure behaviour.

### Notifications

- default and invalid preference migration;
- global and per-class filtering;
- immediate preference changes while both trusted windows are open;
- foreground/background routing;
- duplicate and reconnect event suppression;
- verified target focus without operation execution;
- DSH in-page notice preservation;
- locale projection and bounded redaction;
- native delivery failure containment on each desktop target.

### Browser

- Chrome auto-connect enable, approval, reconnect and disconnect on Windows, macOS and Linux;
- extension／native-host fallback handshake on each platform;
- explicit tab assignment, debugger attachment and detachment;
- existing-profile authenticated operation without raw cookie access;
- rejection of unrelated-tab operations;
- stale element handles;
- redirects and new-origin scope;
- sensitive values absent from result and log;
- cancellation during navigation, wait and dialog;
- browser crash and late protocol events;
- no arbitrary page script operation in the tool schema.
- no legacy remote-debugging command-line launch against the default user profile.

### Storage and diagnostics

- schema migration and rollback;
- atomic replacement under simulated write failure;
- filesystem permission check;
- synthetic-secret redaction across every output sink;
- export preview matches written content;
- retention and partial-deletion reporting.

## Manual release matrix

Test each declared Windows, macOS and Linux target and each documented installation path. Include:

- clean user account;
- existing compatible DSH setup;
- missing and incompatible runtime;
- restricted account without administrator／root rights;
- display scaling and keyboard-only use;
- screen reader smoke test;
- no network during ordinary restart;
- abrupt Host termination during Worker and browser activity on each platform;
- install, upgrade, rollback and uninstall.

Exact supported operating-system builds and release quality budgets are maintained by the release process and must be fixed before a release candidate is declared.

## Release blocking

A release is blocked by:

- any failed acceptance case;
- any reproducible managed-process leak;
- unapproved Host capability execution;
- a synthetic secret appearing in ordinary logs, audit or diagnostic export;
- non-deterministic lifecycle or browser terminal result;
- inability to recover from the defined broken-integration fixture;
- missing notices or integrity information for distributed dependencies.

Flaky release-blocking tests are treated as failures. They must be fixed or replaced with a deterministic assertion, not simply retried until green.
