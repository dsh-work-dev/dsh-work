# PC acceptance criteria

These scenarios describe externally observable release behaviour. Exact test ownership is defined in the test plan.

## Lifecycle and supervision

### AC-001 — Single instance

Given Work is running, when the user launches it again, then no second Host or Worker is created and the existing window becomes visible and focused.

### AC-002 — Visible bounded startup

Given a compatible runtime, when Work starts, then named startup steps and bounded DSH stdout/stderr are visible, the embedded workspace is not shown before readiness, the output can be copied locally, and startup reaches `Ready` or a stable error state.

### AC-003 — Configurable tray close and quit

Given Work is ready with the default close policy, when any Work window is
closed, then that window is hidden and Work remains available in the tray even
when no Work window is visible. Given the close-to-tray setting is disabled,
when the last visible Work window is closed, then Work enters `Stopping`,
cancels active work and exits after cleanup. In either mode, choosing `Quit
DSH Work` performs the full managed shutdown.

During an explicit managed shutdown, Work hides all Work-owned windows before
waiting for Worker cleanup and exits only after the managed process boundary is
verified. If cleanup fails, Work remains available from the tray, preserves the
cleanup owner and opens a trusted recovery surface so cleanup can be retried.
An active DSH conversation, question or approval is cancelled by the Worker
boundary; Work never approves, resumes or reconstructs DSH conversation state.

### AC-004 — Loopback and trusted navigation

Given a Worker launch, then its service is reachable only through the selected loopback endpoint; when an unrelated web origin attempts HTTP or WebSocket access, it cannot read data or invoke state-changing Worker behaviour; when embedded content attempts top-level external navigation, Work blocks it and offers the system browser.

### AC-005 — Descendant cleanup

Given a Worker fixture that creates descendants, when Work quits normally or its Host fixture is force-terminated on Windows, macOS or Linux, then every process assigned to that platform's managed boundary terminates and a later launch can start cleanly.

### AC-006 — Startup failure classes

Given fixtures for missing runtime, unsupported version, bind failure, readiness timeout and early exit, when each launches, then Work remains responsive and shows the corresponding stable code and safe action.

### AC-029 — External selected DSH runtime

Given a catalog containing more than one registered DSH runtime, DSH data
directory and profile, when the user selects one launch target and a separate
Workspace context, then Work verifies the runtime and launches its external
DSH Web UI with the exact data directory and profile while passing the
Workspace only to that session or Worker generation. Normal startup performs
no package installation or profile reconciliation.

### AC-030 — Separate Settings surface with nested DSH manager

Given the DSH Workspace window is open, when the user chooses a DSH management
command or Settings, then Work opens or focuses the separate trusted Settings
window without injecting management markup into the DSH document; its rail
starts with a read-only Overview, has one top-level General page, and exposes
top-level Notifications plus shallow DSH resource pages for runtimes and DSH
data directories. `General` contains the runtime, data directory and profile
launch target but no Workspace selector. The application menu exposes Settings and Help;
Help contains Check for Updates and About Work.

### AC-034 — DSH-owned appearance

Given the selected DSH data directory stores `ui-theme.preference` as `light`, `dark` or
`system`, when a trusted Work surface opens, the operating system theme changes,
or DSH changes the preference while both windows are open, then Work uses that
preference without presenting or persisting a second Work appearance setting.

### AC-035 — Work language preference

Given Work is open, when the user chooses English, Simplified Chinese or
Japanese in General, then the choice is persisted and the trusted Work surfaces
update immediately, including dynamic status, controls, native menu and tray
labels; the external DSH workspace is not modified.

## Notifications

### AC-036 — Notification preferences are available from first use

Given a new or older Work settings document, when the user opens the top-level
`Notifications` route, then Work shows the global desktop-notification switch
and the four class switches with the documented defaults. Missing values are
defaulted without invalidating unrelated settings, and every switch persists
immediately without restarting DSH.

### AC-037 — Global and class preferences route delivery

Given a structured notification event, when the global desktop switch is off,
then no desktop delivery occurs. Given the global switch is on and the event's
class switch is off, then no desktop delivery occurs for that class. In both
cases, any DSH-owned in-page notice remains available.

### AC-038 — Foreground and background delivery

Given the Workspace is active, when a completed or routine lifecycle event
arrives, then Work does not send a duplicate desktop notification. Given the
Workspace is hidden or unfocused and the relevant preference is enabled, then
Work sends one eligible desktop notification. Action-required and error events
remain eligible only when their structured event says the DSH context needs
promotion.

### AC-039 — DSH contextual notices remain owned by DSH

Given DSH emits a contextual input, queue or message notice, when Work receives
or displays related state, then Work does not remove, rewrite or duplicate the
DSH notice. Work's desktop preference affects only Work desktop delivery.

### AC-040 — Deduplication and safe notification actions

Given the same logical event is received more than once, then Work emits at
most one desktop delivery per Work session. Given the user clicks that
notification, then Work focuses the Workspace or a verified target and does not
approve, deny or execute a DSH operation.

### AC-041 — Localised and bounded notification content

Given Work is set to English, Simplified Chinese or Japanese, when Work creates
a desktop notification, then all Work-owned copy uses the selected locale.
Given synthetic secrets, raw logs or unbounded agent text in an event, then the
desktop notification contains none of those values and remains within the
documented size bound.

### AC-042 — Delivery failure is contained

Given the operating-system notification adapter is unavailable or rejects a
delivery, then Work reports a bounded actionable diagnostic while preserving
the source event outcome and keeping DSH's in-page surface usable.

### AC-033 — Persisted close policy

Given a user changes the top-level close-to-tray setting, when Work is
restarted, then the setting is loaded from the versioned Work settings store;
the default for a missing setting is tray, an invalid settings document fails
closed to the tray-safe default and reports a recoverable Settings error, and
the setting does not alter the DSH data directory or profile data.

### AC-031 — Profile-owned plugin management

Given two DSH data directories or profiles with independent plugin state,
when the user lists, installs or removes a plugin, then the operation requires
the exact data directory and profile, delegates composition to DSH's supported
CLI and changes only that profile's association. An operation against the
active profile reports whether a restart is required.

### AC-032 — DSH data directory and runtime data safety

Given a user-owned DSH data directory that does not exist locally, Work
refuses to launch or register it and does not create the directory. Given a
selected or active runtime/data directory, Work refuses removal; catalog
removal explicitly states that managed runtime files are retained until a
future data-management action.

### AC-043 — Custom profile identity editing

Given an existing custom DSH profile, when the user changes its name from the
profile detail, then Work renames only the profile directory, preserves the
profile manifest and patch layers, keeps the selected data-directory identity, and
updates the pending launch target when it references that profile. Built-in
or active profiles cannot be renamed.

### AC-044 — Workspace context is independent of the launch target

Given one DSH data directory serves two valid DSH Workspaces, when the user
selects or creates a Workspace in the DSH Workspace surface, then Work attaches
that Workspace context to the active session without changing the persisted
runtime, data-directory or profile launch target. `General` does not offer an
editable Workspace path, and removing a Workspace registration leaves its
directory, files and sessions intact.

### AC-007 — Bounded retry

Given a repeatedly crashing Worker, when automatic recovery is attempted, then retries stop at the configured policy boundary and Work offers diagnostics and safe mode instead of looping.

## Browser and permissions

### AC-008 — Typed bridge handshake

Given a Worker plugin with the wrong credential, generation or protocol version, when it connects, then the Host rejects it and no capability is exposed.

### AC-009 — Explicit user-browser connection

Given a supported signed-in browser, Work cannot inspect or control it before the user enables and approves the selected browser-control method; after the user assigns a tab, the task can use its existing authenticated web session without receiving raw cookies or saved passwords.

### AC-010 — Basic browser workflow

Given the fixed local test site and an approved origin, when DSH requests navigate, inspect, activate and fill operations, then each returns a structured result and Activity shows redacted progress.

### AC-011 — Sensitive submit approval

Given a filled form, when a high-impact submit is requested, then the Work DSH pre-execute classifier returns `ask`, DSH's official approval UI attaches to the exact Tool call, and denial returns `denied` and sends nothing. Work does not show a duplicate approval dialog.

### AC-012 — Scope change and redirect

Given an origin-scoped grant, when navigation redirects or opens a new origin, then data-changing operations pause until the new scope is approved.

### AC-013 — Cancellation

Given a running browser task, when the user cancels it, then no new action begins, pending waits end, one `cancelled` result is returned and its tab attachment is released.

### AC-026 — Detach preserves the user's browser

Given an attached existing-profile tab, when the task completes, its task attachment is released; when the user disconnects or Work exits, the browser-control connection closes. In both cases the browser and tab remain open, login state remains intact, and unrelated tabs were neither projected to DSH nor modified.

### AC-014 — Replay and mutation resistance

Given an approved canonical request, when the request ID is replayed or its target／payload changes, then Work does not execute it a second time and records a redacted rejection event.

### AC-015 — Page content has no authority

Given a page containing instructions to bypass approval or reveal Host data, when it is inspected, then those instructions remain page data and no capability, secret or audit record is exposed.

### AC-016 — High-risk grants

Given a high-impact browser operation, DSH offers one-time approval only; closing, cancelling or unavailable approval denies the operation, and the Host revalidates the exact call, connection and tab immediately before action.

### AC-027 — Complete DSH Tool classification

Given every registered Work browser Tool and operation variant, the Work-owned `tools/pre-execute` classifier returns a deterministic `allow`, `ask` or `deny`; unknown or unclaimed Work operations fail closed. An `allowed-once` DSH outcome reaches the Host exactly once without a second Work prompt.

### AC-028 — Connection is not action approval

Given an active personal-browser connection, ordinary assigned-tab inspection and interaction follow the automatic policy, but a classified high-impact action still enters DSH approval. Disconnecting invalidates the connection and causes later calls to fail closed.

## Recovery, data and accessibility

### AC-017 — Safe mode preserves data

Given a Worker that fails because of an optional integration, when safe mode starts, then the Host becomes usable, the optional integration is bypassed, and hashes of user-owned DSH data remain unchanged.

### AC-018 — Configuration migration rollback

Given an older supported Host configuration, when migration fails validation or first startup, then the prior configuration remains recoverable and Work reports the failure without partial replacement.

### AC-019 — Diagnostic export

Given synthetic secrets across Host, Worker and browser error fixtures, when diagnostics are previewed and exported, then none of the synthetic values appear and no network upload occurs.

### AC-020 — Local storage isolation

Given a normal task and shutdown, then Host files stay in the per-user application-data area, user Workspaces and DSH data directories are not silently rewritten, and global environment or package-manager settings are unchanged.

### AC-021 — Keyboard operation

Given only keyboard input, the user can start, inspect status, approve or deny, cancel, open Diagnostics, export a report and quit with visible focus and correct focus restoration.

### AC-022 — Assistive status

Given a screen reader and non-colour visual mode, lifecycle, task, approval and error states remain distinguishable and meaningful status changes are announced without streaming-log noise.

### AC-023 — Deterministic terminal results

Given late, duplicate or out-of-order Worker and browser events, each startup and task produces exactly one terminal result and old-generation events do not alter current state.

### AC-024 — Secret storage

Given a secret that must persist, when Work saves and later reads it, then only the operating-system protected store contains the secret and ordinary configuration, logs and diagnostics do not.

### AC-025 — Clean install and uninstall

Given clean supported Windows, macOS and Linux environments, installation and first launch complete through documented steps; uninstall states what data remains and does not silently remove user-owned Workspaces or DSH data directories.

## Requirement coverage

| Requirements | Acceptance cases |
|---|---|
| FR-LIFE-001 | AC-001 |
| FR-LIFE-002, FR-LIFE-004, FR-LIFE-005 | AC-002, AC-006, AC-023 |
| FR-LIFE-003 | AC-003, AC-033 |
| FR-SUP-001, FR-SUP-002 | AC-002, AC-006 |
| FR-SUP-003, FR-SUP-004 | AC-002, AC-004 |
| FR-SUP-005 | AC-006, AC-019 |
| FR-SUP-006, FR-SUP-007, FR-SUP-010 | AC-003, AC-005 |
| FR-SUP-008, FR-SUP-009 | AC-006, AC-007 |
| FR-MGR-001, FR-MGR-002, FR-MGR-003 | AC-002, AC-029, AC-044 |
| FR-MGR-004 | AC-031 |
| FR-MGR-005 | AC-030 |
| FR-MGR-006, FR-MGR-007 | AC-032, AC-025 |
| FR-MGR-008 | AC-034 |
| FR-MGR-009 | AC-035 |
| FR-MGR-010 | AC-043 |
| FR-MGR-011, FR-MGR-012 | AC-044 |
| FR-NOT-001, FR-NOT-002, FR-NOT-003 | AC-036 |
| FR-NOT-004 | AC-037 |
| FR-NOT-005 | AC-038 |
| FR-NOT-006, FR-NOT-008 | AC-040 |
| FR-NOT-007 | AC-038, AC-039 |
| FR-NOT-009 | AC-041 |
| FR-NOT-010 | AC-036, AC-041 |
| FR-WEB-001, FR-WEB-002, FR-WEB-005 | AC-004 |
| FR-WEB-003 | AC-006 |
| FR-WEB-004 | AC-008, AC-015 |
| FR-BRW-001 | AC-008, AC-010, AC-023 |
| FR-BRW-002 | AC-010 |
| FR-BRW-003, FR-BRW-009, FR-BRW-011 | AC-009 |
| FR-BRW-004, FR-BRW-005 | AC-011, AC-012, AC-016 |
| FR-BRW-006 | AC-013 |
| FR-BRW-007, FR-BRW-010, FR-BRW-012 | AC-009, AC-013, AC-026 |
| FR-BRW-008 | AC-015 |
| FR-SEC-001 | AC-027 |
| FR-SEC-002, FR-SEC-003 | AC-011, AC-016, AC-027 |
| FR-SEC-004 | AC-011, AC-027 |
| FR-SEC-005 | AC-009, AC-028 |
| FR-SEC-006, FR-SEC-007 | AC-014, AC-019 |
| FR-SEC-008 | AC-019 |
| FR-SEC-009 | AC-015, AC-027 |
| FR-REC-001, FR-REC-002 | AC-017 |
| FR-REC-003, FR-REC-004 | AC-006, AC-019 |
| FR-REC-005 | AC-017, AC-018 |
| FR-REC-006 | AC-002, AC-019 |
| NFR-REL-001, NFR-REL-002 | AC-002, AC-013, AC-023 |
| NFR-SEC-001 | AC-004, AC-008 |
| NFR-SEC-002 | AC-024 |
| NFR-ACC-001 | AC-021, AC-022 |
| NFR-OBS-001 | AC-006, AC-010, AC-019 |
| NFR-MNT-001 | AC-006, AC-008, AC-010 |
| NFR-MNT-002 | AC-008, AC-018 |
| NFR-PORT-001 | AC-005, AC-025 |
| NFR-NOT-001, NFR-NOT-002 | AC-040, AC-042 |
