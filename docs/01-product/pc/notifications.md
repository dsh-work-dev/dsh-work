# PC notification product specification

Status: accepted for the first notification implementation.

## Product boundary

Work provides desktop notifications for meaningful DSH and Work events. DSH
continues to render contextual notices inside its own Web UI. The Work setting
controls desktop delivery only; it does not hide DSH feedback.

The first version does not add a notification history view or a separate
notification menu. Desktop delivery is the primary background surface, while
the DSH workspace remains the primary foreground surface.

## Notification classes

| Class | Examples | Default desktop delivery | User can change |
|---|---|---:|---:|
| `action-required` | question, approval, confirmation | On | Yes |
| `completed` | finished reply, completed task | On when Work is not active | Yes |
| `error` | DSH error, Worker exit, startup or gateway failure | On when Work is not active | Yes |
| `lifecycle` | starting, restarting, stopped | Off | Yes |

Successful settings saves and ordinary progress updates are not notification
classes.

## Notification settings

Settings gets one new top-level destination named `Notifications`. It contains
one compact list:

| Label | Default | Effect |
|---|---:|---|
| Desktop notifications | On | Enables Work desktop delivery. |
| Task completed | On | Allows `completed` desktop notifications. |
| Needs attention | On | Allows `action-required` desktop notifications. |
| Errors | On | Allows `error` desktop notifications. |
| DSH status | Off | Allows `lifecycle` desktop notifications. |

There is no nested notification settings page, separate appearance setting,
or explanatory paragraph repeating the labels. Sound follows the operating
system and is not an additional Work preference in this version.

## Behaviour rules

1. The global switch is evaluated before every desktop delivery.
2. A class switch is evaluated after the global switch.
3. `completed` and routine `lifecycle` events are suppressed while the
   Workspace window is the active user surface.
4. `action-required` and `error` remain eligible while the Workspace is active
   when the DSH surface cannot provide the required attention signal; the
   bridge must declare this condition rather than guessing from page markup.
5. Turning desktop notifications off never removes an in-page DSH notice.
6. A preference change applies immediately to future events and does not restart
   DSH.
7. One logical event produces at most one desktop delivery per Work session.
8. Notification text is translated using the current Work locale. The DSH
   workspace remains under DSH's own language and rendering ownership.

## Persistence

The preferences are part of the versioned Work settings document:

```text
notifications.enabled
notifications.completed
notifications.interactionRequired
notifications.errors
notifications.lifecycle
```

For older settings documents, missing values use the defaults in this document.
Invalid notification values fail closed to the safe defaults and leave the
rest of the settings document recoverable.

## Content rules

- Title states the event in a few words.
- Body states the affected DSH context and the next useful action.
- No raw process output, stack trace, token, cookie, secret or unbounded agent
  text is placed in a desktop notification.
- Completion copy is informative, not celebratory or promotional.
- Errors use one clear next action; technical detail remains in Diagnostics.
- Auto-save success remains silent; failure stays attached to the setting that
  failed.

## Internationalisation

Every Work-owned title, body fragment, setting label and accessibility label
must exist in English, Simplified Chinese and Japanese before the feature is
enabled. DSH-provided contextual text is not translated or rewritten by Work.
