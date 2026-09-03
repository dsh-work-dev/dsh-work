# PC interaction specification

## Application shell

Work uses two native WebView windows. The separate Settings window contains a
flat, shallow rail and no HTML application menu: `Overview` first and read-only, one top-level `General`
page for launch target and close-to-tray behaviour, one top-level `Notifications`
page for Work desktop-notification preferences, then DSH resource pages for
profiles, runtimes and homes. The Workspace window contains the
external DSH Web UI. Work's native application menu and system tray remain
Host-owned; no Work HTML or JavaScript is injected into the DSH document. Host state
and recovery actions remain available even if Worker content is loading or
failed.

The native settings command and window title are `设置`; the Workspace window
title is `dsh-work`.

Work does not own an appearance setting. It reads the selected DSH home's
`ui-theme.preference`; `system` resolves through the operating system and
changes are reflected in Work's trusted surfaces. If DSH changes the
preference while the Settings window is open, the window updates automatically.

Work's chrome supports English, Simplified Chinese and Japanese. Language is a
Work preference in General; switching it updates both trusted WebView surfaces
and the native menu/tray through the same locale event. The default locale is
Simplified Chinese, and the DSH workspace remains the owner of its own content
and language.

The `Profiles` page uses the profile as its primary entity. The profile list
selects one explicit home/profile reference for the detail pane; it does not
reuse the launch selectors in `General`. With no selected profile, the plugin
list and plugin actions are absent. Whenever a plugin is displayed, its
selected-profile detail contains its management action, including removal.
The active Workspace profile may be the initial selection, but it is not an
implicit backend target.

The profile detail also edits custom profile names. Built-in names are fixed.
Because DSH 0.1.2 has no public rename command, Work changes only the custom
profile directory identity, preserves its manifest and patch layers, blocks
renaming the active profile, and updates the pending launch selection when
needed.

Overview distinguishes the active session from the pending next-launch
selection. It never presents the pending selection as if it were the running
session.

### Window close and quit

- The default close policy hides the closed Work window. When all Work windows
  are hidden, the process remains available from the tray.
- If the user disables the close-to-tray setting, closing the last visible Work
  window initiates managed shutdown instead of hiding it.
- `Quit DSH Work` is always distinct from close and initiates managed shutdown.
- During shutdown, the tray and window show `Stopping`; duplicate quit actions are ignored.
- If a task is active, quit explains that it will cancel the task and detach browser control; it does not close the user's browser.

## Startup surface

Show a named five-step sequence and the active step, even when individual step duration is unknown:

1. checking configuration;
2. checking runtime compatibility;
3. starting Worker;
4. waiting for readiness;
5. opening workspace.

Completed steps, the current step and a failed step remain visually distinct
through text, structure and focus—not colour alone. A small progress indicator
may reflect completed lifecycle steps, but MUST NOT invent a percentage or time
estimate. The surface includes a cancel action where cancellation is safe and a
bounded, redacted DSH stdout/stderr view with a `Copy` action. A Diagnostics
entry is available when diagnostics exist. It never shows a blank WebView as
startup feedback.

### Application menu

The application menu exposes `Settings` and `Help`. `Help` contains
`Check for Updates…` and `About Work`. The update command must report its
availability truthfully until an update channel is implemented; it must not
claim that a check succeeded when no service exists.

## Notifications

Work desktop notifications are a background companion to the DSH workspace, not
a second conversation surface. DSH keeps its contextual notices and toast
feedback. Work owns desktop delivery, preference checks, foreground/background
routing and event deduplication.

The top-level Settings route `Notifications` contains these immediate-save
switches:

- `Desktop notifications`;
- `Task completed`;
- `Needs attention`;
- `Errors`;
- `DSH status`.

Defaults are on for desktop notifications, completed tasks, needs attention and
errors; DSH status is off. Sound follows the operating system. There is no
visible success confirmation after a switch saves. A disabled desktop channel
does not remove a DSH in-page notice.

Completion and routine lifecycle events are suppressed while the Workspace is
the active surface. When it is hidden or unfocused, an enabled class may reach
the operating-system notification surface. Action-required and error events
may also reach that surface when the DSH context cannot provide the required
attention signal. A notification click focuses the Workspace; it never approves
or executes a DSH operation.

See the [notification interaction design](notifications.md) and [notification product specification](../../01-product/pc/notifications.md) for the complete delivery matrix and content rules.

## Tray

Tray status uses icon shape plus text, not colour alone. Its menu offers
`Open Workspace`, `Settings`, `Restart DSH` and `Quit DSH Work`.
`Diagnostics` is available in failure states. An active approval is
surfaced as `Action required` and selecting it focuses the trusted approval
surface.

## Activity drawer

For an active browser task, show:

- task title supplied by DSH, labelled as untrusted text;
- current step described as an action and target;
- session identity and target origin;
- elapsed status without promising a completion time;
- `Cancel task`.

Completed steps use concise summaries. Sensitive field values, cookies and credentials are never displayed in step history.

## DSH approval integration

Work does not render a second approval dialog. For operations classified `ask`, DSH's official Tool call and approval UI includes:

- requester: DSH agent/tool identity;
- action: plain-language operation;
- target: origin, file, protocol or other concrete resource;
- scope: what subsequent steps the grant covers;
- risk: likely effect and whether data leaves the computer;
- outcome: allow once or deny.

`Deny` remains available. Only one-time approval executes. Host `Waiting for approval` state and tray `Action required` focus the exact DSH call ID. If the approval is closed, cancelled, unavailable or cannot be correlated, the operation fails closed.

## User-browser connection visibility

- Work identifies the connected browser, profile label, window and controlled tab without displaying account secrets.
- Control begins only after the user enables and approves a supported browser connection, then assigns a tab.
- Work shows whether browser control is connected, busy or disconnected; browser-owned debugging／extension indicators remain visible.
- Disconnecting stops new operations and detaches debugging without signing the user out, clearing cookies or closing the browser.
- Authentication occurs in the user's browser UI; Work never asks the user to copy cookies or passwords into Work.
- Work explains that direct personal-browser control may technically expose the profile, while unrelated windows and tabs are not projected into DSH context or captured for diagnostics.

## Error interaction

Primary error layout:

1. short summary;
2. stable error code;
3. one recommended safe action;
4. alternative recovery actions;
5. expandable technical details.

Retry is disabled while a previous process tree is still being cleaned up. Destructive recovery actions, if added later, require preview and confirmation.

## Keyboard and assistive technology

- All commands are reachable without a pointing device.
- Focus moves into a newly opened trusted modal and returns to its invoker on close.
- Status changes use an appropriate live region without announcing streaming log noise.
- Escape denies an approval only after the UI makes that consequence clear.
- Browser task progress, errors and tray state have text equivalents.
- Motion follows the operating-system reduced-motion preference.
