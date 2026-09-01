# PC interaction specification

## Application shell

The Host chrome contains application status and entry points for Activity, Diagnostics and Settings. The embedded workspace uses the remaining area. Host controls remain available even if Worker content is loading or failed.

### Window close and quit

- Window close hides Work to the tray and announces that it is still running the first time this happens.
- `Quit` is distinct from close and initiates managed shutdown.
- During shutdown, the tray and window show `Stopping`; duplicate quit actions are ignored.
- If a task is active, quit explains that it will cancel the task and detach browser control; it does not close the user's browser.

## Startup surface

Show a determinate sequence of named steps, even when individual step duration is unknown:

1. checking configuration;
2. checking runtime compatibility;
3. starting Worker;
4. waiting for readiness;
5. opening workspace.

The surface includes elapsed context, a cancel action where cancellation is safe, and a Diagnostics link. It never shows a blank WebView as startup feedback.

## Tray

Tray status uses icon shape plus text, not colour alone. Its menu always offers `Open Work` and `Quit`. `Diagnostics` is available in failure states. An active approval is surfaced as `Action required` and selecting it focuses the trusted approval surface.

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
