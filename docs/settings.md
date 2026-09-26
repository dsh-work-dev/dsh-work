# Settings and startup

## Window lifecycle and preferences

Closing a desktop window hides it and keeps its WebView in the UI client.
Reopening restores the same page state and connects to the same daemon. The
daemon, Worker tasks, tray, Pet and notifications continue while all UI windows
are hidden. Use “停止后台并退出” from the tray or application menu for
Worker/child cleanup and complete background shutdown.

There is no close-behavior setting. Legacy persisted `closeToTray` values are
ignored and are omitted when preferences are saved. Locale, automatic recovery,
Pet and notification preferences remain persisted.

The workbench title is `dsh-work` and its native menu contains 操作/设置/帮助
in Chinese. Settings uses the localized Settings title. Both window types
restore their saved normal size and maximised state independently.

## Information hierarchy

Keep common actions directly available. Use spacing, grouping, ordering and
concise labels to reduce complexity before introducing another view. A growing
history or dense record detail can justify a management dialog; a simple set of
controls does not require a summary screen merely for consistency.

The current square, monochrome token system applies to startup, Settings and
their dialogs. Technical details belong beside the relevant operation or inside
selected details, with the primary action and status visible first.

## Navigation and pages

Overview stands alone. The sidebar groups Application (General, Notifications,
Pets), DSH environment (Runtimes, Profiles, Plugins), and Maintenance (Storage,
About). The narrow-window selector uses the same groups. Switching sections
preserves their selection and remembers content scroll and return focus within
the current Settings instance.

| Page | Current layout |
|---|---|
| Overview | Current/configured/recovery context, switch and safe-mode actions; compact version-record summary and save/history controls. |
| Runtimes | DSH and Node management directly on the same page, separated by headings. Installed versions, acquisition controls, progress and errors remain in their respective section. Removing a runtime shows a short confirmation below the runtime list. |
| Profiles | Directory and selected-profile editor; backup is a primary action, with clone/export/delete grouped as secondary actions. Backup count/latest time leads to a history dialog. |
| Plugins | Explicit profile scope and current-profile mutation rules. Each third-party plugin row offers Remove (and Upgrade when available). While the profile is running, the row also has an enable switch at its right edge, and a disabled plugin is marked in its details. Below the list, a collapsed Official loader entries section shows a searchable tree of official layer → loader entry. Each entry DSH allows to be toggled gets the same switch. Operation feedback sits near its controls. |
| Pets | Compact name/thumbnail/source/current-state list and preview. Use, topmost and size controls precede the full description. |
| General and Notifications | Visible labels, aligned controls and short supporting text. |
| Storage and About | Direct maintenance controls and contextual feedback. |

Version history uses a list, selected details and restore confirmation in the
same dialog rather than expanding every record inline. Backup history has its
own management dialog. Closing dialogs restores the originating focus.
Version records and profile backups are separate operations: a version record
reconstructs dependency versions; it does not archive conversation data.

## Startup and operation feedback

Startup shows Node, DSH, profile/plugins and service-start steps. When a step
fails, its summary, available recovery actions and bounded diagnostic output
appear together. Copy is available beside the diagnostics. A failure with no
known step is presented as a general failure rather than assigned to Node.
When the failure output names installed third-party plugins, a panel lists up
to three of them with Disable and Remove. Each action asks for confirmation and
then starts again. Disable keeps the plugin installed; turn it back on in
Plugins once DSH is running.

Normal startup exposes optional logs and cancellation. Failed startup exposes
logs automatically and initially positions them at the error. Manual log scroll
is retained. Version recovery opens a focused selector; confirmation replaces
the selection view and cancellation returns to the record.

Settings errors follow the triggering operation, and failed operation logs
expand automatically. Selected transient success notices clear after five
seconds without clearing a later different message. Active progress and
actionable persistent results remain available.
