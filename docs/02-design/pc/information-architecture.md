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
│   ├── Profiles
│   ├── Runtimes
│   └── DSH data directories
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
- The Settings window is opened separately and never replaces or overlays DSH content. It has no HTML application menu. Its rail is one flat list of top-level destinations; `General` contains the DSH runtime, DSH data directory and profile Run context plus close-to-tray behaviour, while `Notifications` contains Work desktop-notification preferences. `General` does not contain Workspace selection.
- `Overview` is first and is read-only. It reports the current/known-good Run context, any context-switch state and the Workspace context separately; it does not contain inputs or a save-for-next-launch action.
- `Profiles` is the primary profile resource page. The left side lists profiles; selecting one establishes an inspection context for the right-side profile detail. An explicit `Switch to this profile` action changes the Run context immediately; General's runtime and data-directory selectors are not reused as plugin context.
- A profile must be selected before plugin details are shown. Non-current profiles are inspectable but read-only. Only the profile in the current `Ready` Run context exposes install, removal and other plugin-composition actions; every mutation carries the exact data-directory/profile reference and is checked again by the manager.
- Custom profile names can be changed from profile detail. Built-in DSH profile names remain fixed. A rename changes only the profile directory identity and updates the Configured Run context when it points at that profile; the current profile cannot be renamed while running.
- Runtimes and DSH data directories remain shallow resource-management destinations. A complete runtime/data-directory/profile selection starts an immediate atomic context switch, with automatic rollback on failure.
- Workspace selection and creation belong to the DSH Workspace surface or an explicit start/session action. Settings pages use one continuous reading column; only related short fields in a form may sit side by side, while long data-directory paths and read-only current-Workspace values span the content width.
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
- Work appearance follows the selected DSH data directory's `ui-theme.preference`; Work has no separate appearance setting. `system` follows the operating system. When both trusted windows are open, the Settings window reflects DSH preference changes without a manual refresh.
- The Workspace window contains the external DSH content; Worker content has no direct access to Host APIs. Work's native application menu remains available for Settings and Help, while lifecycle actions remain in the system tray.
- Plugin management is always labelled with its DSH data directory and profile reference. The profile list shows the profile name as the primary identity and the data directory as scope; plugin names appear inside the selected profile's detail, with mutation actions only when that profile is current.
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
five-step lifecycle sequence, the trusted current-Workspace facts, bounded DSH
process output and then the available host actions. The output is readable and
copyable, while remaining redacted and bounded. The surface fills the window as
a control surface rather than centring the content inside a decorative card.
