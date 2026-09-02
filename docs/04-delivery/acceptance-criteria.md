# PC acceptance criteria

These scenarios describe externally observable release behaviour. Exact test ownership is defined in the test plan.

## Lifecycle and supervision

### AC-001 — Single instance

Given Work is running, when the user launches it again, then no second Host or Worker is created and the existing window becomes visible and focused.

### AC-002 — Visible bounded startup

Given a compatible runtime, when Work starts, then named startup steps are visible, the embedded workspace is not shown before readiness, and startup reaches `Ready` or a stable error state.

### AC-003 — Tray close and quit

Given Work is ready, when the window is closed, then Work remains available in the tray; when `Quit` is chosen, Work enters `Stopping`, cancels active work and exits after cleanup.

### AC-004 — Loopback and trusted navigation

Given a Worker launch, then its service is reachable only through the selected loopback endpoint; when an unrelated web origin attempts HTTP or WebSocket access, it cannot read data or invoke state-changing Worker behaviour; when embedded content attempts top-level external navigation, Work blocks it and offers the system browser.

### AC-005 — Descendant cleanup

Given a Worker fixture that creates descendants, when Work quits normally or its Host fixture is force-terminated on Windows, macOS or Linux, then every process assigned to that platform's managed boundary terminates and a later launch can start cleanly.

### AC-006 — Startup failure classes

Given fixtures for missing runtime, unsupported version, bind failure, readiness timeout and early exit, when each launches, then Work remains responsive and shows the corresponding stable code and safe action.

### AC-029 — External selected DSH runtime

Given a catalog containing more than one registered DSH runtime, home and profile, when the user selects one complete launch tuple, then Work verifies that runtime and launches its external DSH Web UI with the exact home, profile and workspace after readiness. Normal startup performs no package installation or profile reconciliation.

### AC-030 — Separate Manager surface and native Work menu

Given the DSH Workspace window is open, when the user chooses a Work management command, then Work opens or focuses a separate Manager window without injecting management markup into the DSH document; the native menu also offers restart and quit commands.

### AC-031 — Profile-owned plugin management

Given two DSH homes or profiles with independent plugin state, when the user lists, installs or removes a plugin, then the operation requires the exact DSH home and profile, delegates composition to DSH's supported CLI and changes only that profile's association. An operation against the active profile reports whether a restart is required.

### AC-032 — DSH home and runtime data safety

Given a user-owned DSH home that does not exist locally, Work refuses to launch or register it and does not create the directory. Given a selected or active runtime/home, Work refuses removal; catalog removal explicitly states that managed runtime files are retained until a future data-management action.

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

Given a normal task and shutdown, then Host files stay in the per-user application-data area, user workspaces and DSH homes are not silently rewritten, and global environment or package-manager settings are unchanged.

### AC-021 — Keyboard operation

Given only keyboard input, the user can start, inspect status, approve or deny, cancel, open Diagnostics, export a report and quit with visible focus and correct focus restoration.

### AC-022 — Assistive status

Given a screen reader and non-colour visual mode, lifecycle, task, approval and error states remain distinguishable and meaningful status changes are announced without streaming-log noise.

### AC-023 — Deterministic terminal results

Given late, duplicate or out-of-order Worker and browser events, each startup and task produces exactly one terminal result and old-generation events do not alter current state.

### AC-024 — Secret storage

Given a secret that must persist, when Work saves and later reads it, then only the operating-system protected store contains the secret and ordinary configuration, logs and diagnostics do not.

### AC-025 — Clean install and uninstall

Given clean supported Windows, macOS and Linux environments, installation and first launch complete through documented steps; uninstall states what data remains and does not silently remove user-owned workspaces or DSH homes.

## Requirement coverage

| Requirements | Acceptance cases |
|---|---|
| FR-LIFE-001 | AC-001 |
| FR-LIFE-002, FR-LIFE-004, FR-LIFE-005 | AC-002, AC-006, AC-023 |
| FR-LIFE-003 | AC-003 |
| FR-SUP-001, FR-SUP-002 | AC-002, AC-006 |
| FR-SUP-003, FR-SUP-004 | AC-002, AC-004 |
| FR-SUP-005 | AC-006, AC-019 |
| FR-SUP-006, FR-SUP-007, FR-SUP-010 | AC-003, AC-005 |
| FR-SUP-008, FR-SUP-009 | AC-006, AC-007 |
| FR-MGR-001, FR-MGR-002, FR-MGR-003 | AC-002, AC-029 |
| FR-MGR-004 | AC-031 |
| FR-MGR-005 | AC-030 |
| FR-MGR-006, FR-MGR-007 | AC-032, AC-025 |
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
| NFR-REL-001, NFR-REL-002 | AC-002, AC-013, AC-023 |
| NFR-SEC-001 | AC-004, AC-008 |
| NFR-SEC-002 | AC-024 |
| NFR-ACC-001 | AC-021, AC-022 |
| NFR-OBS-001 | AC-006, AC-010, AC-019 |
| NFR-MNT-001 | AC-006, AC-008, AC-010 |
| NFR-MNT-002 | AC-008, AC-018 |
| NFR-PORT-001 | AC-005, AC-025 |
