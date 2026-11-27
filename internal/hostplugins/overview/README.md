# Overview data

`@dsh-work/overview` is a read-only Worker plugin. The trusted Host requests
`GET /__dshwork/overview?period=today&locale=zh-CN&timezoneOffsetSeconds=28800`
over the current generation's authenticated Worker channel. DSH's
`connection.requestRejection` gates the endpoint. Only `today`, `week`, and
`month` are accepted; offsets are signed seconds east of UTC. The result is:

```js
{schemaVersion: 1, collectedAt: "2026-11-08T12:00:00.000Z",
 availability: {sessions: {state: "ready"}}, data: {/* DashboardData fields */}}
```

## Data ownership and completeness

- `sessionController.list` supplies live running state and projection titles.
  `sessionQuery.listSessions` reconciles the complete cold/live corpus, including
  cold records without a working directory that the presentation controller
  omits. `sessionQuery.observeSession(..., {projectionMode: 'none'})` reads the
  lossless durable events without activating an Agent. Every observation is
  disposed; earlier runtimes can use `sessionController.inspect` instead.
- Aggregation consumes all **owned** log events, after `inheritedEventCount`.
  A fork's inherited history is not charged twice. Ordinary forks remain visible
  conversations; `origin: subagent` / positive delegation depth distinguish
  delegated Sessions. Their owned usage, tools and work time still contribute.
- Calendar periods use the requesting machine's current UTC offset. Weeks start
  Monday, months on day 1. Historical daylight-saving transitions are not an
  IANA-zone reconstruction. Date boundaries are inclusive at start and end.
- Conversation nodes are top-level Sessions active/waiting now or carrying work
  in the selected period. Blank Sessions with no work are omitted. Completion
  counts Sessions whose latest state is completed and whose last turn ended in
  the period, rather than counting every completed turn as another conversation.
  Recent summaries are bounded plain text from the latest non-interrupted
  assistant message, not a generated or invented summarization.
- Agent work time is the **sum of each Session's turn intervals**, clipped to
  the period, including currently running turns. Concurrent Agents therefore
  contribute concurrent seconds. It is not elapsed wall time for the whole app.
- Tool-call counts use the tool's declared name and call-start time. Results
  pair by the source call event, or by turn, step and `callId` in logs
  without a source link. Failure rate is failed results / settled calls in
  that call-start cohort; unfinished calls are excluded.
- Usage reads provider counters from `assistant/message` or the last embedded
  `usage` chunk in an `assistant/attempt`. Replacement settlements for one
  turn/step replace the same sample; `llm/retry-started` closes that slot, so a
  subsequent retry contributes another attempt. Reasoning is already included
  in output and is never added again. Cache-read, uncached input, cache-write,
  and output counters are disjoint. The displayed cache-miss bucket combines
  uncached input and cache writes. Model identity includes provider + model.
  Hour buckets use settlement time and only contain observed usage; missing
  hours are not fabricated observations. Today's cache-rate change compares
  the previous calendar day when both days have measured input tokens and
  neither has missing usage.
- `workspaceChanges.summary(sessionId, seq)` provides real per-turn totals.
  Latest summary wins for a turn. Added/deleted lines sum those turn summaries;
  changed files deduplicate known paths across turns after resolving them
  against each Session's working directory. This is turn activity, not the
  current Git diff. Summaries are retained only while the recording
  Worker owns the live Session: a durable `workspace/changes` event does **not**
  contain the old summary. Missing/truncated summaries produce `partial`, not
  invented historical zeros. Older persisted change totals cannot be rebuilt
  after Worker loss through this service.
- Workspace identities/paths come only from `workspaceRegistry.list()`. Git
  inspection uses the fixed argument vector `git --no-optional-locks -c
  core.fsmonitor=false -C <path> status --porcelain=v1 --branch --untracked-files=all -z`, so a
  repository's own fsmonitor command never runs, with a 3-second deadline, a
  1 MiB output bound and no shell. The result is the branch and a changed-file
  count; each untracked file is counted separately, and renames/copies count
  once. Non-repositories, missing Git and unavailable directories leave that
  Workspace's Git fields absent.
- `schedule.catalog()` supplies persisted active reminders without resuming
  Sessions. The orbit and timeline include reminders due in the selected
  calendar period; the DSH runtime tile shows the overall count and next
  reminder. `skills.list({signal})` supplies the global/root skill catalog,
  excluding project-scoped skills of a particular Agent.
- `accountController.getBalance({version, locale, timezoneOffsetSeconds})`
  queries DSH's display-safe account service. No credential files or tokens are
  read by this plugin. Recharge and bonus wallets are summed **within one
  currency**; when both currencies exist CNY is displayed and availability is
  partial. Signed-out and failed queries stay unavailable, never zero.
  The token contract contains no monetary price or authoritative invoice, so
  `spent`, per-model cost, consumed fraction, and runway remain absent;
  `availability.billing` is always explicitly unavailable. The installed rc.5
  account controller exposes safe wallet balances and a browser usage-page link,
  not a transaction ledger. Model catalog fields contain model/capacity metadata,
  not currency rates; request-image "pricing" APIs count tokens, not money.
- `pluginManager.listPlugins()` supplies effective live `enabled` and
  `fiberPhase` metadata for the five reporting modules named by the existing
  `dshadapter.ensureAppProfilePatch` defaults: desktop telemetry, product
  analytics, session telemetry, session-log contribution and plugin inventory.
  Privacy is OK only when all five are present, effectively disabled, and have
  no live root fiber. Any enabled duplicate is an error; remaining fibers give
  a warning, and absent inventory/entries stay unknown. The label states
  "currently disabled", not "locked": profile initialization pins these rows
  off, but existing live PluginManager controls can enable them. Neither a
  default patch nor an unrelated read-only management reason proves a lock.
  This observer does not create or change a reporting policy. Offline privacy
  becomes unknown instead of reusing a cached current-state claim.
- Plugin inventory/upgrades, lifecycle/restore events, recovery counters,
  hardware/Job Object samples and privacy policy enforcement belong to Host
  services. This Worker plugin does not infer their values or inspect complete
  plugin configurations, which can contain credentials.

## Performance and offline behavior

Each generation coalesces identical in-flight reads and caches responses for
5 seconds within the same local day. Prepared Session folds are cached by
durable revision, query cursor, calendar window and offset (up to 2,048
folds); unchanged cold observations use DSH's own revision-aware preparation
cache without materializing logs again. Live Sessions are refolded for current
turn duration and workspace summaries. Corrupt/unreadable Sessions produce
partial aggregate status without deleting valid peers.

The trusted Go overview service owns durable snapshots and their full RunContext
identity, local date, period, locale and timezone isolation. `dashboard-dsh.ts`
only validates/projects JSON: it writes no duplicate browser cache. Offline
data retains collection time, receives `dshStaleAt` even without a session list, and marks module
availability unavailable. Layout persistence is a separate UI responsibility.

## Sidebar integration

The Client registers only `sidebar.panellist`, never `main`. DSH's slot supplies
an icon inside a DSH-owned button; its normal handler requires a registered DSH
main panel. A capture listener on this single button opens the trusted Host
overview before that handler executes. Native button keyboard activation stays
intact. Public `layout.panelInfo` and `sessions.list` subscriptions dismiss the
overlay on navigation; a capture listener within that sidebar column also
covers reselecting an unchanged Session. Sidebar geometry is measured from the
slot's own containing `nav` parent, without reading application data or hashed
CSS classes.

Messages are version 1 and require the exact parent source and ancestor origin:

- Client → Host: `dsh-work/open-overview` and
  `dsh-work/overview-geometry`, each with `sidebarWidth`;
  `dsh-work/close-overview`; and `dsh-work/overview-activity` with integer
  `pending` and boolean `running`. The client derives these from the
  `useSessionStatus` store DSH passes to the slot icon (a session with a
  non-empty `pendingInteraction` is pending, otherwise `running` counts), so the
  badge needs no Host polling. The Host uses them for the title-bar toggle.
- Host → Client: `dsh-work/overview-badge` with boolean `offline`, from the
  Host lifecycle; `dsh-work/overview-opened` synchronizes a Host/top-bar
  open, and `dsh-work/overview-closed` clears selected styling. Replaying these
  messages after component readiness also restores state before icon mounting.
- Badge priority is pending count, offline `!`, then a running pulse. Reduced
  motion disables the pulse. The Lucide LayoutDashboard shape is copied from
  the application's pinned Lucide 1.48.0 ISC distribution.

Revisit the capture integration if DSH adds a public custom-action sidebar
launcher; its current public panel launcher always selects a main panel.

## Checks

```powershell
node --test internal/hostplugins/overview/*.test.mjs
npm run typecheck --prefix frontend
```

DSH service contracts were inspected in the locally installed rc.2, rc.3 and
rc.5 distributions. The account balance, schedule catalog and workspace-change
services exist in all three; actual composition can still omit a service, in
which case its availability is explicit. No running app was operated for these
source/aggregation checks.
