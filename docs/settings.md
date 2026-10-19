# Settings and startup

## Window lifecycle and preferences

Closing a desktop window hides it and keeps its WebView in the UI client.
Reopening restores the same page state and connects to the same daemon. The
daemon, Worker tasks, tray, Pet and notifications continue while all UI windows
are hidden. Use 文件 → 退出 in the workbench menu, or “停止后台并退出” from the tray, for
Worker/child cleanup and complete background shutdown.

There is no close-behavior setting. Legacy persisted `closeToTray` values are
ignored and are omitted when preferences are saved. Locale, appearance,
automatic recovery, Pet and notification preferences remain persisted.
Settings saved before dsh-work owned its appearance adopt DSH's light/dark
preference once.

The workbench has no system title bar. Its top bar shows the app icon, the menus
文件/视图/设置/帮助 in Chinese, and the window buttons. It shares the DSH sidebar's
colour and uses the theme's navigation and menu styles. While
DSH shows a dialog, the top bar takes the same dimmed mask. Settings opens as a
panel inside the workbench, below the top bar and over DSH, which stays loaded.
The tray, the CLI and the 设置 menu open the same panel at a section. 返回工作台 at
the top of the Settings navigation, or Esc, closes it; opening the workbench without a section or running a
DSH command from the menu also closes it. The workbench restores its saved
normal size and maximised state. Menu copy names the action only: no ellipsis
after an item that opens another view, and 退出 rather than a description of
what quitting stops.

## Information hierarchy

Keep common actions directly available. Use spacing, grouping, ordering and
concise labels to reduce complexity before introducing another view. A growing
history or dense record detail can justify a management dialog; a simple set of
controls does not require a summary screen merely for consistency.

Startup, Settings and their dialogs share one theme-neutral layout (see
[Interface standards](standards/interface.md#visual-system)). The default theme is square and monochrome with system sans-serif
type; monospace is limited to versions, paths and logs. Other themes may
restyle the content area boldly but never change page structure, order or
copy. General lists Theme and
Appearance (match system, light, dark); modes a theme does not support are
disabled. DSH follows the theme's colours, mode, corner shapes, shadows and
interface font without a restart; its conversation text keeps DSH's font, and
its own Appearance setting is hidden. The Theme list starts with Monochrome, then the product styles
(ChatGPT, Claude, GitHub, LobeHub, each named after the product alone), then
everyday themes (Soft, Swiss, Paper, Aurora glass), then retro and expressive
ones (Ink, Classic OS, Terminal, Neon, Neo-brutalism, Bauhaus, Art deco). Technical details belong beside the relevant operation or inside
selected details, with the primary action and status visible first.

Every Settings page follows one grammar so pages read as one application:

- A page is its title followed by sections. A section is a heading and one
  group of rows. A page with a single group omits the section heading.
- Section-wide actions sit at the right of the section heading; item actions
  sit at the right of their row; a group's footer row holds actions for the
  whole list (download, import, clone/export/rename/delete).
- Every control and button lives in a group row. Only operation results,
  errors and status text appear below a group.
- Rows put title and supporting text on the left and controls on the right.
  When a field shows its label above the control, row actions align with the
  control, not the label. A chromeless button that starts a row keeps its text
  on the row's content edge.
- List and detail pages put the list in one group and the selected item in
  another group whose first row names it, so both groups start at the same
  height.

## Navigation and pages

Overview stands alone. The sidebar groups Application (General, Notifications,
Pets), DSH environment (Runtimes, Profiles, Plugins), and Maintenance (Storage,
Diagnostics, About). The narrow-window selector uses the same groups. Switching sections
preserves their selection and remembers content scroll and return focus within
the current Settings instance.

| Page | Current layout |
|---|---|
| Overview | Current environment facts (state, DSH, profile, Node) with Switch environment on the section heading; configured and last-working context when they differ; version-record summary row with history/save. |
| Runtimes | DSH and Node management directly on the same page, separated by headings. Installed versions, acquisition controls, progress and errors remain in their respective section. Removing a runtime shows a short confirmation below the runtime list. |
| Profiles | One column, list then detail. All profiles is a group of rows (name, current tag, kind and plugin count) with Import on its heading; choosing a row opens that profile's detail, and All profiles returns to the list, as does choosing Profiles in the sidebar. The detail has the name with Switch on its heading, a group with backups (manage history, back up now) and plugins, and a Profile actions group with one row each for clone, export, rename and delete. |
| Plugins | Explicit profile scope and current-profile mutation rules. Each third-party plugin row offers Remove (and Upgrade when available). When two or more plugins have updates, Upgrade all on the Installed heading upgrades them in one change: the Worker stops once, every package updates in order, then one health check decides. A failure is handled as one plugin change, so automatic rollback returns the profile to its state before the first upgrade. While the profile is running, the row also has an enable switch at its right edge, and a disabled plugin is marked in its details. Below the list, a collapsed Official loader entries section shows a searchable tree of official layer → loader entry. Each entry DSH allows to be toggled gets the same switch. Operation feedback sits near its controls. A 内置组件 section lists dsh-work's own DSH plugins (shell, account and desktop pet integration) with their load state and no controls. Check for updates re-runs the registry check. A plugin DSH disables because it does not declare support for the running DSH version is marked with that version and offers Enable anyway, which grants DSH's exemption for that exact plugin and DSH version after confirmation; an install or upgrade DSH refuses for the same reason asks before retrying with that exemption. |
| Pets | Display section (show, always on top, size), then the pet list. The pet being viewed opens its preview in place under its row (large preview, description, Clear/Retry/Use), one pet at a time; the page opens on the pet in use. Arrow keys move between pets. |
| General and Notifications | General has Startup (failure policy) and Interface (language, theme, appearance) sections; Notifications is one group with the desktop switch and its nested event switches. |
| Storage and About | Storage has one section per location, with migration save/cancel in the user-data footer. About has a product section (version, description, help, copy diagnostics) and an application update section. |
| Diagnostics | Recent problems from the last 7 days, one row each with a plain title, time and repeat count, or a short empty state. Expanding a row shows the code and message, the actions that fit its code, Copy report and Report on GitHub. Below, a Logs group has the Detailed logging switch, Open log folder and Export diagnostics bundle; an exported bundle shows its saved path and any files left out. |

Problem actions map diagnostic codes to existing places: runtime and version
problems open Runtimes; DSH start failures offer Restart and Plugins; plugin
problems open Plugins; profile and Workspace problems open Profiles; saved-data
and settings problems open Storage; update failures check for updates. Other
codes offer only the report actions. Report on GitHub copies the report, then
opens the dsh-work issue form with the code and message as the title; the
report itself never goes into the URL. Help › Logs opens this page, and Help ›
Copy diagnostics, About and the failed-startup Report problem use the same
report for the newest problem, or an environment-only report when there is
none.

Version history uses a list, selected details and restore confirmation in the
same dialog rather than expanding every record inline. Backup history has its
own management dialog. Closing dialogs restores the originating focus.
Version records and profile backups are separate operations: a version record
reconstructs dependency versions; it does not archive conversation data.

## Startup and operation feedback

Startup shows Node, DSH, profile/plugins and service-start steps. When a step
fails, its summary, available recovery actions and bounded diagnostic output
appear together. Copy and Report problem are available beside the diagnostics. A failure with no
known step is presented as a general failure rather than assigned to Node.
When the Worker fails to start or stops unexpectedly, a panel lists that
profile's third-party plugins with Disable and Remove. Plugins the failure
output names (up to three) come first and are marked; already disabled plugins
show their state and offer only Remove. With two or more enabled plugins,
Disable all and start turns them all off in one step. Each action asks for
confirmation and then starts again. Disable keeps the plugin installed; turn it
back on in Plugins once DSH is running, one at a time to find the one that
fails.

Normal startup exposes optional logs and cancellation. Failed startup exposes
logs automatically and initially positions them at the error. Manual log scroll
is retained. Version recovery opens a focused selector; confirmation replaces
the selection view and cancellation returns to the record.

Settings errors follow the triggering operation, and failed operation logs
expand automatically. Selected transient success notices clear after five
seconds without clearing a later different message. Active progress and
actionable persistent results remain available.
