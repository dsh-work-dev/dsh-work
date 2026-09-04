# PC product requirements

Status: normative specification for the first PC release.

## Application lifecycle

| ID | Requirement |
|---|---|
| FR-LIFE-001 | dsh-work MUST enforce one active desktop instance per user session and focus the existing instance when launched again. |
| FR-LIFE-002 | dsh-work MUST expose `Starting`, `Ready`, `Busy`, `Waiting for approval`, `Stopping`, `Failed` and `Safe mode` as explicit user-visible states. |
| FR-LIFE-003 | dsh-work MUST apply the persisted close policy to every dsh-work window. The default `closeToTray=true` hides a closed window and keeps dsh-work available from the tray when no windows remain; when disabled, closing the last visible window MUST perform a full managed shutdown. Explicit `Quit dsh-work` MUST always perform a full managed shutdown. |
| FR-LIFE-004 | dsh-work MUST remain operable enough to show diagnostics and recovery actions when the Worker fails. |
| FR-LIFE-005 | Every startup and shutdown attempt MUST reach a terminal success or failure state; indefinite spinners are not allowed. |

## Runtime and Worker supervision

| ID | Requirement |
|---|---|
| FR-SUP-001 | dsh-work MUST verify that the configured DSH runtime is compatible before normal startup. |
| FR-SUP-002 | dsh-work MUST start the Worker through a versioned DSH adapter rather than embedding CLI assumptions across modules. |
| FR-SUP-003 | The Worker service MUST bind to loopback by default and use an available port selected without user intervention. |
| FR-SUP-004 | dsh-work MUST validate a readiness signal before showing Worker content. |
| FR-SUP-005 | dsh-work MUST capture bounded diagnostic stdout/stderr without exposing secrets in the normal UI. |
| FR-SUP-006 | Normal quit MUST request graceful Worker shutdown, then use a bounded forced cleanup when required. |
| FR-SUP-007 | dsh-work MUST clean up the full process tree that it started after normal quit, Worker failure and forced Host termination on every supported platform. |
| FR-SUP-008 | Restart attempts MUST be bounded and MUST stop when repeated failure would create a loop. |
| FR-SUP-009 | dsh-work MUST distinguish configuration, compatibility, port, timeout, crash and permission failures with stable error codes. |
| FR-SUP-010 | Windows, macOS and Linux MUST implement the same Supervisor contract through separate platform adapters and pass the same hostile-child fixture suite. |

## DSH runtime, data directory and profile management

| ID | Requirement |
|---|---|
| FR-MGR-001 | dsh-work MUST load the external DSH Web UI from the current out-of-process DSH Worker after readiness; the dsh-work binary MUST NOT embed a second DSH Web UI. |
| FR-MGR-002 | `dsh-work` and the Manager service MUST maintain a versioned catalog that can contain multiple DSH runtimes, while normal dsh-work startup MUST NOT download or reconcile runtime packages. |
| FR-MGR-003 | Every persisted Configured Run context MUST identify exactly one DSH runtime, DSH data directory and profile; a profile name without its data-directory identity MUST be rejected. Changing any member while dsh-work is running MUST request an immediate managed context switch rather than storing a pending next-launch target. Each Worker generation MUST receive a separately resolved Workspace context; Workspace MUST NOT be persisted as part of the Configured Run context. |
| FR-MGR-004 | A DSH data directory MUST scope its profiles and their plugin associations, and a DSH profile MUST own its plugin associations. Plugin inspection MUST require an explicit profile reference. Plugin installation, removal and other profile mutations MUST be accepted only when that reference equals the current Run context's profile; non-current profiles MUST be read-only, including for forged backend requests. Composition MUST delegate to the current DSH runtime's supported CLI. |
| FR-MGR-005 | Runtime, DSH data-directory, profile and plugin management MUST live inside a separate trusted dsh-work Settings window. Its rail MUST be flat and shallow, with `Overview` first and read-only, one top-level `General` page for dsh-work preferences, one top-level `Notifications` page for desktop-notification preferences, a profile resource page that owns its child plugin actions, and separate resource pages for runtimes and DSH data directories. `General` MUST show the current/configured Run context and MUST NOT contain a Workspace selector or editable Workspace path. The application menu MUST expose `Settings` and `Help`; `Help` MUST contain `Check for Updates…` and `About dsh-work`. |
| FR-MGR-006 | dsh-work-owned DSH data directories may be created by dsh-work; user-owned DSH data directories MUST be registered from an existing directory and MUST NOT be silently created, relocated or deleted by dsh-work. |
| FR-MGR-007 | Runtime or DSH data-directory removal MUST be rejected while selected or active, and the Manager UI/CLI MUST state whether removal unregisters catalog metadata or removes files; user-owned DSH data MUST never be silently deleted. |
| FR-MGR-008 | dsh-work appearance MUST read the selected DSH data directory's `ui-theme.preference`; `light`, `dark` and `system` MUST resolve consistently across trusted dsh-work surfaces, including when the preference changes while both windows are open. dsh-work MUST NOT expose or persist a second appearance preference. |
| FR-MGR-009 | dsh-work MUST provide English, Simplified Chinese and Japanese UI copy through one persisted language preference. Changing the preference in Settings MUST update the trusted dsh-work surfaces, native menu and tray without restarting DSH. |
| FR-MGR-010 | Custom profile names MUST be editable from profile detail. Built-in profile names MUST remain fixed. A custom rename MUST preserve the DSH profile manifest and patch layers and MUST update the Configured Run context when it references that profile. The current profile MUST remain protected from rename while it is running. |
| FR-MGR-011 | Workspace selection and creation MUST be performed through DSH's Workspace surface or an explicit launch/session action. dsh-work MUST display the current Workspace context separately from the current/configured Run context and MUST NOT infer a Workspace from its process directory, install directory, operating-system home or DSH data directory. |
| FR-MGR-012 | Workspace resolution MUST use DSH's Workspace contract, including canonical directory identity and existing-directory validation. Removing a Workspace registration MUST NOT delete or relocate its directory, user files, sessions or logs. |
| FR-MGR-013 | A change to the selected runtime, DSH data directory or profile MUST take effect immediately through one serialized Worker context switch. dsh-work MUST stop and verify the previous generation before starting the candidate, and MUST publish the candidate as current only after it reaches `Ready`. |
| FR-MGR-014 | If a candidate Run context fails compatibility, startup or readiness, dsh-work MUST automatically restore the last known-good Run context. The failed candidate MUST NOT become current, the user MUST receive a terminal actionable switch result, and dsh-work MUST NOT run overlapping Workers during the switch or rollback. If rollback also fails, dsh-work MUST enter `Failed` with retryable recovery. |
| FR-MGR-015 | Only the current profile in a `Ready` Run context may be modified through plugin or profile-composition operations. Non-current profiles and their plugin associations MUST remain inspectable but read-only until that profile becomes current through a successful context switch. |

## Notifications

| ID | Requirement |
|---|---|
| FR-NOT-001 | dsh-work MUST provide desktop-notification preferences in the first notification implementation, in a flat top-level `Notifications` Settings route. |
| FR-NOT-002 | dsh-work MUST persist the global desktop-notification switch and the `completed`, `interactionRequired`, `errors` and `lifecycle` class switches in the versioned dsh-work settings document. |
| FR-NOT-003 | Missing notification preferences MUST use the safe defaults: global, completed, interaction-required and errors enabled; lifecycle disabled. Invalid notification values MUST fail closed to those defaults without discarding the rest of the settings document. |
| FR-NOT-004 | dsh-work MUST evaluate the global switch and then the matching class switch before every desktop delivery; a disabled dsh-work preference MUST NOT hide a DSH in-page notice. |
| FR-NOT-005 | dsh-work MUST suppress completion and routine lifecycle desktop notifications while the Workspace is the active surface, and MUST make eligible enabled events available when the Workspace is hidden or unfocused. |
| FR-NOT-006 | One logical DSH or dsh-work notification event MUST produce at most one desktop delivery per dsh-work session, using a stable event identity or a bounded deduplication key. |
| FR-NOT-007 | DSH notification events MUST enter dsh-work through a structured, versioned bridge carrying event class, source context, identity and a verified focus target when available; DOM scraping, arbitrary log parsing and gateway presentation parsing MUST NOT be the contract. |
| FR-NOT-008 | Notification clicks MUST focus the Workspace or a verified target and MUST NOT approve, deny or execute a DSH operation. |
| FR-NOT-009 | Desktop notification copy MUST be bounded, localised and redacted; raw process output, credentials, tokens, cookies, stack traces and unbounded agent text MUST NOT be placed in a desktop notification. |
| FR-NOT-010 | dsh-work MUST apply notification preference changes immediately across both trusted dsh-work windows without restarting DSH, and dsh-work-owned notification copy MUST support English, Simplified Chinese and Japanese. |

## Embedded content and navigation

| ID | Requirement |
|---|---|
| FR-WEB-001 | The embedded DSH view MUST initially navigate only to the validated loopback Worker origin. |
| FR-WEB-002 | Top-level navigation away from trusted application origins MUST be blocked in the embedded view and offered to the operating-system browser. |
| FR-WEB-003 | A failed or disconnected Worker MUST replace the embedded view with a trusted native recovery surface. |
| FR-WEB-004 | Host capabilities MUST NOT be exposed as an unrestricted global JavaScript object. |
| FR-WEB-005 | dsh-work MUST prevent an untrusted web origin from reading or invoking Worker HTTP or WebSocket endpoints; loopback binding alone is not sufficient protection. |

## Browser automation

| ID | Requirement |
|---|---|
| FR-BRW-001 | Browser operations MUST enter through a typed, versioned tool request and return a structured terminal result. |
| FR-BRW-002 | The first release MUST support navigation, page inspection, element activation and text entry in a user-assigned tab of a supported installed Chromium browser. |
| FR-BRW-003 | dsh-work MUST use the user's existing browser profile and login state only after the user enables a supported browser-control method, accepts the browser-owned connection prompt where applicable, and assigns a tab. |
| FR-BRW-004 | Every operation MUST declare its capability, target and scope before execution. |
| FR-BRW-005 | Sensitive submission, authentication transition, download, external protocol launch and scope expansion MUST pause for approval. |
| FR-BRW-006 | The user MUST be able to cancel an active browser task; no new action may start after cancellation is accepted. |
| FR-BRW-007 | Task completion, cancellation and dsh-work exit MUST detach automation without closing the user's browser, clearing its profile or altering unrelated tabs. |
| FR-BRW-008 | Page content MUST be treated as untrusted input and MUST NOT grant new Host capability. |
| FR-BRW-009 | Direct Chrome control SHOULD use Chrome's explicit personal-browser agent connection where supported; dsh-work MUST NOT launch the default profile with legacy remote-debugging command-line switches. |
| FR-BRW-010 | Each task MUST name its assigned browser, window and tab; operations targeting any other tab MUST be rejected until the user assigns it. |
| FR-BRW-011 | The extension and Host MUST NOT read or export raw cookies, saved passwords, browsing history or unrelated-tab content for first-release operations. |
| FR-BRW-012 | Browser connection method, profile-wide exposure, task attachment and assigned-tab state MUST be visible and immediately revocable from dsh-work and the browser-owned control surface. |

## Permission and audit

| ID | Requirement |
|---|---|
| FR-SEC-001 | Every dsh-work browser Tool MUST be explicitly claimed by a dsh-work-owned DSH `tools/pre-execute` classifier that returns `allow`, `ask` or `deny`; unclassified dsh-work Tool calls MUST fail closed. |
| FR-SEC-002 | Operations classified `ask` MUST reuse DSH `ctx.approval` and its official approval UI; only `allowed-once` may execute. |
| FR-SEC-003 | Rejection, cancellation and unavailable approval MUST be normal structured Tool results and MUST NOT be represented as a Host crash. |
| FR-SEC-004 | dsh-work MUST NOT show a second Host approval prompt for the same DSH Tool operation; browser- or OS-owned permission prompts remain separate. |
| FR-SEC-005 | Browser connection is a visible, revocable Host session grant and MUST NOT by itself approve a DSH high-impact action. |
| FR-SEC-006 | DSH approval events and Host execution events MUST share the DSH call ID and dsh-work correlation ID without duplicating secret arguments. |
| FR-SEC-007 | Audit and diagnostic output MUST redact credentials, tokens, cookies and sensitive form values. |
| FR-SEC-008 | dsh-work MUST NOT upload user content, diagnostics or audit data without a separate explicit user action. |
| FR-SEC-009 | Page content, an extension message or a Worker plugin MUST NOT change the dsh-work Tool risk classification or manufacture an approval outcome. |

## Recovery and diagnostics

| ID | Requirement |
|---|---|
| FR-REC-001 | Repeated startup failure MUST offer a safe-mode path that bypasses optional integrations without deleting user-owned data. |
| FR-REC-002 | Recovery actions MUST state whether they change configuration, create a backup or affect running tasks. |
| FR-REC-003 | Diagnostic export MUST support preview, redaction and an explicit local destination. |
| FR-REC-004 | Error displays MUST include a stable code, summary, likely cause and next action. |
| FR-REC-005 | dsh-work MUST preserve the original configuration until a recovery change has been confirmed successful. |
| FR-REC-006 | The startup surface MUST show bounded, redacted DSH stdout/stderr for the active or last startup attempt and MUST provide a local copy action. |

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
| NFR-NOT-001 | Desktop notification delivery MUST be isolated behind a platform-neutral dsh-work interface and separate native adapters for the declared desktop targets. |
| NFR-NOT-002 | Notification delivery failure MUST remain bounded and MUST NOT change the source DSH or dsh-work event outcome. |

## Requirement changes

Changing a `MUST` requirement requires the same change to update its acceptance case and any affected state, security or architecture document.
