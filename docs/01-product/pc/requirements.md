# PC product requirements

Status: normative specification for the first PC release.

## Application lifecycle

| ID | Requirement |
|---|---|
| FR-LIFE-001 | Work MUST enforce one active desktop instance per user session and focus the existing instance when launched again. |
| FR-LIFE-002 | Work MUST expose `Starting`, `Ready`, `Busy`, `Waiting for approval`, `Stopping`, `Failed` and `Safe mode` as explicit user-visible states. |
| FR-LIFE-003 | Closing the main window MUST hide it to the tray; choosing `Quit` MUST perform a full managed shutdown. |
| FR-LIFE-004 | Work MUST remain operable enough to show diagnostics and recovery actions when the Worker fails. |
| FR-LIFE-005 | Every startup and shutdown attempt MUST reach a terminal success or failure state; indefinite spinners are not allowed. |

## Runtime and Worker supervision

| ID | Requirement |
|---|---|
| FR-SUP-001 | Work MUST verify that the configured DSH runtime is compatible before normal startup. |
| FR-SUP-002 | Work MUST start the Worker through a versioned DSH adapter rather than embedding CLI assumptions across modules. |
| FR-SUP-003 | The Worker service MUST bind to loopback by default and use an available port selected without user intervention. |
| FR-SUP-004 | Work MUST validate a readiness signal before showing Worker content. |
| FR-SUP-005 | Work MUST capture bounded diagnostic stdout/stderr without exposing secrets in the normal UI. |
| FR-SUP-006 | Normal quit MUST request graceful Worker shutdown, then use a bounded forced cleanup when required. |
| FR-SUP-007 | Work MUST clean up the full process tree that it started after normal quit, Worker failure and forced Host termination on every supported platform. |
| FR-SUP-008 | Restart attempts MUST be bounded and MUST stop when repeated failure would create a loop. |
| FR-SUP-009 | Work MUST distinguish configuration, compatibility, port, timeout, crash and permission failures with stable error codes. |
| FR-SUP-010 | Windows, macOS and Linux MUST implement the same Supervisor contract through separate platform adapters and pass the same hostile-child fixture suite. |

## DSH runtime, home and profile management

| ID | Requirement |
|---|---|
| FR-MGR-001 | Work MUST load the external DSH Web UI from the selected out-of-process DSH Worker after readiness; the Work binary MUST NOT embed a second DSH Web UI. |
| FR-MGR-002 | `dsh-work` and the Manager service MUST maintain a versioned catalog that can contain multiple DSH runtimes, while normal Work startup MUST NOT download or reconcile runtime packages. |
| FR-MGR-003 | Every launch selection MUST identify exactly one DSH runtime, DSH home, profile and workspace; a profile name without its home identity MUST be rejected. |
| FR-MGR-004 | A DSH profile MUST own its plugin associations. Plugin list, install and removal operations MUST require an explicit profile reference and MUST delegate composition to the selected DSH runtime's supported CLI. |
| FR-MGR-005 | Runtime, DSH home, profile and plugin management MUST be available in a separate trusted Manager window without an application menu; the DSH Workspace window MUST expose only native DSH menu commands for opening or focusing that surface, restarting DSH and quitting Work. |
| FR-MGR-006 | Work-owned DSH homes may be created by Work; user-owned DSH homes MUST be registered from an existing directory and MUST NOT be silently created, relocated or deleted by Work. |
| FR-MGR-007 | Runtime or home removal MUST be rejected while selected or active, and the Manager UI/CLI MUST state whether removal unregisters catalog metadata or removes files; user-owned DSH data MUST never be silently deleted. |

## Embedded content and navigation

| ID | Requirement |
|---|---|
| FR-WEB-001 | The embedded DSH view MUST initially navigate only to the validated loopback Worker origin. |
| FR-WEB-002 | Top-level navigation away from trusted application origins MUST be blocked in the embedded view and offered to the operating-system browser. |
| FR-WEB-003 | A failed or disconnected Worker MUST replace the embedded view with a trusted native recovery surface. |
| FR-WEB-004 | Host capabilities MUST NOT be exposed as an unrestricted global JavaScript object. |
| FR-WEB-005 | Work MUST prevent an untrusted web origin from reading or invoking Worker HTTP or WebSocket endpoints; loopback binding alone is not sufficient protection. |

## Browser automation

| ID | Requirement |
|---|---|
| FR-BRW-001 | Browser operations MUST enter through a typed, versioned tool request and return a structured terminal result. |
| FR-BRW-002 | The first release MUST support navigation, page inspection, element activation and text entry in a user-assigned tab of a supported installed Chromium browser. |
| FR-BRW-003 | Work MUST use the user's existing browser profile and login state only after the user enables a supported browser-control method, accepts the browser-owned connection prompt where applicable, and assigns a tab. |
| FR-BRW-004 | Every operation MUST declare its capability, target and scope before execution. |
| FR-BRW-005 | Sensitive submission, authentication transition, download, external protocol launch and scope expansion MUST pause for approval. |
| FR-BRW-006 | The user MUST be able to cancel an active browser task; no new action may start after cancellation is accepted. |
| FR-BRW-007 | Task completion, cancellation and Work exit MUST detach automation without closing the user's browser, clearing its profile or altering unrelated tabs. |
| FR-BRW-008 | Page content MUST be treated as untrusted input and MUST NOT grant new Host capability. |
| FR-BRW-009 | Direct Chrome control SHOULD use Chrome's explicit personal-browser agent connection where supported; Work MUST NOT launch the default profile with legacy remote-debugging command-line switches. |
| FR-BRW-010 | Each task MUST name its assigned browser, window and tab; operations targeting any other tab MUST be rejected until the user assigns it. |
| FR-BRW-011 | The extension and Host MUST NOT read or export raw cookies, saved passwords, browsing history or unrelated-tab content for first-release operations. |
| FR-BRW-012 | Browser connection method, profile-wide exposure, task attachment and assigned-tab state MUST be visible and immediately revocable from Work and the browser-owned control surface. |

## Permission and audit

| ID | Requirement |
|---|---|
| FR-SEC-001 | Every Work browser Tool MUST be explicitly claimed by a Work-owned DSH `tools/pre-execute` classifier that returns `allow`, `ask` or `deny`; unclassified Work Tool calls MUST fail closed. |
| FR-SEC-002 | Operations classified `ask` MUST reuse DSH `ctx.approval` and its official approval UI; only `allowed-once` may execute. |
| FR-SEC-003 | Rejection, cancellation and unavailable approval MUST be normal structured Tool results and MUST NOT be represented as a Host crash. |
| FR-SEC-004 | Work MUST NOT show a second Host approval prompt for the same DSH Tool operation; browser- or OS-owned permission prompts remain separate. |
| FR-SEC-005 | Browser connection is a visible, revocable Host session grant and MUST NOT by itself approve a DSH high-impact action. |
| FR-SEC-006 | DSH approval events and Host execution events MUST share the DSH call ID and Work correlation ID without duplicating secret arguments. |
| FR-SEC-007 | Audit and diagnostic output MUST redact credentials, tokens, cookies and sensitive form values. |
| FR-SEC-008 | Work MUST NOT upload user content, diagnostics or audit data without a separate explicit user action. |
| FR-SEC-009 | Page content, an extension message or a Worker plugin MUST NOT change the Work Tool risk classification or manufacture an approval outcome. |

## Recovery and diagnostics

| ID | Requirement |
|---|---|
| FR-REC-001 | Repeated startup failure MUST offer a safe-mode path that bypasses optional integrations without deleting user-owned data. |
| FR-REC-002 | Recovery actions MUST state whether they change configuration, create a backup or affect running tasks. |
| FR-REC-003 | Diagnostic export MUST support preview, redaction and an explicit local destination. |
| FR-REC-004 | Error displays MUST include a stable code, summary, likely cause and next action. |
| FR-REC-005 | Work MUST preserve the original configuration until a recovery change has been confirmed successful. |

## Quality requirements

| ID | Requirement |
|---|---|
| NFR-REL-001 | Lifecycle and permission state machines MUST be deterministic and covered by automated tests. |
| NFR-REL-002 | Host startup, shutdown and cancellation MUST use bounded waits and remain responsive. |
| NFR-SEC-001 | Local listeners MUST use least exposure and reject untrusted origins. |
| NFR-SEC-002 | Secrets MUST use operating-system protected storage when persistence is required. |
| NFR-ACC-001 | All native and web controls MUST be keyboard operable, labelled for assistive technology and understandable without colour alone. |
| NFR-OBS-001 | Logs MUST use stable event names and correlation IDs across Host, Worker and browser operations. |
| NFR-MNT-001 | Platform, DSH and browser dependencies MUST be isolated behind interfaces with contract tests. |
| NFR-MNT-002 | Public interfaces and persisted schemas MUST be versioned before the first stable release. |
| NFR-PORT-001 | A PC release candidate MUST pass lifecycle, WebView, credential-store and packaging tests on every declared Windows, macOS and Linux target. |

## Requirement changes

Changing a `MUST` requirement requires the same change to update its acceptance case and any affected state, security or architecture document.
