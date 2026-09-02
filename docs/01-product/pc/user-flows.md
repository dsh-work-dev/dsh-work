# PC user flows

## UF-01 — First launch

1. User starts Work.
2. Work checks its own configuration and DSH runtime compatibility.
3. The window shows `Starting` with the current step, bounded DSH output and a cancel action.
4. User can copy the redacted output when startup needs support.
5. Work starts the Worker and waits for a validated readiness signal.
6. On success, Work navigates the embedded view to the loopback Worker URL and enters `Ready`.
7. On failure, Work shows a stable error code, plain-language cause and safe next actions.

The user never needs to choose a port or inspect a terminal.

## UF-02 — Return from tray

1. Closing the window applies the close-to-tray setting; by default it hides
   the window while Work remains available in the system tray.
2. The tray indicates current state without relying on colour alone.
3. Selecting `Open Workspace` restores and focuses the Workspace window.
4. Selecting `Quit DSH Work` starts a full managed shutdown and exits only
   after cleanup finishes or a bounded fallback completes.

## UF-02A — Manage Work settings

1. User opens `Settings` from the application menu or system tray.
2. Work opens the separate trusted Settings window on its flat top-level
   `General` route; `Overview` is read-only and DSH workspace markup is not
   changed.
3. User changes the launch target or close-to-tray preference. The launch
   target uses an explicit save action; close-to-tray persists immediately.
4. The next window close applies the selected policy; explicit `Quit DSH Work`
   remains a full shutdown in either mode.

## UF-02B — Manage desktop notifications

1. User opens `Settings` from the application menu or system tray.
2. Work opens the separate trusted Settings window on the flat top-level
   `Notifications` route.
3. User turns desktop delivery or an individual notification class on or off.
4. Work persists each switch immediately and applies it to future desktop
   deliveries without restarting DSH.
5. DSH in-page notices continue to follow DSH's own contextual UI rules.

## UF-02C — Receive a desktop notification

1. DSH or Work emits a structured action-required, completed, error or enabled
   lifecycle event.
2. Work checks the event identity, current window state and notification
   preferences.
3. If delivery is eligible, Work sends one bounded, localised desktop
   notification.
4. User selects the notification; Work focuses the Workspace or its verified
   target without approving or executing any operation.

## UF-03 — Run a browser task

1. DSH requests a browser operation through the registered Work tool.
2. Work validates the request schema, capability and target scope.
3. If no browser is connected, Work asks the user to enable the browser's personal-agent connection and approve its browser-owned prompt.
4. Work connects to the current profile, then the user assigns the tab AI may control; the existing login state remains available.
5. Low-risk steps execute with visible progress.
6. A sensitive submit, download, authentication transition or expanded scope pauses through DSH's approval flow.
7. The user approves or rejects the exact DSH tool call.
8. Work returns a structured result to DSH and records a redacted execution event.
9. User can cancel at any time; cancellation stops pending actions, detaches control and returns a terminal result without closing the browser.

## UF-03A — Connect the user's browser

1. User opens Browser Connection in Work.
2. Work detects supported installed browsers and available connection methods.
3. For supported Chrome, Work guides the user to enable personal-browser remote debugging and the user accepts Chrome's connection prompt.
4. Where auto-connect is unavailable or finer tab scope is preferred, Work offers the signed extension／native-host adapter.
5. User selects an existing tab or asks Work to create a new tab; Work explains that the underlying personal-browser connection may technically access the whole profile.
6. Browser displays its own debugging or extension indicator while control is connected.
7. Work shows browser, profile label, connection method, window and assigned tab; unrelated tabs stay outside the DSH task context.
8. User can disconnect at any time from Work or the browser-owned control surface.

## UF-04 — Startup failure and recovery

1. The Worker exits or fails readiness.
2. Work keeps its native shell responsive and enters `Failed`.
3. Work records diagnostics and offers `Retry`, `Start in safe mode`, `View details` and `Quit` where applicable.
4. Safe mode launches through a separate recovery configuration without deleting the normal configuration.
5. Work explains which optional integration was bypassed and how to return to normal mode.

## UF-05 — Export diagnostics

1. User opens Diagnostics.
2. Work lists the categories to include and flags fields removed by redaction.
3. User previews the generated report.
4. User chooses an explicit destination with the native save dialog.
5. Work confirms the saved path; it never uploads the report automatically.

## UF-06 — Application update or runtime mismatch

1. Work detects that the configured runtime is unsupported or unavailable.
2. The normal launch stops before user data is modified.
3. Work explains what component needs attention and offers only supported remediation paths.
4. After remediation, the user retries the compatibility check and launch.

Automatic application updating is not required for the first release; the flow defines the required failure behaviour around incompatible versions.
