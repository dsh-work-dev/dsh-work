# PC information architecture

## Surface map

```text
Work desktop shell
├── Startup / recovery surface
│   ├── current startup step
│   ├── error details
│   └── retry / safe mode / quit
├── Workspace window
│   ├── external DSH Worker view
│   ├── DSH Tool call and approval UI
│   └── Host lifecycle handoff
├── Settings window
│   ├── Overview (read-only)
│   ├── General (one Work settings page)
│   ├── Notifications
│   ├── Profiles & plugins
│   ├── Runtimes
│   └── DSH homes
├── Activity drawer
│   ├── active browser task
│   ├── step progress
│   └── cancel action
├── Approval focus bridge
│   ├── action-required status
│   └── focus matching DSH call card
├── Diagnostics
│   ├── runtime status
│   ├── recent redacted events
│   └── preview / export

System tray
├── status
├── open Workspace
├── Settings
├── Restart DSH
└── Quit DSH Work
```

## Navigation model

- The DSH workspace window is the default ready-state destination.
- The Settings window is opened separately and never replaces or overlays DSH content. It has no HTML application menu. Its rail is one flat list of top-level destinations; `General` contains launch target plus close-to-tray behaviour, while `Notifications` contains Work desktop-notification preferences.
- `Overview` is first and is read-only. It reports the selected runtime, home, profile, workspace and launch state; it does not contain inputs or a save action.
- Profiles, runtimes and DSH homes remain shallow resource-management destinations. Plugin actions always carry an explicit home/profile context.
- Settings pages use one continuous reading column; only related short fields in a form may sit side by side, while long paths and workspace values span the content width.
- Native menu commands and deep links preserve the requested destination; an absent or invalid section defaults to `Overview`.
- Startup and failure surfaces replace unavailable Worker content in the Workspace window.
- Activity opens as a drawer so the user can observe or cancel a task without losing DSH context.
- Model-action approval stays in DSH's official Tool call UI; Host status and tray actions focus the matching call instead of drawing a duplicate dialog.
- Diagnostics and Settings are Host-owned routes. They remain available when the Worker is down.
- External web pages never become Host navigation destinations.

## Ownership cues

The UI must make the boundary between Work and embedded DSH content understandable:

- Host-owned surfaces contain status, Activity, Settings, Diagnostics and the DSH manager inside the Settings window.
- The application menu has only top-level `Settings` and `Help`; `Help` contains `Check for Updates…` and `About Work`. The system tray remains a compact lifecycle surface.
- Work owns desktop notification preferences and delivery. DSH owns in-page notices and conversation context. The first release has no persistent Work notification panel or notification menu.
- Work appearance follows the selected DSH home's `ui-theme.preference`; Work has no separate appearance setting. `system` follows the operating system. When both trusted windows are open, the Settings window reflects DSH preference changes without a manual refresh.
- The Workspace window contains the external DSH content; Worker content has no direct access to Host APIs. Work's native application menu remains available for Settings and Help, while lifecycle actions remain in the system tray.
- Plugin management is always labelled with its DSH home and profile reference.
- DSH approval is trusted only when correlated with the active DSH call ID and official approval channel; ordinary page content cannot create Host action-required state.
- A disconnected Worker is covered by a Host-owned recovery page to prevent stale content from appearing usable.

## Content hierarchy

For every operational message, present information in this order:

1. what state Work is in;
2. what is happening or failed;
3. whether user action is required;
4. safest primary action;
5. technical details behind disclosure.

Raw process output and stack traces belong in Diagnostics, not in primary error copy.

The startup surface uses the same order spatially: a named current state, a
five-step lifecycle sequence, the trusted workspace facts, bounded DSH process
output and then the available host actions. The output is readable and
copyable, while remaining redacted and bounded. The surface fills the window as
a control surface rather than centring the content inside a decorative card.
