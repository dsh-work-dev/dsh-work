# PC UI design guidelines

Status: normative UI guidance for the first PC release. This document defines the global visual language and interface construction rules; product behaviour remains defined by the [requirements](../../01-product/pc/requirements.md) and [interaction specification](interaction-spec.md).

## Design direction

Work is a **restrained, desktop-native and information-clear AI workspace**.

- **Restrained:** the shell supports the work instead of competing with it. Colour, elevation and motion communicate meaning rather than decoration.
- **Desktop-native:** the application respects operating-system window, menu, focus, shortcut and notification conventions.
- **Information-clear:** current state, required action and safe next step are visually obvious without exposing unnecessary technical detail.
- **AI workspace:** streaming activity, Tool calls, approvals, browser attachment and task progress are first-class interface patterns rather than generic chat decoration.

The DSH workspace is the visual subject. Work-owned chrome is quiet, stable and recognisable. The product must not look like a traditional IDE, a web administration dashboard or a game launcher.

## Scope

This guide governs:

- visual hierarchy, layout and density;
- colour, type, spacing, shape and elevation semantics;
- shared controls and AI-specific patterns;
- loading, empty, error and recovery presentation;
- icons, motion, platform adaptation and accessibility;
- design-token ownership and visual acceptance.

It does not decide product scope, permission classification, process behaviour or browser policy. Those decisions are rendered according to this guide after they are defined elsewhere.

## Interface priorities

When visual goals conflict, use this order:

1. current state and required action are understandable;
2. the primary task remains usable with keyboard and assistive technology;
3. trusted Work chrome is distinguishable from embedded or untrusted content;
4. content remains legible at supported scaling and window sizes;
5. platform behaviour feels native;
6. visual consistency is preserved;
7. density and polish are optimised.

## Shell hierarchy

The ready-state window has three visual layers:

1. **Work chrome:** title or command area, application status and entry points for Activity, Diagnostics and Settings.
2. **DSH workspace:** the dominant canvas and default focus destination.
3. **Transient layer:** drawers, menus, tooltips and trusted dialogs used only while needed.

Rules:

- Work chrome MUST remain available when Worker content is loading or failed.
- The workspace receives the majority of the window area and MUST NOT be boxed inside decorative cards.
- Activity SHOULD use a side drawer or adjacent panel so it does not replace task context.
- Settings and Diagnostics are distinct Host-owned routes, not overlays on untrusted content.
- Startup and recovery replace unavailable Worker content with a trusted Work surface.
- A persistent border, surface change or comparable cue MUST make the Host／Worker ownership transition perceptible without dominating the canvas.
- The system tray is a compact companion surface, not an alternative full application UI.

The screen map is maintained in [information architecture](information-architecture.md).

## Layout and density

- Use a consistent base spacing scale expressed through semantic tokens.
- Align related labels, controls and status indicators to shared edges.
- Prefer spacing and headings over nested containers to express grouping.
- Use compact density for logs, trees, task steps and metadata; use comfortable density for forms, approvals and recovery actions.
- A density change MUST be intentional at the pattern level, not applied per individual control.
- Primary actions stay close to the content they affect. Global actions remain in stable shell locations.
- Long paths, origins and identifiers truncate in the middle or end according to what users must distinguish; the complete value is available accessibly on demand.
- Narrow windows collapse secondary panels before compressing primary content below usable width.
- Resizing MUST NOT hide the active approval, error action or task cancellation control.

Exact measurements belong to theme tokens and validated screen specifications. Feature code MUST NOT invent local spacing values.

## Colour system

Colour is semantic and theme-driven. At minimum, themes define these roles:

| Role | Purpose |
|---|---|
| `canvas` | primary workspace background |
| `surface` | navigation, panels and grouped controls |
| `surface-raised` | transient menus, dialogs and elevated content |
| `border` | structural separation |
| `text-primary` | main content and labels |
| `text-secondary` | supporting information |
| `text-disabled` | unavailable content that remains legible |
| `accent` | selected state and primary action |
| `info` | neutral operational information |
| `success` | verified completion or healthy state |
| `warning` | attention needed without immediate failure |
| `danger` | failure, rejection or destructive consequence |
| `focus` | keyboard focus indication |

Rules:

- Semantic roles MUST be used instead of raw colour names in feature code.
- Status MUST NOT rely on colour alone; pair it with text, icon shape or structure.
- Accent colour is reserved for selection, focus and the primary action. It is not general decoration.
- Danger colour MUST NOT be used for ordinary cancellation or neutral close actions unless data or external state is at risk.
- Light and dark themes preserve hierarchy and contrast rather than mechanically invert values.
- Embedded DSH content may have its own theme, but Work chrome and ownership cues must remain recognisable in both themes.

Specific palette values are selected and locked only with a reviewed visual reference. Until then, the semantic roles are the contract.

## Typography

- Use the platform UI font stack for Work chrome and ordinary interface text.
- Use a legible cross-platform monospace stack only for code, commands, paths, identifiers and diagnostic output.
- Define semantic text roles for window title, section heading, body, control label, metadata, caption and monospace output.
- Heading size and weight MUST reflect information hierarchy, not visual decoration.
- Body text must remain readable at operating-system scaling without fixed-height clipping.
- Interface labels use sentence case unless a platform convention or proper name requires otherwise.
- Avoid all-caps labels and excessive weight changes.
- Streaming output and logs use stable line metrics so incoming content does not cause avoidable layout movement.

Font families, sizes, weights and line heights are theme tokens. Individual screens consume semantic text roles.

## Surfaces, borders and elevation

- Prefer flat, aligned surfaces with subtle structural separation.
- Use borders for persistent relationships and elevation for transient overlap.
- A raised surface MUST correspond to a real interaction layer such as a menu, popover or dialog.
- Avoid cards nested inside cards and shadows on every container.
- Corner radius follows one small semantic scale for controls, containers and overlays.
- The same kind of surface uses the same border and elevation treatment across the application.
- Focus indication is independent of border styling and remains visible against every supported surface.

## Shared controls

Every shared control defines default, hover, pressed, focused, selected, disabled, loading and error states where applicable.

### Buttons

- One visual primary action per local decision area.
- Secondary and quiet buttons preserve hierarchy without becoming text that looks non-interactive.
- Destructive buttons state the effect with a verb; colour alone does not communicate consequence.
- Loading buttons retain their label context and prevent duplicate submission.

### Inputs and selection

- Labels remain visible after entry; placeholder text is never the only label.
- Validation appears near the field and is announced accessibly.
- Sensitive values are masked by default and never echoed into activity history.
- Selection controls expose their current value, available choices and keyboard behaviour.

### Lists, trees and tables

- Selection and keyboard focus are visually distinct.
- Rows maintain stable alignment when status or actions appear.
- Row actions remain discoverable by keyboard and do not depend only on hover.
- Tables are used for genuine column comparison; simple metadata uses a list or description layout.

### Menus, popovers and dialogs

- Menus contain commands, not arbitrary multi-step forms.
- Popovers provide short contextual interaction and close without losing work.
- Dialogs are reserved for blocking decisions or focused tasks that cannot safely remain inline.
- Focus enters a trusted dialog and returns to its invoker when the dialog closes.

## AI and operational patterns

### Streaming activity

- Distinguish user content, agent output, Tool activity and system status by structure and labels, not colour alone.
- Streaming text should remain readable without forcing the viewport away from content the user is inspecting.
- Background activity is summarised; raw event noise belongs in Diagnostics.

### Tool calls and approval

- Work MUST NOT draw a duplicate approval dialog for a DSH Tool call.
- Work-owned `Waiting for approval` indicators focus the exact trusted DSH call card.
- The call presentation makes action, target, scope, consequence and masked sensitive data scannable.
- Approval and rejection controls remain visually balanced enough to support a deliberate choice; the risky action is not visually coerced.

### Browser connection

- Connection state shows browser, profile label, assigned tab and whether a task is active.
- `Disconnect` remains visible and does not look destructive to browser data.
- Browser-owned permission or debugging indicators are never hidden or imitated.
- Profile-wide technical exposure is explained in plain language through progressive disclosure.

### Task progress

- Show the current action and target before elapsed time or technical metadata.
- Completed steps use concise summaries; pending steps do not imply certainty about future execution.
- `Cancel task` remains reachable while an operation can still be cancelled.
- Indeterminate progress never displays a fabricated percentage or completion time.

## States and feedback

All screens and reusable patterns account for:

- initial and empty;
- loading or starting;
- ready;
- active or busy;
- waiting for user action;
- success;
- partial completion;
- disconnected;
- cancelled;
- recoverable failure;
- terminal failure;
- disabled or unavailable.

Rules:

- Never use a blank WebView as loading feedback.
- Prefer a named current step over a generic spinner.
- State changes use concise text and an appropriate live region; streaming logs are not announced continuously.
- Success feedback is proportional: persistent when the result affects future state, transient when the effect is already visible.
- Error surfaces present summary, stable code, recommended safe action, alternatives and disclosed technical detail in that order.
- Retry is disabled while cleanup is incomplete, and the UI explains why.
- Empty states explain what belongs in the area and provide an action only when the user can meaningfully take one.

Detailed behaviour is defined in [states and errors](states-and-errors.md).

## Icons and visual assets

- Use one maintained icon family with consistent stroke, optical size and alignment.
- Icons that trigger actions have accessible names; decorative icons are hidden from assistive technology.
- Familiar platform symbols may follow native convention where that improves recognition.
- Do not use emoji, text glyph approximations or improvised drawings as production icons.
- Status icons must remain distinguishable without colour.
- Product illustrations and branded assets require an approved visual source and must fit their measured UI slot.
- Screenshots containing user data are not used as generic documentation or placeholder artwork.

## Motion

- Motion explains origin, destination, continuity or status change; it is not ambient decoration.
- Use short transitions for local control feedback and slightly longer transitions only for major panel or route changes.
- Streaming indicators and progress motion MUST NOT distract from reading.
- Repeated or infinite animation is limited to genuinely active states and stops when the state ends.
- Respect the operating-system reduced-motion preference by removing non-essential movement and preserving state feedback.
- Functional correctness and focus order must not depend on animation completion.

Motion timing and easing are theme tokens selected with the first reviewed visual system.

## Platform adaptation

Windows, macOS and Linux share information architecture, semantic tokens, component roles and state language. They do not need pixel-identical window chrome.

- Use native window controls and expected placement on each platform.
- Follow platform conventions for application menus, tray or menu-bar behaviour, notifications, file selection and keyboard shortcuts.
- Platform font rendering and scaling differences are accepted; clipping and hierarchy differences are not.
- Shortcut labels render with platform-appropriate modifier names and symbols.
- System theme and reduced-motion changes are reflected without requiring restart where the runtime supports it.
- A platform exception belongs in the affected pattern specification, not as an unexplained one-off style override.

## Accessibility

- All controls are keyboard reachable in a logical order.
- Focus is always visible and never indicated only by colour.
- Every control has an accessible name, role, state and value as applicable.
- Text and essential graphics meet the project's adopted WCAG contrast target in every theme and state.
- Status, error and validation messages have text equivalents and appropriate announcements.
- Text scaling and display scaling do not hide primary actions or require two-dimensional scrolling for ordinary forms.
- Hit targets remain usable at supported scale factors.
- Tooltips do not contain information unavailable by keyboard or assistive technology.
- Screen-reader and keyboard smoke tests are release requirements on every supported platform.

## Design tokens and implementation

The UI Implementation consumes semantic tokens rather than feature-local constants. Token groups include:

```text
color.*       semantic colour roles and interaction states
text.*        font family, size, weight and line height roles
space.*       layout and control spacing scale
size.*        control, icon and target dimensions
radius.*      control, container and overlay shape roles
border.*      structural and focus treatments
elevation.*   transient-layer treatments
motion.*      duration and easing roles
z.*           named layer ordering
```

- Token names describe purpose, not their current visual value.
- Theme files own concrete values for light, dark and platform adaptations.
- Shared controls own their state mapping; feature screens MUST NOT restyle internal states ad hoc.
- A new token requires a repeated semantic need. A one-screen exception is resolved through composition before extending the global system.
- Changes to global tokens require visual regression review across shell, startup, Activity, approval focus, Settings and Diagnostics.

## Visual acceptance

Each release candidate is reviewed at minimum across:

- every supported operating system;
- light and dark themes;
- minimum and common window sizes;
- supported display scale factors;
- keyboard-only navigation;
- reduced motion;
- long translated-style strings, long paths and long origins;
- startup, ready, busy, approval, disconnected, failure and safe-mode states.

Review checks:

- [ ] Work chrome remains quiet and visually distinct from embedded content.
- [ ] The current state and primary action are identifiable at a glance.
- [ ] Alignment, spacing, typography, borders and radius are consistent.
- [ ] No content is clipped, obscured or dependent on hover.
- [ ] Selection, focus, warning and failure remain distinguishable without colour.
- [ ] Loading, empty, failure and cancellation states are complete.
- [ ] Platform-native window and keyboard conventions are respected.
- [ ] Sensitive content is masked and absent from incidental UI history.
- [ ] Accessibility checks pass in the rendered application, not only in source markup.

## Prohibited UI shortcuts

- blank loading views or indefinite unlabeled spinners;
- decorative card nesting, excessive shadows or accent colour everywhere;
- a second Work approval dialog for a DSH approval;
- hidden browser connection or automation state;
- status communicated only with colour, animation or an icon;
- placeholders used as labels;
- hover-only actions or invisible keyboard focus;
- emoji and improvised glyphs used as functional icons;
- arbitrary per-screen colours, spacing, typography or z-index values;
- pixel-identical platform chrome that conflicts with native conventions;
- technical logs or stack traces in primary user-facing error content.

