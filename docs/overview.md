# Overview

The overview (概览) is one page that shows what agents did and how the
environment under them is doing: conversations, usage, DSH Work, DSH and this
computer. It only presents. It starts no work and has no composer; every
action on it opens an existing flow (a conversation, a Settings section, the
environment dialog).

## Entry

- An item in DSH's left sidebar, first in the panel list.
- The overview button in the title bar, and 视图 › 概览. These work while DSH
  is not running, when the sidebar is gone.

The sidebar item shows the number of conversations waiting for the user, a red
`!` when DSH is not running, and a pulsing dot when conversations are running
and none is waiting. The title-bar button follows the same priority. Choosing
any other sidebar entry or conversation closes the overview.

## Layout

A header (title, date and time, 调整布局, and 今天 / 本周 / 本月), then three
groups, each a heading over a 12-column grid with 116 px rows:

| Group | Tile | Shows | Form | Span |
|---|---|---|---|---|
| 对话 | Conversations | Running, waiting, completed, interrupted and scheduled conversations in the period, with counts | Orbit: DSH in the centre, running on the inner ring, results on the middle ring, scheduled on the dashed outer ring | 7×4 |
| 对话 | 产出 | Completed, agent work time, changed files, lines added/removed; tool calls by purpose and failure rate | Four numbers and a segmented bar | 5×2 |
| 对话 | 最近结果 | The latest three results with a one-line summary; interrupted ones offer 继续 | List | 5×2 |
| 对话 | 时间线 | Conversation events and DSH Work/DSH lifecycle events (start, restart, plugin updates, restores, restore points) | Time axis with a "now" mark | 12×1 |
| 用量 | 花费与余额 | Spending when known, otherwise the account balance; tokens; runway when known | Large number and a usage bar | 3×2 |
| 用量 | 按小时 tokens | Cache hit, cache miss and output per hour | Stacked bars with the peak labelled | 5×2 |
| 用量 | 模型与缓存 | Cache hit rate, change from yesterday, tokens per model | Ring and per-model bars | 4×2 |
| 环境 | DSH Work | Run state and uptime, automatic recoveries, DSH, profile, Node, latest restore point; browser, computer use, pet and notifications | Status, key–value rows, capability cells; 切换环境 and 保存恢复点, or 停用插件并重试 and 恢复环境 after a failed start | 4×3 |
| 环境 | DSH | Plugins (disabled, updatable), skills, scheduled tasks, workspaces with branch and change count | Three numbers and a workspace list; 全部升级 and 管理插件 | 4×3 |
| 环境 | 此电脑 | CPU, memory and disk, the last hour of CPU, processes the agents started with their role | Half-ring gauges with the agent share overlaid, a trend line, a process list | 4×3 |
| 环境 | 健康 | Diagnostics, app updates, storage, browser components, privacy | One row of status chips | 12×1 |

Forms follow what the data is: a single figure is a large number; a share of a
whole is a ring or segmented bar; change over time is bars or a line; items with
a state are a list with a coloured dot; on/off capabilities are cells; events
sit on a time axis; how conversations relate right now is the orbit. Quiet when
normal, amber when something needs attention, red when something failed. Agent
quantities use the accent gradient so they stand apart from the rest of the
machine. Tiles never scroll inside themselves.

Missing data is shown as missing, never as zero. A tile whose source is
partial or unavailable says so in its heading. When DSH stops, its tiles keep
the last collected values with a "最后同步于" notice.

Settings has no overview page. Its former content, the current, configured
and last-working environments with environment switching and version records,
is the environment dialog that the DSH Work tile opens.

## Layout editing

调整布局 shows a drag handle and earlier/later buttons on each tile and
up/down buttons on each group. Tiles move only within their group; groups move
as a whole. 完成 leaves editing and 恢复默认 restores the default order. The
order is stored on this computer under `dsh-work-overview-layout`, keyed by
stable tile and group ids; ids the saved order does not know are appended, so
new tiles appear without a reset. Drag reordering uses SortableJS.

## Data

The overview is drawn by the DSH Work shell, not by DSH, and reads three
sources:

- **DSH Work** (Host services): run state, environment, restore points,
  plugin inventory, automation, settings, updates and diagnostics.
- **This computer** (`internal/overviewpc`, sampled by the daemon every five
  seconds, an hour of history): CPU, memory, the data volume, and the Worker's
  Job Object members. The Worker is the first `node` down the Job tree;
  shells, console hosts, browsers and tool servers below it are named by role.
- **DSH** (`@dsh-work/overview`, see its
  [README](../internal/hostplugins/overview/README.md)): sessions, output,
  usage, workspaces, skills, schedules and privacy plugin state, collected for
  the selected period over the current generation's Worker channel.

The daemon's `OverviewService` keeps a lifecycle and operation journal
(`overview-journal.json`) and the latest DSH payloads (`overview-cache.json`,
at most four period/locale/day entries, rewritten only when a payload
changes) in app data. A snapshot is tied to the Run context it was collected
for; DSH data from another context is dropped, while Host and computer data
still show.

The page collects every 15 seconds while it is on screen and not at all while
it is hidden. The sidebar badge needs no collection: the sidebar client counts
waiting and running sessions from DSH's own session status store and reports
them to the shell, which adds the offline state from the Host lifecycle.
Plugin inventory is read at most once a minute and only for a Ready current
Run context, because listing plugins takes the manager's exclusive operation
gate.

## Decision: a Host surface entered from DSH's sidebar

The overview is a DSH Work surface laid over the DSH content area, entered
from a DSH sidebar item. The sidebar plugin registers only the
`sidebar.panellist` icon and intercepts that one button before DSH selects a
main panel.

Why: the overview must stay usable when DSH has not started or has failed, so
it can offer recovery and still show the environment and this computer. A
Host surface also reads Host-only data (Job Object accounting, restore points,
plugin inventory) without exposing it to the DSH page.

Rejected:
- A DSH plugin page in the `main` slot, as
  [dsh-homepage](https://github.com/AngelosZou/dsh-homepage) does. It is the
  simplest to integrate, but it disappears together with DSH.
- An equal-weight card wall with free placement. Users reorder tiles within
  fixed groups instead, so the page keeps one reading order.
- A greeting, composer and daily brief on the overview. Those belong to DSH's
  new-conversation page and to a future personal-agent surface.
- A model-callable overview tool. If agents need these readings, the data
  modules expose them, not the page.

Revisit if DSH adds a public sidebar launcher for custom actions (the capture
on its button would go), or if users need to hide or resize tiles.
