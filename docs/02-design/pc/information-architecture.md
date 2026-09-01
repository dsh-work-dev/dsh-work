# PC information architecture

## Surface map

```text
Work desktop shell
├── Startup / recovery surface
│   ├── current startup step
│   ├── error details
│   └── retry / safe mode / quit
├── DSH workspace
│   ├── embedded Worker view
│   └── DSH Tool call and approval UI
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
└── Settings
    ├── runtime
    ├── permissions
    ├── browser connection and controlled tabs
    └── accessibility

System tray
├── status
├── open Work
├── active task summary
├── diagnostics
└── quit
```

## Navigation model

- The DSH workspace is the default ready-state destination.
- Startup and failure surfaces replace, rather than overlay, untrusted or unavailable Worker content.
- Activity opens as a drawer so the user can observe or cancel a task without losing DSH context.
- Model-action approval stays in DSH's official Tool call UI; Host status and tray actions focus the matching call instead of drawing a duplicate dialog.
- Diagnostics and Settings are Host-owned routes. They remain available when the Worker is down.
- External web pages never become Host navigation destinations.

## Ownership cues

The UI must make the boundary between Work and embedded DSH content understandable:

- Host-owned chrome contains status, Activity, Settings and Diagnostics.
- Worker content is framed as the workspace and has no direct access to Host APIs.
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
