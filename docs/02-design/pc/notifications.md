# PC notification interaction design

## Surface model

```text
DSH event or dsh-work event
        │
        ▼
structured notification event
        │
        ├── DSH contextual notice (DSH owns)
        │
        └── dsh-work policy
              ├── preference check
              ├── foreground/background check
              ├── deduplication
              └── desktop notification (dsh-work owns)
```

There is no persistent dsh-work notification panel in the first version. This keeps
the Workspace window free of an extra dsh-work chrome layer and avoids competing
with DSH's conversation context.

## Settings route

The Settings window remains a separate native window with no menu bar. Its rail
is flat and shallow. `Notifications` is one top-level route, alongside the
existing dsh-work and DSH management routes; it is not a child route of `General`.

The page contains:

1. the page title `Notifications`;
2. the `Desktop notifications` switch;
3. four category switches;
4. no save button for individual switches;
5. no visible success message after an automatic save.

Controls are labelled by the result they control. Supporting copy is used only
when a category would otherwise be ambiguous.

## Delivery matrix

| Event | Workspace active | Workspace hidden or unfocused | Preference off |
|---|---|---|---|
| Action required | DSH context plus dsh-work delivery when needed | Desktop delivery | DSH context only |
| Completed | No duplicate desktop delivery | Desktop delivery | No desktop delivery |
| Error | Contextual DSH or dsh-work error surface | Desktop delivery | Source surface only |
| Lifecycle | No delivery by default | Only if enabled | No desktop delivery |

The bridge supplies the context needed to distinguish an event already visible
in DSH from an event that needs promotion to the desktop. dsh-work must not infer
that state by searching DSH DOM text.

## Desktop notification action

- The default action focuses the Workspace window.
- An action-required event focuses the matching DSH call or session when the
  bridge supplies a verified target.
- If no verified target exists, the action opens the Workspace window without
  claiming that it located a specific message.
- Clicking a notification does not approve, deny or execute a DSH operation.

## Visual language

System notifications use the operating-system visual language. Any dsh-work-owned
transient fallback uses the existing neutral surface tokens:

- no blue accent line or left rail;
- no gradient, glow, glass, oversized icon or decorative badge;
- one concise title and one concise body;
- status is communicated by text and a familiar icon, not colour alone;
- the fallback is placed near the active dsh-work control or state it describes;
- errors remain visible until the user can reach the recommended action, while
  routine completion feedback is short-lived.

## Accessibility

- Every switch exposes its name, checked state and disabled state.
- Preference changes are announced only as a state change, not as a repeated
  paragraph of confirmation.
- Desktop notification action labels are verbs and remain meaningful without
  colour.
- Reduced-motion settings remove entrance animation from any dsh-work fallback.
- Notification delivery failures are presented as an actionable dsh-work error,
  never as an unexplained silent failure.
