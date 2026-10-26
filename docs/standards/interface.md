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
- The Host has one locale (`en`, `zh-CN`, `ja-JP`). Trusted WebViews and
  native menu and tray chrome use the same typed vocabulary; DSH content is
  not translated or modified.
- Every applicable surface covers initial, loading, ready, busy, disabled,
  waiting, cancelled, recoverable failure and terminal failure states.
- Use a named current step instead of a blank view, indefinite unlabeled spinner
  or fabricated percentage.
- Error surfaces present a safe summary and next action before technical detail.
  Place diagnostics and copy/retry controls near the failing operation. Failed
  startup and operation logs open automatically. Clear short-lived success
  notices without clearing a later error or active progress.

## Visual system

- Startup and Settings share one stylesheet stack under `frontend/src/ui/`:
  tokens, base, components, layout, then page files. Layout owns structure,
  order, slots, spacing rhythm and breakpoints; a theme supplies token values
  and optional ornaments and never changes structure. The startup window
  reserves a backdrop layer for a theme's ambient effect.
- Feature code consumes semantic color, typography, spacing, border, elevation
  and motion tokens instead of inventing local constants.
- Express hierarchy primarily through alignment, spacing and typography. Layout
  and components avoid nested decorative cards, excessive shadows, gradients,
  glow and ornamental badges; the default DSH theme follows DSH's native
  surfaces, controls and neutral palette, with blue reserved for its semantic
  accents.
- A theme may restyle the content area boldly (window title bars, frosted
  panels, cut corners, patterned backgrounds, glow), provided the decoration
  carries no information, page structure and copy stay unchanged, and body
  text sits on a surface opaque enough to keep normal contrast. Each theme
  declares the light/dark modes it supports.
- New components read the semantic tokens (accent, outline, failed, navigation,
  selection, group and row) and reuse the danger/focus treatments, so every
  theme styles them without component-specific rules.
- A theme changes components through tokens in `frontend/src/ui/tokens.css`
  (navigation, switch size, track border and checked thumb, title and row
  weights, page-title divider, button and select shapes, badges), not by
  overriding component rules. When a theme needs a look no token covers, add a
  token whose default keeps the current appearance and move every theme to it.
  Theme files keep only decoration that tokens cannot express (inset row
  dividers, raised panels, bevels, glow). Per-theme overrides drift per
  surface and make each new theme repeat geometry fixes; the cost of tokens is
  a larger set in `tokens.css`.
- Within one theme a component looks the same wherever it appears; it does not
  change with the page or container it sits in.
- Product-style themes (ChatGPT, Claude, GitHub, LobeHub) take their values
  from the product itself: its published design tokens, or values measured
  from its current settings page. Where a value leaves text below 4.5:1
  against its actual background, the theme darkens or lightens that one value;
  control borders and switch tracks follow the product even below 3:1, because
  the label inside or beside the control identifies it. An impression of the
  brand drifts toward generic habits the products do not use. Revisit a
  product-style theme when that product's settings page changes visibly.
- Do not mark the current or selected item with a thick accent bar on one side
  unless the style being reproduced has one (GitHub's sidebar does). Themes
  show current and selected state through background, weight or their own
  idiom; `--nav-active-bar` and `--selection-bar` stay 0.
- To add a theme: list its id and modes in `internal/settings/appearance.go`
  and `frontend/src/themes.ts` in the same order (a frontend test keeps them
  equal), add its names to every locale, and add
  `frontend/src/ui/themes/<id>.css`, imported in the same order in
  `frontend/src/ui/app.css`. Check startup, every Settings page and the
  dialogs in each supported mode, then check every component in every theme
  and mode: each button, field, select, switch, tag, step marker, progress
  bar, row state, notice, log and dialog, with hover and keyboard focus,
  measuring text contrast on the composited background, equal control heights,
  centred icons and switch thumbs, and visible focus rings.
- Native controls follow the theme: option lists use the opaque page colours
  and the search field's clear button uses the muted text colour.
- Selection, focus, warning and failure must remain distinguishable without
  relying on color alone.
- dsh-work owns the workbench appearance (`settings.Appearance`: theme id and
  system/light/dark mode). SettingsService is the only writer and publishes
  each saved change as the `appearance` event. In the DSH theme, DSH receives
  only the resolved light/dark mode with empty tokens, so its surfaces,
  controls, conversation and stored `ui-theme.preference` remain DSH-owned;
  dsh-work's title bar, Settings, startup and dialogs adapt to DSH's native
  values. Other themes map their colours, radii, shadows, interface font and
  controls into DSH through `@dsh-work/shell` (`frontend/src/dsh-theme.ts` and
  `frontend/src/dsh-controls.ts`). The plugin clears its decoration stylesheet
  and control markers when switching to empty tokens. Theme checks include
  workbench surfaces, text, focus and selected states in light and dark. The
  native window frame takes the mode when the window is created; Wails 3 beta
  has no runtime frame-theme switch.
- Use one maintained icon family, `lucide` (ISC, tree-shaken ES modules,
  stroke icons whose weight and caps follow tokens); only imported icons ship.
  Do not use emoji, improvised text glyphs or a hand-drawn set as production
  controls.

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
