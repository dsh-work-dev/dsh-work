# ADR-0005: Keep Work localization small, typed and shared by native chrome

Status: Accepted

## Context

Work has two trusted WebView surfaces plus a Wails application menu and system
tray. The language switch must update all of them while leaving the external DSH
workspace untouched. The first release has a fixed set of short labels,
statuses and feedback messages; it does not need plural rules, date/number
formatting or runtime-loaded translation packages.

## Decision

Persist one `locale` field in the versioned Work settings document. Supported
values are `en`, `zh-CN` and `ja-JP`; missing or invalid values use Simplified
Chinese. Settings is the only write boundary. After a successful write, the
composition root updates Wails menu/tray labels and emits a typed `locale`
event. Each trusted WebView applies the same locale event and re-renders its
dynamic state.

The frontend uses a small typed message dictionary with interpolation for the
fixed product copy. The native composition edge has a deliberately small
projection for menu, tray and dialog labels. A general-purpose frontend i18n
library was considered, but it cannot update Wails native controls and would
create a second authority for this small fixed vocabulary; adding it would not
provide a capability required by the contract. The local dictionary is
therefore the narrower dependency boundary, not a general translation
infrastructure claim.

## Consequences

- Language switching is immediate in Settings and the startup surface when
  both windows are open.
- DSH theme ownership remains unchanged; Work does not add a language or theme
  setting to the external DSH page.
- New user-facing copy must be added to all three dictionaries and reviewed for
  concise labels before it is introduced.
