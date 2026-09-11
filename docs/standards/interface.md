# Interface standards

## Ownership and hierarchy

- Trusted dsh-work surfaces must remain usable while DSH is starting, unavailable
  or failed.
- The DSH Workspace remains DSH-owned and receives no injected Host controls.
- Host and Workspace ownership must be perceptible without adding decorative
  chrome that competes with the primary task.
- Settings is a trusted, shallow management surface; the tray is a compact
  lifecycle companion, not another full application.
- Add a separate view only when density or task complexity justifies it. Prefer
  ordering, grouping and reduced repetition first. Keep simple management
  controls directly reachable; runtime management stays on one page.
- Apply the concrete layout in [Settings and startup](../settings.md).

## Copy and state

- User-facing copy describes only an action, state, result or decision the user
  needs.
- Do not expose implementation instructions, architecture rules, acceptance
  criteria, storage keys or internal enum names as UI copy.
- Prefer concise labels and one short sentence when context is necessary.
- Every applicable surface covers initial, loading, ready, busy, disabled,
  waiting, cancelled, recoverable failure and terminal failure states.
- Use a named current step instead of a blank view, indefinite unlabeled spinner
  or fabricated percentage.
- Error surfaces present a safe summary and next action before technical detail.
  Place diagnostics and copy/retry controls near the failing operation. Failed
  startup and operation logs open automatically. Clear short-lived success
  notices without clearing a later error or active progress.

## Visual system

- Feature code consumes semantic color, typography, spacing, border, elevation
  and motion tokens instead of inventing local constants.
- Express hierarchy primarily through alignment, spacing and typography. Avoid
  nested decorative cards, excessive shadows, gradients, glow and ornamental
  badges.
- The current visual system uses square control/card/surface radius tokens and
  a monochrome palette. New components reuse those tokens and danger/focus
  treatments.
- Selection, focus, warning and failure must remain distinguishable without
  relying on color alone.
- DSH owns `ui-theme.preference`; dsh-work trusted surfaces consume the resolved
  preference and do not persist a competing appearance setting.
- Use one maintained icon family. Do not use emoji or improvised text glyphs as
  production controls.

## Interaction and accessibility

- All controls are keyboard reachable in a logical order with visible focus.
- Controls expose accessible names, roles, states and values.
- Labels remain visible after input; placeholders are not labels.
- Actions must not depend on hover, animation completion or color perception.
- Status, errors and validation have text equivalents and appropriate live
  announcements without continuously announcing raw logs.
- Respect operating-system scaling, theme and reduced-motion preferences.
- Resizing must not hide the active decision, failure action or cancellation
  control.
