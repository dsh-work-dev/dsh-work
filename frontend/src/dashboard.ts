import Sortable from "sortablejs";
import {icon} from "./ui/icons";
import {t} from "./i18n";
import {dashboardLayoutKey, defaultDashboardLayout, normalizeDashboardLayout} from "./dashboard-layout";
import type {DashboardGroup, DashboardLayout, DashboardTile} from "./dashboard-layout";
import type {
  DashboardCallbacks,
  DashboardData,
  DashboardHandle,
  DashboardPeriod,
  DashboardSession,
  DashboardSettingsSection,
  DashboardState,
} from "./dashboard-model";
import "./ui/dashboard.css";

/** Grid span of each tile: columns of 12, rows of 116 px. */
const tileSize: Record<DashboardTile, [number, number]> = {
  orbit: [7, 4],
  output: [5, 2],
  results: [5, 2],
  timeline: [12, 1],
  cost: [3, 2],
  tokens: [5, 2],
  models: [4, 2],
  host: [4, 3],
  runtime: [4, 3],
  computer: [4, 3],
  health: [12, 1],
};

/** Which data field feeds each tile, for the partial/unavailable marker. */
const tileSource: Record<DashboardTile, keyof DashboardData> = {
  orbit: "sessions",
  output: "output",
  results: "sessions",
  timeline: "events",
  cost: "cost",
  tokens: "hourlyTokens",
  models: "models",
  host: "host",
  runtime: "runtime",
  computer: "computer",
  health: "health",
};

/** Tool names vary by plugin; the output tile groups them by what the agent was doing. */
const toolCategories: [string, RegExp][] = [
  ["browser", /browser|web|fetch|navigate/i],
  ["computer", /computer|screen|mouse|keyboard|window/i],
  ["edit", /edit|write|patch|apply|replace|create_file/i],
  ["read", /read|inspect|view|open_file|cat\b|list_dir/i],
  ["search", /grep|glob|search|find/i],
  ["command", /bash|shell|exec|command|terminal|powershell/i],
  ["skill", /skill/i],
];

const ORBIT = {cx: 400, cy: 250, rx: [180, 290, 365], ry: [110, 175, 220]};
const NODE_ANGLES = [
  [-30, 150, -90, 90, 30, 210],
  [-118, 28, 205, 112, -62, 165, 350],
  [-40, 140, 40, 220],
];

const esc = (value: unknown) =>
  String(value ?? "").replace(/[&<>"']/g, (c) => ({"&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;"})[c]!);
const known = (value: unknown): value is number => typeof value === "number" && Number.isFinite(value);
const clamp = (value: number) => Math.max(0, Math.min(100, value));
const svgIcon = (name: Parameters<typeof icon>[0]) => icon(name).outerHTML;
const reducedMotion = () => window.matchMedia("(prefers-reduced-motion: reduce)").matches;
const periods: DashboardPeriod[] = ["today", "week", "month"];

export function mountDashboard(container: HTMLElement, callbacks: DashboardCallbacks = {}): DashboardHandle {
  let data: DashboardData = {};
  let layout: DashboardLayout = defaultDashboardLayout();
  let editing = false;
  let pending = false;
  let wantedPeriod: DashboardPeriod | undefined;
  let periodRevision = 0;
  let dragging = false;
  let destroyed = false;
  let error = "";
  let sortables: Sortable[] = [];
  let orbitSignature = "";
  const gradientId = `dash-${Math.random().toString(36).slice(2)}`;

  try {
    layout = normalizeDashboardLayout(JSON.parse(localStorage.getItem(dashboardLayoutKey) || "null"));
  } catch {
    // An absent or unreadable saved layout keeps the default.
  }

  // ---------- formatting ----------
  const locale = () => data.locale || "zh-CN";
  const num = (value: number | null | undefined, compact = false) =>
    known(value)
      ? new Intl.NumberFormat(locale(), {maximumFractionDigits: 1, notation: compact ? "compact" : "standard"}).format(value)
      : "—";
  const percent = (fraction: number | null | undefined) => (known(fraction) ? `${num(fraction * 100)}%` : "—");
  const gigabytes = (bytes: number | null | undefined) => (known(bytes) ? `${num(bytes / 1024 ** 3)} GB` : "—");
  const memory = (bytes: number | null | undefined) =>
    !known(bytes) ? "—" : bytes >= 1024 ** 3 ? gigabytes(bytes) : `${num(Math.round(bytes / 1024 ** 2))} MB`;
  const clock = (at: string | undefined) =>
    at && !Number.isNaN(Date.parse(at))
      ? new Intl.DateTimeFormat(locale(), {hour: "2-digit", minute: "2-digit"}).format(new Date(at))
      : "—";
  const day = (at: number) => new Intl.DateTimeFormat(locale(), {month: "short", day: "numeric"}).format(new Date(at));
  const sameDay = (at: string | undefined) => !!at && new Date(at).toDateString() === new Date(data.now || Date.now()).toDateString();
  const when = (at: string | undefined) => (!at ? "—" : sameDay(at) ? clock(at) : `${day(Date.parse(at))} ${clock(at)}`);
  const duration = (seconds: number | null | undefined) => {
    if (!known(seconds)) return "—";
    const s = Math.max(0, Math.round(seconds));
    if (s < 60) return t("overview.duration.seconds", {n: s});
    if (s < 3600) return t("overview.duration.minutes", {n: Math.floor(s / 60)});
    if (s < 86400) return t("overview.duration.hours", {h: Math.floor(s / 3600), m: Math.floor((s % 3600) / 60)});
    return t("overview.duration.days", {d: Math.floor(s / 86400), h: Math.floor((s % 86400) / 3600)});
  };
  /** One number for narrow stat columns: minutes below an hour, then hours with one decimal. */
  const shortDuration = (seconds: number | null | undefined) => {
    if (!known(seconds)) return "—";
    const unit = seconds < 60 ? "second" : seconds < 3600 ? "minute" : "hour";
    const value = unit === "second" ? Math.round(seconds) : unit === "minute" ? Math.floor(seconds / 60) : seconds / 3600;
    return new Intl.NumberFormat(locale(), {style: "unit", unit, unitDisplay: "short", maximumFractionDigits: 1})
      .formatToParts(value)
      .map((part) => part.type === "unit" ? `<small>${esc(part.value)}</small>` : esc(part.value))
      .join("");
  };
  const money = (value: number | null | undefined, currency = "CNY") =>
    known(value) ? new Intl.NumberFormat(locale(), {style: "currency", currency, maximumFractionDigits: 2}).format(value) : "—";
  const shownPeriod = () => data.period ?? "today";
  const periodLabel = () => t(`overview.period.${shownPeriod()}`);

  // ---------- small parts ----------
  const empty = (message = t("overview.noData")) => `<div class="dash-empty">${esc(message)}</div>`;
  const dot = (state: string) => `<span class="dash-dot dash-${esc(state)}" aria-hidden="true"></span>`;
  const actionState = (enabled: boolean, busy = pending) => !enabled ? "disabled" : busy ? 'aria-disabled="true"' : "";
  const button = (label: string, action: string, {enabled = true, primary = false, focusKey, busy = pending}: {enabled?: boolean; primary?: boolean; focusKey?: string; busy?: boolean} = {}) =>
    `<button type="button" class="button button-compact ${primary ? "button-primary" : ""}" data-action="${esc(action)}" ${focusKey ? `data-focus-key="${esc(focusKey)}"` : ""} ${actionState(enabled, busy)}>${esc(label)}</button>`;
  const keyValues = (pairs: [string, string][]) =>
    `<dl class="dash-kv">${pairs.map(([k, v]) => `<dt>${esc(k)}</dt><dd title="${esc(v)}">${esc(v)}</dd>`).join("")}</dl>`;
  const sessionsKnown = () => !!data.sessions && (data.sessions.listCoverage ?? data.availability?.sessions?.state) !== "unavailable";
  const canOpenSession = (id: string | undefined) =>
    !!id && !id.startsWith("schedule:") && !!callbacks.onOpenSession &&
    data.host?.state === "running" && sessionsKnown() && !data.dshStaleAt;
  const sessionAction = (id: string) => `data-action="session:${esc(id)}"`;
  const hostActions = () => {
    const host = data.host;
    const available = !!host && !host.environmentBusy;
    const running = available && host.state === "running";
    const down = available && (host.state === "failed" || host.state === "stopped");
    const point = host?.restorePoint;
    const plugin = host?.suspectPluginId;
    return {
      switch: running ? callbacks.onSwitchEnvironment : undefined,
      save: running && host.canSaveRestorePoint ? callbacks.onSaveRestorePoint : undefined,
      restore: down && point && !point.unavailable && callbacks.onRestore ? () => callbacks.onRestore!(point.id) : undefined,
      retry: down && plugin && callbacks.onDisablePluginAndRetry ? () => callbacks.onDisablePluginAndRetry!(plugin) : undefined,
      upgrade: running && known(data.runtime?.pluginUpdates) && data.runtime.pluginUpdates > 0 ? callbacks.onUpgradePlugins : undefined,
    };
  };

  function availabilityBadge(tile: DashboardTile) {
    const key = tileSource[tile];
    const state = (tile === "orbit" ? data.availability?.orbit?.state : undefined) ?? data.availability?.[key]?.state;
    if (state === "partial") return `<span class="dash-availability" title="${esc(t("overview.partialHint"))}">${esc(t("overview.partial"))}</span>`;
    if (state === "unavailable" || data[key] === undefined) return `<span class="dash-availability">${esc(t("overview.unavailable"))}</span>`;
    return "";
  }

  function tileTitle(tile: DashboardTile) {
    if (tile === "orbit") return t(`overview.tile.orbit.${data.period || "today"}`);
    if (tile === "tokens" && (data.period === "week" || data.period === "month")) return t("overview.tile.tokensDaily");
    return t(`overview.tile.${tile}`);
  }

  // ---------- tiles ----------
  function orbit() {
    const sessions = data.sessions?.items;
    const live = sessions?.filter((s) => s.state === "running" || s.state === "waiting") ?? [];
    const results = sessions?.filter((s) => s.state === "completed" || s.state === "interrupted") ?? [];
    const scheduled = sessions?.filter((s) => s.state === "scheduled") ?? [];
    const running = data.host?.state === "running";
    const down = data.host?.state === "stopped" || data.host?.state === "failed";
    const count = (state: DashboardState) =>
      state === "completed" && known(data.output?.completed)
        ? num(data.output!.completed)
        : sessionsKnown()
          ? num(sessions!.filter((s) => s.state === state).length)
          : "—";

    const node = (s: DashboardSession, ring: number, index: number, total: number) => {
      const degrees = NODE_ANGLES[ring][index] ?? -100 + (index * 360) / Math.max(total, 1);
      const angle = (degrees * Math.PI) / 180;
      const x = ORBIT.cx + ORBIT.rx[ring] * Math.cos(angle);
      const y = ORBIT.cy + ORBIT.ry[ring] * Math.sin(angle);
      // Labels point away from the centre, except near the tile edge where they turn inward.
      const nearEdge = Math.abs(x - ORBIT.cx) > 220;
      const right = x >= ORBIT.cx !== nearEdge;
      const labelX = x + (right ? 14 : -14);
      const anchor = right ? "start" : "end";
      const sub = s.state === "scheduled" ? t("overview.runtime.next", {time: when(s.nextRunAt)}) : `${t(`overview.state.${s.state}`)} · ${when(s.updatedAt)}`;
      const title = s.title.length > 18 ? `${s.title.slice(0, 17)}…` : s.title;
      const focusable = s.state !== "scheduled" && canOpenSession(s.id) ? `role="button" tabindex="0" ${sessionAction(s.id)} ${actionState(true)}` : "";
      return `<g class="dash-node dash-${s.state}" ${focusable} aria-label="${esc(s.title)} · ${esc(sub)}">
        <title>${esc(s.title)} · ${esc(sub)}</title>
        <circle class="dash-halo" cx="${x}" cy="${y}" r="${ring === 0 ? 17 : 13}"/>
        <circle class="dash-node-core" cx="${x}" cy="${y}" r="${ring === 0 ? 8 : 5.5}"/>
        <text class="dash-node-label" x="${labelX}" y="${y - 2}" text-anchor="${anchor}">${esc(title)}</text>
        <text class="dash-node-sub" x="${labelX}" y="${y + 14}" text-anchor="${anchor}">${esc(sub)}</text>
      </g>`;
    };

    const animated = running && live.length > 0 && !reducedMotion();
    const rings = [0, 1, 2]
      .map((i) => `<ellipse class="dash-ring ${i === 2 ? "dash-scheduled" : ""}" cx="${ORBIT.cx}" cy="${ORBIT.cy}" rx="${ORBIT.rx[i]}" ry="${ORBIT.ry[i]}"/>`)
      .join("");
    const comet = animated
      ? `<circle class="dash-comet" r="3"><animateMotion dur="12s" repeatCount="indefinite" path="M${ORBIT.cx + ORBIT.rx[0]} ${ORBIT.cy} a${ORBIT.rx[0]} ${ORBIT.ry[0]} 0 1 1 -${ORBIT.rx[0] * 2} 0 a${ORBIT.rx[0]} ${ORBIT.ry[0]} 0 1 1 ${ORBIT.rx[0] * 2} 0"/></circle>`
      : "";
    const nodes = [
      ...live.slice(0, 6).map((s, i) => node(s, 0, i, Math.min(live.length, 6))),
      ...results.slice(0, 7).map((s, i) => node(s, 1, i, Math.min(results.length, 7))),
      ...scheduled.slice(0, 4).map((s, i) => node(s, 2, i, Math.min(scheduled.length, 4))),
    ].join("");

    const headline = down
      ? t("overview.core.stopped")
      : !running
        ? t("overview.core.unknown")
        : !sessionsKnown()
          ? t("overview.unavailable")
          : live.length
            ? t("overview.core.active", {count: live.length})
            : t("overview.core.idle");
    const subline = !sessionsKnown()
      ? ""
      : !live.length && !results.length ? t("overview.core.emptyPeriod") : t("overview.core.done", {count: count("completed")});
    const orbClass = down ? "dash-down" : animated ? "dash-orb-active" : "dash-orb-idle";
    const counts = (["running", "waiting", "completed", "interrupted"] as DashboardState[])
      .map((s) => `<span>${dot(s)}${esc(t(`overview.state.${s}`))} <b>${count(s)}</b></span>`)
      .join("");

    return `<div class="dash-orbit-heading"><h2>${esc(tileTitle("orbit"))}</h2>${availabilityBadge("orbit")}<div class="dash-counts">${counts}</div></div>
      <svg class="dash-orbit" viewBox="0 0 800 500" role="img" aria-label="${esc(tileTitle("orbit"))}">${rings}${comet}${nodes}</svg>
      <div class="dash-core-wrap"><div class="dash-orb ${orbClass}"></div><strong>${esc(headline)}</strong>${subline ? `<span>${esc(subline)}</span>` : ""}</div>`;
  }

  function output() {
    const o = data.output;
    if (!o) return empty();
    const lines =
      known(o.addedLines) || known(o.removedLines)
        ? `<span class="dash-positive">+${num(o.addedLines ?? 0)}</span> <span class="dash-negative">−${num(o.removedLines ?? 0)}</span>`
        : "—";
    const stats = `<div class="dash-stats4">
      <div><b>${num(o.completed)}</b><span>${esc(t("overview.output.completed"))}</span></div>
      <div><b class="dash-duration">${shortDuration(o.agentSeconds)}</b><span>${esc(t("overview.output.agentWork"))}</span></div>
      <div><b>${num(o.changedFiles)}</b><span>${esc(t("overview.output.files"))}</span></div>
      <div><b class="dash-lines">${lines}</b><span>${esc(t("overview.output.lines"))}</span></div>
    </div>`;
    if (!o.toolCalls?.length) return stats;

    const groups = new Map<string, {count: number; names: string[]}>();
    for (const call of o.toolCalls) {
      const category = toolCategories.find(([, pattern]) => pattern.test(call.name))?.[0] ?? "other";
      const group = groups.get(category) ?? {count: 0, names: []};
      group.count += call.count;
      group.names.push(`${call.name} ${call.count}`);
      groups.set(category, group);
    }
    const sorted = Array.from(groups.entries()).sort((a, b) => b[1].count - a[1].count);
    const total = sorted.reduce((sum, [, g]) => sum + g.count, 0);
    const segments = sorted
      .map(([id, g], i) => `<i class="dash-tool-${i}" style="flex:${g.count}" title="${esc(`${t(`overview.tool.${id}`)} ${g.count}\n${g.names.join("\n")}`)}"></i>`)
      .join("");
    const legend = sorted
      .map(([id, g], i) => `<span title="${esc(g.names.join("\n"))}"><i class="dash-tool-${i}"></i>${esc(t(`overview.tool.${id}`))} <b>${num(g.count)}</b></span>`)
      .join("");
    return `${stats}<div class="dash-tool-mix">
      <div class="dash-tool-caption"><span>${esc(t("overview.output.calls", {count: num(total)}))}</span><span>${esc(t("overview.output.failure", {rate: percent(o.toolFailureRate)}))}</span></div>
      <div class="dash-segbar">${segments}</div><div class="dash-legend">${legend}</div></div>`;
  }

  function results() {
    if (!sessionsKnown()) return empty();
    const items = data.sessions!.items.filter((s) => s.state === "completed" || s.state === "interrupted").slice(0, 3);
    if (!items.length) return empty(t("overview.noResults"));
    return `<div class="dash-results">${items
      .map(
        (s) => `<div class="dash-result">${dot(s.state)}
          <div class="dash-result-text"><button class="dash-text-button" ${canOpenSession(s.id) ? sessionAction(s.id) : ""} ${actionState(canOpenSession(s.id))}>${esc(s.title)}</button>
          <p>${esc(s.summary || t(`overview.state.${s.state}`))}</p></div>
          ${s.state === "interrupted" && canOpenSession(s.id) ? button(t("overview.continue"), `session:${s.id}`, {focusKey: `continue:${s.id}`}) : `<time>${esc(when(s.updatedAt))}</time>`}
        </div>`,
      )
      .join("")}</div>`;
  }

  function timeline() {
    if (!data.events) return empty();
    const now = Date.parse(data.now || new Date().toISOString());
    const start = new Date(now);
    start.setHours(0, 0, 0, 0);
    if (data.period === "week") start.setDate(start.getDate() - ((start.getDay() + 6) % 7));
    if (data.period === "month") start.setDate(1);
    const end = new Date(start);
    if (data.period === "month") end.setMonth(end.getMonth() + 1);
    else end.setDate(end.getDate() + (data.period === "week" ? 7 : 1));
    const events = data.events.filter((e) => Date.parse(e.at) >= start.getTime() && Date.parse(e.at) < end.getTime());
    if (!events.length) return empty(t("overview.noEvents"));

    const wide = data.period === "week" || data.period === "month";
    const times = events.map((e) => Date.parse(e.at));
    // Today zooms to the active span (at least an hour either side); week and month show the whole range.
    const from = wide ? start.getTime() : Math.min(...times, now) - 3600000;
    const to = wide ? end.getTime() : Math.max(...times, now) + 3600000;
    const x = (at: number) => clamp(((at - from) / (to - from)) * 100);
    const label = (at: number) => (wide ? day(at) : clock(new Date(at).toISOString()));
    const marks = events
      .map((e) => {
        const at = Date.parse(e.at);
        const text = `${wide ? day(at) + " " : ""}${clock(e.at)} · ${e.source} · ${e.title}${e.description ? ` · ${e.description}` : ""}`;
        const attrs = `class="dash-event dash-${e.state || "info"}" style="left:${x(at)}%" title="${esc(text)}" aria-label="${esc(text)}"`;
        return e.sessionId && canOpenSession(e.sessionId)
          ? `<button ${attrs} ${sessionAction(e.sessionId)} data-focus-key="event:${esc(e.id)}" ${actionState(true)}></button>`
          : `<span role="img" ${attrs}></span>`;
      })
      .join("");
    return `<div class="dash-timeline"><div class="dash-timeline-line"></div><div class="dash-timeline-past" style="width:${x(now)}%"></div>${marks}
      <span class="dash-now" style="left:${x(now)}%"></span>
      <div class="dash-time-axis"><span>${esc(label(from))}</span><span>${esc(t("overview.now"))} ${esc(label(now))}</span><span>${esc(label(to))}</span></div></div>`;
  }

  function cost() {
    const c = data.cost;
    if (!c) return empty();
    const spentKnown = known(c.spent);
    const big = spentKnown ? money(c.spent, c.currency) : money(c.balance, c.currency);
    const caption = spentKnown ? t("overview.cost.spent", {period: periodLabel()}) : t("overview.cost.balance");
    const rows = [
      known(c.tokens) ? `<span>${esc(t("overview.cost.tokens", {period: periodLabel(), tokens: num(c.tokens, true)}))}</span>` : "",
      spentKnown && known(c.balance) ? `<span>${esc(t("overview.cost.balance"))} <b>${esc(money(c.balance, c.currency))}</b></span>` : "",
      known(c.runwaySeconds) ? `<span>${esc(t("overview.cost.runway", {duration: duration(c.runwaySeconds)}))}</span>` : "",
    ].join("");
    const bar = known(c.consumedFraction) ? `<div class="dash-progress"><i style="width:${clamp(c.consumedFraction * 100)}%"></i></div>` : "";
    return `<div class="dash-cost"><div class="dash-big">${esc(big)}</div><span class="dash-label">${esc(caption)}</span><div class="dash-cost-rows">${rows}</div>${bar}</div>`;
  }

  function tokens() {
    const buckets = data.hourlyTokens;
    if (!buckets?.length) return empty();
    const now = Date.parse(data.now || new Date().toISOString());
    // Use the collector's fixed offset for the whole period. Missing buckets leave empty slots.
    const shift = -new Date(now).getTimezoneOffset() * 60000;
    const start = new Date(now + shift);
    start.setUTCHours(0, 0, 0, 0);
    const daily = data.period === "week" || data.period === "month";
    if (data.period === "week") start.setUTCDate(start.getUTCDate() - ((start.getUTCDay() + 6) % 7));
    if (data.period === "month") start.setUTCDate(1);
    const end = new Date(start);
    if (data.period === "month") end.setUTCMonth(end.getUTCMonth() + 1);
    else end.setUTCDate(end.getUTCDate() + (data.period === "week" ? 7 : 1));
    const step = daily ? 86400000 : 3600000;
    const slots = (end.getTime() - start.getTime()) / step;
    const label = new Intl.DateTimeFormat(locale(), {
      timeZone: "UTC",
      ...(daily ? {month: "short", day: "numeric"} : {hour: "2-digit", minute: "2-digit"}),
    });
    const at = (index: number) => label.format(start.getTime() + index * step);
    const counts = new Map<number, {cacheHit: number; cacheMiss: number; output: number}>();
    for (const b of buckets) {
      const time = Date.parse(b.at);
      const index = Math.floor((time + shift - start.getTime()) / step);
      if (!known(index) || index < 0 || index >= slots || time > now) continue;
      const totals = counts.get(index) ?? {cacheHit: 0, cacheMiss: 0, output: 0};
      totals.cacheHit += b.cacheHit;
      totals.cacheMiss += b.cacheMiss;
      totals.output += b.output;
      counts.set(index, totals);
    }
    if (!counts.size) return empty();
    const max = Math.max(...Array.from(counts.values(), (b) => b.cacheHit + b.cacheMiss + b.output));
    const bars = Array.from(counts)
      .map(([index, b]) => {
        const tip = `${at(index)} · ${t("overview.tokens.hit")} ${num(b.cacheHit, true)} · ${t("overview.tokens.miss")} ${num(b.cacheMiss, true)} · ${t("overview.tokens.output")} ${num(b.output, true)}`;
        const h = (v: number) => `${(v / (max || 1)) * 100}%`;
        return `<div style="grid-column:${index + 1}" title="${esc(tip)}"><i class="dash-hit" style="height:${h(b.cacheHit)}"></i><i class="dash-miss" style="height:${h(b.cacheMiss)}"></i><i class="dash-output" style="height:${h(b.output)}"></i></div>`;
      })
      .join("");
    return `<div class="dash-token-head"><div class="dash-token-legend"><span class="hit">${esc(t("overview.tokens.hit"))}</span><span class="miss">${esc(t("overview.tokens.miss"))}</span><span class="out">${esc(t("overview.tokens.output"))}</span></div>
      <span class="dash-label">${esc(t(daily ? "overview.tokens.peakDay" : "overview.tokens.peak", {value: num(max, true)}))}</span></div>
      <div class="dash-token-bars ${daily ? "dash-token-daily" : ""}" style="--dash-token-slots:${slots}" role="img" aria-label="${esc(tileTitle("tokens"))}">${bars}</div>
      <div class="dash-time-axis"><span>${esc(at(0))}</span><span>${esc(at(slots - 1))}</span></div>`;
  }

  function models() {
    if (!data.models && !known(data.cacheHitRate)) return empty();
    const rate = data.cacheHitRate;
    const max = Math.max(...(data.models ?? []).map((m) => m.tokens), 1);
    const list = data.models?.length
      ? data.models
          .slice(0, 4)
          .map(
            (m, i) => `<div class="dash-model"><div class="dash-model-label"><span title="${esc(m.name)}">${esc(m.name.split("/").pop())}</span>
              <span>${num(m.tokens, true)}${known(m.cost) ? ` · ${esc(money(m.cost, m.currency))}` : ""}</span></div>
              <div class="dash-progress dash-model-${i}"><i style="width:${(m.tokens / max) * 100}%"></i></div></div>`,
          )
          .join("")
      : empty();
    const delta = known(data.cacheDelta)
      ? `<span class="dash-label ${data.cacheDelta >= 0 ? "dash-positive" : "dash-negative"}">${esc(t("overview.models.delta", {value: `${data.cacheDelta >= 0 ? "+" : "−"}${num(Math.abs(data.cacheDelta) * 100)} pp`}))}</span>`
      : "";
    return `<div class="dash-donut-models">
      <div class="dash-donut"><svg viewBox="0 0 90 90"><circle class="dash-track" cx="45" cy="45" r="35"/><circle class="dash-donut-value" cx="45" cy="45" r="35" pathLength="100" stroke-dasharray="${known(rate) ? clamp(rate * 100) : 0} 100"/></svg>
        <strong>${esc(percent(rate))}</strong><small>${esc(t("overview.models.hitRate"))}</small></div>
      <div class="dash-model-list">${list}${delta}</div></div>`;
  }

  function host() {
    const h = data.host;
    if (!h) return empty();
    const down = h.state === "failed" || h.state === "stopped";
    const status = `<span class="dash-status dash-${h.state}">${dot(h.state)}${esc(t(`overview.host.${h.state}`))}</span>`;
    const uptime = h.state === "running" ? `<strong>${esc(duration(h.uptimeSeconds))}</strong>` : "";
    const recovered = known(h.recoveredCount) && h.recoveredCount > 0 ? `<span class="dash-label">${esc(t("overview.host.recovered", {count: h.recoveredCount}))}</span>` : "";
    const allowed = hostActions();
    const actions = down
      ? button(t("overview.host.retry"), "retry", {enabled: !!allowed.retry, primary: true}) +
        button(t("overview.host.restore"), "restore", {enabled: !!allowed.restore})
      : button(t("overview.host.switch"), "switch", {enabled: !!allowed.switch}) + button(t("overview.host.save"), "save", {enabled: !!allowed.save});
    const capabilities = (["browser", "computer", "pet", "notification"] as const)
      .map((id) => {
        const c = h.capabilities?.find((item) => item.id === id);
        const state = c?.state ?? "unknown";
        const section = id === "pet" ? "pets" : id === "notification" ? "notifications" : id;
        return `<button class="dash-capability dash-capability-${state}" data-action="settings:${section}" ${actionState(!!callbacks.onSettings)} title="${esc(c?.detail || "")}">
          ${dot(state === "ready" ? "ok" : state === "error" ? "error" : "unknown")}<span>${esc(t(`overview.cap.${id}`))}</span><small>${esc(c?.detail || t(`overview.cap.${state}`))}</small></button>`;
      })
      .join("");
    return `<div class="dash-host-state">${status}${uptime}${recovered}</div>
      ${h.failure ? `<p class="dash-failure">${esc(h.failure)}</p>` : ""}
      ${keyValues([
        ["DSH", h.dshVersion || "—"],
        ["Profile", h.profile || "—"],
        ["Node", h.nodeVersion || "—"],
        [t("overview.host.restorePoint"), h.restorePoint ? `${h.restorePoint.label}` : "—"],
      ])}
      <div class="dash-actions">${actions}</div>
      <div class="dash-capabilities">${capabilities}</div>`;
  }

  function runtime() {
    const r = data.runtime;
    if (!r) return empty();
    const updates = known(r.pluginUpdates) && r.pluginUpdates > 0 ? `<em class="dash-warning-text">${esc(t("overview.runtime.updates", {count: r.pluginUpdates}))}</em>` : "";
    const workspaces = r.workspaces?.length
      ? `<div class="dash-workspaces">${r.workspaces
          .slice(0, 4)
          .map((w) => {
            const status = !known(w.changedFiles) ? t("overview.runtime.gitUnavailable") : w.changedFiles ? t("overview.runtime.changed", {count: w.changedFiles}) : t("overview.runtime.clean");
            return `<div><span class="dash-workspace-name" title="${esc(w.name)}">${esc(w.name)}${w.branch ? `<small>${esc(w.branch)}</small>` : ""}</span><span class="${w.changedFiles ? "dash-warning-text" : ""}">${esc(status)}</span></div>`;
          })
          .join("")}</div>`
      : empty();
    return `<div class="dash-trio">
        <div><strong>${num(r.plugins)}</strong><span>${esc(t("overview.runtime.plugins"))}${known(r.disabledPlugins) && r.disabledPlugins ? ` · ${esc(t("overview.runtime.pluginsDetail", {disabled: r.disabledPlugins}))}` : ""}</span>${updates}</div>
        <div><strong>${num(r.skills)}</strong><span>${esc(t("overview.runtime.skills"))}</span></div>
        <div><strong>${num(r.scheduled)}</strong><span>${esc(t("overview.runtime.scheduled"))}</span>${r.nextScheduledAt ? `<em>${esc(t("overview.runtime.next", {time: when(r.nextScheduledAt)}))}</em>` : ""}</div>
      </div>
      <div class="dash-label dash-subhead">${esc(t("overview.runtime.workspaces"))}</div>${workspaces}
      <div class="dash-actions dash-push-bottom">${button(t("overview.runtime.upgrade"), "upgrade", {enabled: !!hostActions().upgrade, primary: true})}${button(t("overview.runtime.manage"), "settings:plugins", {enabled: !!callbacks.onSettings})}</div>`;
  }

  function gauge(value: number | null | undefined, agent: number | null | undefined, label: string, caption: string, id: string) {
    const arc = "M15 61 A35 35 0 1 1 75 61";
    const total = known(value) ? Math.round(clamp(value)) : null;
    const totalArc = total ? `<path class="dash-gauge-value" d="${arc}" pathLength="100" stroke-dasharray="${total} 200"/>` : "";
    const agentArc = known(agent) && agent > 0 ? `<path class="dash-gauge-agent" d="${arc}" pathLength="100" stroke="url(#${id})" stroke-dasharray="${clamp(agent)} 200"/>` : "";
    return `<div class="dash-gauge"><svg viewBox="0 0 90 70" role="img" aria-label="${esc(label)} ${total === null ? t("overview.unavailable") : total + "%"}">
        <defs><linearGradient id="${id}"><stop stop-color="var(--dash-agent-a)"/><stop offset="1" stop-color="var(--dash-agent-b)"/></linearGradient></defs>
        <path class="dash-gauge-track" d="${arc}" pathLength="100"/>${totalArc}${agentArc}
        <text x="45" y="50" text-anchor="middle">${total === null ? "—" : `${total}%`}</text></svg>
      <strong>${esc(label)}</strong><span>${esc(caption)}</span></div>`;
  }

  function computer() {
    const pc = data.computer;
    if (!pc) return empty();
    const share = (part: number | null | undefined, whole: number | null | undefined) => (known(part) && known(whole) && whole > 0 ? (part / whole) * 100 : null);
    const memoryShare = share(pc.memoryUsedBytes, pc.memoryTotalBytes);
    const agentMemoryShare = share(pc.agentMemoryBytes, pc.memoryTotalBytes);
    const diskShare = share(pc.diskUsedBytes, pc.diskTotalBytes);
    const free = known(pc.diskTotalBytes) && known(pc.diskUsedBytes) ? gigabytes(pc.diskTotalBytes - pc.diskUsedBytes) : "—";
    const end = Date.parse(pc.sampledAt || data.now || new Date().toISOString());
    const start = end - 3600000;
    const history = (pc.cpuHistory ?? [])
      .map((p) => ({at: Date.parse(p.at), percent: p.percent}))
      .filter((p) => p.at >= start && p.at <= end && known(p.percent))
      .sort((a, b) => a.at - b.at);
    const spark = history.length > 1
      ? `<svg class="dash-spark" viewBox="0 0 300 36" preserveAspectRatio="none" role="img" aria-label="${esc(t("overview.pc.trend"))}"><line x1="0" y1="35" x2="300" y2="35"/><polyline points="${history.map((p) => `${(p.at - start) / 12000},${35 - clamp(p.percent) * 0.34}`).join(" ")}"/></svg>`
      : "";
    // Console hosts belong to the shell that opened them; listing them separately is noise.
    const processes = pc.processes?.filter((p) => !/^conhost(\.exe)?$/i.test(p.name)).sort((a, b) => (b.memoryBytes ?? 0) - (a.memoryBytes ?? 0)).slice(0, 4);
    const list = !processes
      ? ""
      : processes.length
        ? processes
            .map((p) => `<div><span title="${esc(`${p.name} · PID ${p.pid}`)}">${esc(p.name.replace(/\.exe$/i, ""))}${p.role ? `<small>${esc(p.role)}</small>` : ""}</span><span>${known(p.cpuPercent) ? `${num(p.cpuPercent)}%` : "—"} · ${esc(memory(p.memoryBytes))}</span></div>`)
            .join("")
        : `<div class="dash-muted-row">${esc(t("overview.pc.noProcesses"))}</div>`;
    return `<div class="dash-gauges">
        ${gauge(pc.cpuPercent, pc.agentCpuPercent, "CPU", known(pc.agentCpuPercent) ? t("overview.pc.agent", {value: `${num(pc.agentCpuPercent)}%`}) : "", `${gradientId}-cpu`)}
        ${gauge(memoryShare, agentMemoryShare, t("overview.pc.memory"), `${gigabytes(pc.memoryUsedBytes)} / ${gigabytes(pc.memoryTotalBytes)}`, `${gradientId}-mem`)}
        ${gauge(diskShare, null, pc.diskLabel || t("overview.pc.disk"), t("overview.pc.free", {size: free}), `${gradientId}-disk`)}
      </div>
      ${spark ? `<div class="dash-spark-row"><span class="dash-label">${esc(t("overview.pc.trend"))}</span>${spark}</div>` : ""}
      ${list ? `<div class="dash-label dash-subhead">${esc(t("overview.pc.processes"))}</div><div class="dash-processes">${list}</div>` : ""}`;
  }

  function health() {
    const items = (["diagnostics", "updates", "storage", "browser", "privacy"] as const)
      .map((id) => {
        const h = data.health?.find((item) => item.id === id);
        const state = h?.state || "unknown";
        const content = `${dot(state)}<strong>${esc(t(`overview.health.${id}`))}</strong><span>${esc(h?.summary || t("overview.unavailable"))}</span>`;
        return id === "privacy"
          ? `<div class="dash-health-item dash-health-readonly dash-health-${state}">${content}</div>`
          : `<button class="dash-health-item dash-health-${state}" data-action="settings:${id}" ${actionState(!!callbacks.onSettings)}>${content}</button>`;
      })
      .join("");
    return `<div class="dash-health">${items}</div>`;
  }

  const bodies: Record<DashboardTile, () => string> = {orbit, output, results, timeline, cost, tokens, models, host, runtime, computer, health};

  function tile(id: DashboardTile, index: number, list: DashboardTile[]) {
    const [cols, rows] = tileSize[id];
    const heading = id === "orbit" ? "" : `<h2>${esc(tileTitle(id))}${availabilityBadge(id)}</h2>`;
    const tools = editing
      ? `<div class="dash-tile-edit">
          <span class="dash-drag-handle" aria-hidden="true" title="${esc(t("overview.drag"))}">${svgIcon("sliders-horizontal")}</span>
          <button data-action="tile-up:${id}" aria-label="${esc(t("overview.moveEarlier"))}" ${index === 0 ? "disabled" : ""}>${svgIcon("chevron-left")}</button>
          <button data-action="tile-down:${id}" aria-label="${esc(t("overview.moveLater"))}" ${index === list.length - 1 ? "disabled" : ""}>${svgIcon("chevron-right")}</button></div>`
      : "";
    return `<section class="dashboard-tile group dash-cols-${cols} dash-rows-${rows} dash-tile-${id} ${id === "orbit" ? "dash-hero" : ""}" data-tile="${id}" aria-label="${esc(tileTitle(id))}">${heading}${tools}<div class="dash-tile-body" ${editing ? "inert" : ""}>${bodies[id]()}</div></section>`;
  }

  function group(id: DashboardGroup, index: number) {
    const move = editing
      ? `<div class="dash-group-move">${button(t("overview.moveUp"), `group-up:${id}`, {enabled: index > 0, busy: false})}${button(t("overview.moveDown"), `group-down:${id}`, {enabled: index < layout.groups.length - 1, busy: false})}</div>`
      : "";
    return `<section class="dashboard-module-group" data-group="${id}">
      <header class="dashboard-group-label"><h2>${esc(t(`overview.group.${id}`))}</h2><span></span>${move}</header>
      <div class="dashboard-grid">${layout.tiles[id].map(tile).join("")}</div></section>`;
  }

  function header() {
    const now = new Intl.DateTimeFormat(locale(), {month: "short", day: "numeric", weekday: "short", hour: "2-digit", minute: "2-digit"}).format(new Date(data.now || Date.now()));
    const periodLoading = wantedPeriod !== undefined && wantedPeriod !== shownPeriod();
    const periodButtons = periods
      .map((p) => `<button data-action="period:${p}" aria-pressed="${shownPeriod() === p}" ${!callbacks.onPeriodChange ? "disabled" : ""} ${periodLoading && wantedPeriod === p ? "data-loading" : ""}>${esc(t(`overview.period.${p}`))}</button>`)
      .join("");
    const stale = data.dshStaleAt
      ? t("overview.lastKnown", {time: new Intl.DateTimeFormat(locale(), {dateStyle: "short", timeStyle: "short"}).format(new Date(data.dshStaleAt))})
      : "";
    const notice = [stale, data.sourceError].filter(Boolean).join(" · ");
    return `<header class="dashboard-head"><h1>${esc(t("overview.title"))}</h1><time>${esc(now)}</time><span class="dash-head-spacer"></span>
        ${button(t(editing ? "overview.done" : "overview.edit"), "edit", {primary: editing, busy: false})}${editing ? button(t("overview.reset"), "reset", {busy: false}) : ""}
        <div class="dash-period">${periodButtons}</div></header>
      ${notice ? `<div class="dash-stale">${esc(notice)}</div>` : ""}
      ${error ? `<div class="dash-error" role="alert">${esc(error)}</div>` : ""}
      ${pending || periodLoading ? `<span class="sr-only" role="status">${esc(t("overview.busy"))}</span>` : ""}`;
  }

  // ---------- rendering ----------
  function save() {
    try {
      localStorage.setItem(dashboardLayoutKey, JSON.stringify(layout));
    } catch {
      error = t("overview.storageFailed");
    }
  }

  function attachSortables() {
    container.querySelectorAll<HTMLElement>(".dashboard-grid").forEach((grid) => {
      const groupId = grid.parentElement!.dataset.group as DashboardGroup;
      sortables.push(
        new Sortable(grid, {
          handle: ".dash-drag-handle",
          draggable: ".dashboard-tile",
          dataIdAttr: "data-tile",
          forceFallback: true,
          fallbackOnBody: true,
          fallbackTolerance: 4,
          animation: 150,
          group: {name: groupId, pull: false, put: false},
          ghostClass: "dash-drag-ghost",
          onChoose: () => {
            dragging = true;
          },
          onUnchoose: () => {
            // Even a press that never becomes a drag must release the redraw hold.
            queueMicrotask(() => {
              dragging = false;
              render();
            });
          },
          onEnd: ({oldIndex, newIndex}) => {
            const order = Sortable.get(grid)?.toArray();
            if (order && oldIndex !== newIndex) {
              layout.tiles[groupId] = order as DashboardTile[];
              save();
            }
          },
        }),
      );
    });
  }

  function render() {
    if (destroyed || dragging) return;
    sortables.forEach((s) => s.destroy());
    sortables = [];
    const scroll = container.scrollTop;
    const active = document.activeElement as HTMLElement | SVGElement | null;
    const focused = active && container.contains(active) ? active : null;
    const tileId = focused?.closest<HTMLElement>("[data-tile]")?.dataset.tile;
    const groupId = !tileId ? focused?.closest<HTMLElement>("[data-group]")?.dataset.group : undefined;
    const actionKey = focused?.dataset.focusKey ?? focused?.dataset.action;
    // Keep the orbit's DOM when nothing it shows has changed, so the comet animation does not restart on every poll.
    const previousOrbit = container.querySelector<HTMLElement>(".dash-hero .dash-tile-body");
    const nextSignature = JSON.stringify([data.host?.state, data.sessions, data.output?.completed, data.locale, data.period, data.availability?.sessions, data.availability?.orbit, data.dshStaleAt, pending]);

    container.classList.add("dashboard-host");
    container.innerHTML = `<div class="dashboard-page ${editing ? "dash-editing" : ""}"><div class="dashboard-glow" aria-hidden="true"></div>${header()}${layout.groups.map(group).join("")}</div>`;
    if (previousOrbit && orbitSignature === nextSignature) {
      previousOrbit.inert = editing;
      container.querySelector(".dash-hero .dash-tile-body")?.replaceWith(previousOrbit);
    }
    orbitSignature = nextSignature;
    container.scrollTop = scroll;
    if (actionKey) {
      const scope = tileId
        ? container.querySelector(`[data-tile="${CSS.escape(tileId)}"]`)
        : groupId ? container.querySelector(`[data-group="${CSS.escape(groupId)}"]`) : container;
      const key = focused?.dataset.focusKey ? "data-focus-key" : "data-action";
      scope?.querySelector<HTMLElement | SVGElement>(`[${key}="${CSS.escape(actionKey)}"]:not(:disabled)`)?.focus({preventScroll: true});
    }
    if (editing) attachSortables();
  }

  function swap<T>(list: T[], index: number, other: number) {
    if (index < 0 || other < 0 || other >= list.length) return false;
    [list[index], list[other]] = [list[other], list[index]];
    return true;
  }

  function focusMove(action: string, inverse: string) {
    for (const candidate of [action, inverse]) {
      const button = container.querySelector<HTMLButtonElement>(`[data-action="${CSS.escape(candidate)}"]:enabled`);
      if (button) {
        button.focus();
        button.scrollIntoView({block: "nearest", inline: "nearest"});
        break;
      }
    }
  }

  const actionFailed = (e: unknown) => t("overview.actionFailed", {message: e instanceof Error ? e.message : String(e)});

  async function choosePeriod(period: DashboardPeriod) {
    if (!callbacks.onPeriodChange || period === (wantedPeriod ?? shownPeriod())) return;
    const revision = ++periodRevision;
    wantedPeriod = period;
    error = "";
    render();
    try {
      await callbacks.onPeriodChange(period);
    } catch (e) {
      if (revision !== periodRevision) return;
      wantedPeriod = shownPeriod();
      error = actionFailed(e);
      render();
    }
  }

  async function run(action: string) {
    if (dragging) return;
    const [kind, ...rest] = action.split(":");
    const id = rest.join(":");

    if (kind === "period") {
      const period = periods.find((p) => p === id);
      if (period) await choosePeriod(period);
      return;
    }

    if (kind === "edit") {
      editing = !editing;
      render();
      return;
    }
    if (kind === "reset") {
      layout = defaultDashboardLayout();
      error = "";
      try {
        localStorage.removeItem(dashboardLayoutKey);
      } catch {
        error = t("overview.storageFailed");
      }
      render();
      return;
    }
    if (kind === "group-up" || kind === "group-down") {
      const index = layout.groups.indexOf(id as DashboardGroup);
      if (swap(layout.groups, index, index + (kind === "group-up" ? -1 : 1))) {
        save();
        render();
        focusMove(action, `group-${kind === "group-up" ? "down" : "up"}:${id}`);
      }
      return;
    }
    if (kind === "tile-up" || kind === "tile-down") {
      const owner = layout.groups.find((g) => layout.tiles[g].includes(id as DashboardTile));
      if (!owner) return;
      const tiles = layout.tiles[owner];
      const index = tiles.indexOf(id as DashboardTile);
      if (swap(tiles, index, index + (kind === "tile-up" ? -1 : 1))) {
        save();
        render();
        focusMove(action, `tile-${kind === "tile-up" ? "down" : "up"}:${id}`);
      }
      return;
    }

    if (pending || editing) return;
    const calls: Record<string, (() => void | Promise<void>) | undefined> = {
      ...hostActions(),
      session: canOpenSession(id) ? () => callbacks.onOpenSession?.(id) : undefined,
      settings: () => callbacks.onSettings?.(id as DashboardSettingsSection),
    };
    const call = calls[kind];
    if (!call) return;
    pending = true;
    error = "";
    render();
    try {
      await call();
    } catch (e) {
      error = actionFailed(e);
    } finally {
      pending = false;
      render();
    }
  }

  const onClick = (event: Event) => {
    const target = (event.target as Element).closest<HTMLElement>("[data-action]");
    if (target && container.contains(target) && !target.matches(':disabled,[aria-disabled="true"]') && !target.closest("[inert]")) {
      void run(target.dataset.action!);
    }
  };
  const onKey = (event: KeyboardEvent) => {
    if ((event.key === "Enter" || event.key === " ") && (event.target as Element).matches("g[data-action]")) {
      event.preventDefault();
      if (!event.repeat) onClick(event);
    }
  };
  container.addEventListener("click", onClick);
  container.addEventListener("keydown", onKey);
  render();

  return {
    update(next) {
      // A previous choice can finish after the currently requested period.
      if (wantedPeriod && (next.period ?? "today") !== wantedPeriod) return;
      data = next;
      render();
    },
    destroy() {
      destroyed = true;
      sortables.forEach((s) => s.destroy());
      container.removeEventListener("click", onClick);
      container.removeEventListener("keydown", onKey);
      container.replaceChildren();
      container.classList.remove("dashboard-host");
    },
  };
}
