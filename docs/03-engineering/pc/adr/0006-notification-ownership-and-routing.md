# ADR-0006: Work-owned desktop notifications with a structured DSH event bridge

- Status: Accepted
- Date: 2026-09-03

## Context

Work needs to notify a user when DSH finishes work, waits for a decision or
fails while its Workspace window is hidden. DSH already has contextual notices
and toast surfaces, but those are part of the DSH Web UI and are useful because
they retain conversation context. The current DSH shell does not expose a
stable browser or operating-system notification channel. Community plugins can
add one, but using one as a required dependency would create duplicate delivery
and version ownership problems.

The product also needs user-controlled notification preferences from the first
implementation. Those preferences are Work-global and must apply consistently
to both trusted Work windows and the native desktop delivery path.

## Decision

1. Work owns the notification domain, desktop delivery, preference evaluation,
   foreground/background routing and deduplication.
2. DSH remains the owner of in-page notices, conversation context and any
   DSH-specific UI feedback. Work does not hide or rewrite those notices.
3. DSH events enter Work through a versioned structured bridge. The bridge must
   carry event class, event identity, source context and a verified focus target
   where one exists. DOM inspection, arbitrary log parsing and gateway
   WebSocket frame parsing are not supported integration contracts.
4. Work persists these initial notification preferences in the existing
   versioned settings document:

   ```text
   notifications.enabled
   notifications.completed
   notifications.interactionRequired
   notifications.errors
   notifications.lifecycle
   ```

5. The default policy enables action-required, completed and error desktop
   notifications, and disables lifecycle notifications. Completion and routine
   lifecycle notifications are suppressed when the Workspace is the active
   surface. DSH contextual feedback remains available regardless of Work
   desktop preferences.
6. The domain exposes a narrow delivery interface. The Wails notification
   service in the pinned Wails dependency is used only by a platform/composition
   adapter; domain packages do not import Wails or native notification types.
   Direct per-platform notification code and a required third-party DSH
   notification plugin were rejected because they add a broader or less stable
   dependency boundary than the existing Wails service adapter.
7. The first implementation does not add a notification history store or a
   separate notification menu. The bounded event identity used for deduplication
   is retained only for the active Work session until a history use case is
   specified. The fixed-capacity identity set fails closed when full; it does not
   evict old IDs and risk a replayed delivery.

## Consequences

Positive:

- Users get one predictable desktop notification policy instead of competing
  DSH and Work systems.
- The DSH Web UI keeps contextual feedback that cannot be represented well in a
  system toast.
- Settings are available from the beginning and changes take effect without a
  DSH restart.
- DSH upgrades can be tested at one bridge boundary rather than through UI
  scraping.
- Notification delivery remains replaceable across Windows, macOS and Linux.

Costs and risks:

- The bridge cannot be completed until the selected DSH version's event and
  plugin seams are verified.
- A system notification may not be available or may be disabled by the
  operating system; Work must report delivery failure without changing the DSH
  event outcome.
- Foreground detection and target focus need native-window smoke tests.
- The Wails notification service is part of a beta dependency and remains
  behind the Work adapter for future replacement.

## Rejected alternatives

- **Take over every DSH web toast:** loses DSH context and couples Work to DSH
  presentation details.
- **Scrape the DSH DOM:** breaks on UI changes and cannot provide stable event
  identity or security boundaries.
- **Inspect gateway WebSocket frames:** couples the gateway to DSH's internal
  session protocol and makes proxying a business-event authority.
- **Override the browser Notification API:** the DSH core does not currently
  provide that channel, and the override cannot replace a structured event
  contract.
- **Require a community notification plugin:** plugin lifecycle and delivery
  semantics would be outside Work's versioned ownership, with a high risk of
  duplicate notifications.
- **Add a notification centre immediately:** Work has no persistent chrome over
  the DSH workspace in the ready state; adding one before the background use
  cases are proven would create another competing surface.

## Review triggers

Revisit this decision if DSH publishes a stable, versioned desktop-notification
contract, if Work gains a persistent workspace chrome layer, or if users need
cross-session notification history. Any such change must preserve the split
between DSH contextual notices and Work desktop delivery.
