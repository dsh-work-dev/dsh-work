import type {
  AutomationService,
  DiagnosticsService,
  HostService,
  OverviewService,
  SettingsService,
  ShellService,
} from "../bindings/github.com/local/dsh-work/internal/desktopclient";
import type {Snapshot as ManagerSnapshot, PluginInfo, RunContext} from "../bindings/github.com/local/dsh-work/internal/dshmanager";
import {parseDashboardDSH} from "./dashboard-dsh";
import type {DashboardCapability, DashboardData, DashboardEvent, DashboardHealth, DashboardHost, DashboardPeriod} from "./dashboard-model";
import {t} from "./i18n";
import type {Locale} from "./i18n";

type Result<F extends (...args: never[]) => unknown> = Awaited<ReturnType<F>>;

/** Everything one refresh read. Any source may be missing; its tiles then show as unavailable. */
export interface OverviewSources {
  period: DashboardPeriod;
  locale: Locale;
  now: Date;
  host?: Result<typeof HostService.GetWorkspaceStatus>;
  manager?: ManagerSnapshot;
  source?: Result<typeof OverviewService.GetSnapshot>;
  automation?: Result<typeof AutomationService.GetAutomation>;
  preferences?: Result<typeof SettingsService.GetSettings>;
  update?: Result<typeof HostService.GetUpdateState>;
  problems?: Result<typeof DiagnosticsService.GetProblems>;
  pet?: Result<typeof ShellService.GetPet>;
  plugins?: PluginInfo[];
}

/** Identity of a Run context, matching the daemon's ProfileKey encoding. */
export function contextKey(value: RunContext | null | undefined): string {
  return value
    ? JSON.stringify([value.runtimeId, value.profile.dataDirectoryId, value.profile.name, value.node.kind, value.node.installationId ?? ""])
    : "";
}

export function targetOf(manager: ManagerSnapshot | undefined): RunContext | undefined {
  return manager?.current ?? manager?.configured ?? undefined;
}

/** Enabled and prepared counts as on; whether a browser or the DSH gateway is connected right now is a detail, not an outage. */
export function capability(
  id: "browser" | "computer",
  slot: {enabled: boolean; dependency: string; availability: string} | undefined,
): DashboardCapability {
  if (!slot) return {id, state: "unknown"};
  if (!slot.enabled) return {id, state: "disabled"};
  if (slot.dependency === "failed") return {id, state: "error", detail: t("overview.cap.error")};
  if (slot.dependency === "preparing") return {id, state: "unknown", detail: t("overview.cap.preparing")};
  if (slot.dependency !== "prepared") return {id, state: "unknown", detail: t("overview.cap.notInstalled")};
  if (slot.availability === "available") return {id, state: "ready"};
  return {id, state: "ready", detail: t(id === "browser" ? "overview.cap.browserIdle" : "overview.cap.computerIdle")};
}

const hostStates: Record<string, DashboardHost["state"]> = {Ready: "running", Starting: "starting", Stopping: "starting", Failed: "failed"};

function samePlace(point: {target: {profile: {name: string; dataDirectoryId: string}}}, target: RunContext | undefined) {
  return !!target && point.target.profile.name === target.profile.name && point.target.profile.dataDirectoryId === target.profile.dataDirectoryId;
}

function health(s: OverviewSources, privacy: DashboardHealth | undefined): DashboardHealth[] {
  const updatePhase = s.update?.phase;
  const pc = s.source?.computer;
  const browser = s.automation?.browser.dependency;
  return [
    s.problems
      ? {id: "diagnostics", state: s.problems.length ? "warning" : "ok", summary: s.problems.length ? t("overview.health.problems", {count: s.problems.length}) : t("overview.health.noProblems")}
      : {id: "diagnostics", state: "unknown", summary: t("overview.unavailable")},
    {
      id: "updates",
      state: !updatePhase ? "unknown" : updatePhase === "available" || updatePhase === "ready" ? "warning" : updatePhase === "error" ? "error" : updatePhase === "up-to-date" ? "ok" : "unknown",
      summary: updatePhase ? t(`overview.update.${updatePhase}`) : t("overview.unavailable"),
    },
    pc?.disk
      ? {id: "storage", state: "ok", summary: t("overview.health.diskFree", {size: `${((pc.disk.totalBytes - pc.disk.usedBytes) / 2 ** 30).toFixed(1)} GB`})}
      : {id: "storage", state: "unknown", summary: t("overview.unavailable")},
    {
      id: "browser",
      state: !browser ? "unknown" : browser === "failed" ? "error" : browser === "prepared" ? "ok" : "warning",
      summary: browser ? t(`overview.browser.${browser}`) : t("overview.unavailable"),
    },
    privacy ?? {id: "privacy", state: "unknown", summary: t("overview.unavailable")},
  ];
}

function hostEvents(s: OverviewSources, target: RunContext | undefined, pointLabel: (at: string) => string): DashboardEvent[] {
  const events: DashboardEvent[] = (s.source?.events ?? []).map((event, i) => {
    const kind = event.kind || "lifecycle";
    const failed = event.state === "failed" || event.state === "unavailable";
    const title = kind === "lifecycle"
      ? t(`overview.lifecycle.${event.state}`)
      : t(failed ? "overview.event.failed" : "overview.event.done", {name: t(`overview.event.${kind}`)});
    return {id: event.id || `host-${event.at}-${i}`, at: event.at, title, source: "DSH Work", state: "info"};
  });
  for (const point of s.manager?.restorePoints?.points ?? []) {
    if (!samePlace(point, target)) continue;
    events.push({id: `point-${point.id}`, at: point.createdAt, title: t("overview.event.restorePoint", {label: point.label || pointLabel(point.createdAt)}), source: "DSH Work", state: "info"});
  }
  return events;
}

/** Only failed writes are "could not be saved"; a failed read just means older history is missing. */
function sourceNotice(source: OverviewSources["source"], dshNotice: string | undefined): string | undefined {
  if (!source) return t("overview.notice.unavailable");
  const errors = Object.keys(source.persistenceErrors ?? {});
  if (errors.some((key) => key.endsWith("Save"))) return t("overview.notice.saveFailed");
  if (source.dshError) return t("overview.notice.dshUnavailable");
  return dshNotice;
}

/** Projects the Host's sources into the dashboard's presentation model. Pure, so it can be tested without bindings. */
export function buildDashboardData(s: OverviewSources): DashboardData {
  const target = targetOf(s.manager);
  // A snapshot collected for another Run context must not be shown as this one's.
  let source = s.source;
  if (source?.profileKey) {
    try {
      if (contextKey(JSON.parse(source.profileKey)) !== contextKey(target)) source = {...source, dshPayload: "", staleAt: ""};
    } catch {
      source = {...source, dshPayload: "", staleAt: ""};
    }
  }
  let dsh: Partial<DashboardData> = {};
  if (source?.dshPayload) {
    try {
      dsh = parseDashboardDSH(source.dshPayload, source.staleAt);
    } catch {
      // An invalid payload leaves the DSH tiles unavailable.
    }
  }
  // Without an authoritative bill the balance is real but spending is not: the cost tile is partial.
  if (dsh.cost && dsh.availability?.billing?.state === "unavailable") dsh.availability = {...dsh.availability, cost: {state: "partial"}};

  const pointLabel = (at: string) =>
    new Intl.DateTimeFormat(s.locale, {month: "short", day: "numeric", hour: "2-digit", minute: "2-digit"}).format(new Date(at));
  const point = s.manager?.restorePoints?.points?.filter((p) => samePlace(p, target)).sort((a, b) => b.createdAt.localeCompare(a.createdAt))[0];
  const profile = s.manager?.profiles?.find((p) => samePlace({target: {profile: p.ref}}, target));
  const pc = source?.computer;
  const plugins = s.plugins;
  const checked = plugins?.every((p) => p.updateCheck === "current" || p.updateCheck === "available");

  return {
    ...dsh,
    locale: s.locale,
    period: s.period,
    now: s.now.toISOString(),
    sourceError: sourceNotice(source, dsh.sourceError),
    host: {
      state: s.host ? hostStates[s.host.state] ?? "stopped" : "unknown",
      uptimeSeconds: source?.uptimeSeconds,
      recoveredCount: source?.automaticRecoveries,
      recoveryTrackingSince: source?.recoveryTrackingSince,
      dshVersion: s.manager?.runtimes?.find((r) => r.id === target?.runtimeId)?.version,
      profile: target?.profile.name,
      nodeVersion: s.host?.launchSelection?.nodeVersion,
      restorePoint: point ? {id: point.id, label: point.label || pointLabel(point.createdAt), at: point.createdAt} : undefined,
      failure: s.host?.error?.summary,
      suspectPluginId: s.host?.pluginFault?.plugins?.find((p) => p.enabled && p.suspected)?.package,
      capabilities: [
        capability("browser", s.automation?.browser),
        capability("computer", s.automation?.computer),
        {id: "pet", state: s.pet ? (s.pet.ready ? "ready" : "disabled") : "unknown"},
        {id: "notification", state: s.preferences ? (s.preferences.notifications.enabled ? "ready" : "disabled") : "unknown"},
      ],
    },
    runtime: {
      ...dsh.runtime,
      plugins: plugins?.length ?? profile?.pluginCount,
      disabledPlugins: plugins?.filter((p) => p.disabled).length,
      pluginUpdates: checked ? plugins!.filter((p) => p.updateCheck === "available").length : undefined,
    },
    events: [...(dsh.events ?? []), ...hostEvents(s, target, pointLabel)],
    health: health(s, dsh.health?.find((h) => h.id === "privacy")),
    computer: pc
      ? {
          sampledAt: pc.sampledAt,
          cpuPercent: pc.cpuPercent,
          agentCpuPercent: pc.agents.cpuPercent,
          memoryUsedBytes: pc.memory?.usedBytes,
          memoryTotalBytes: pc.memory?.totalBytes,
          agentMemoryBytes: pc.agents.memoryBytes,
          diskUsedBytes: pc.disk?.usedBytes,
          diskTotalBytes: pc.disk?.totalBytes,
          diskLabel: pc.disk?.path.match(/^[A-Za-z]:/)?.[0],
          cpuHistory: pc.history?.filter((p) => p.cpuPercent !== null).map((p) => ({at: p.at, percent: p.cpuPercent!})),
          processes: pc.agents.processes?.map((p) => ({
            pid: p.pid,
            name: p.name,
            role: p.role ? t(`overview.role.${p.role}`) : undefined,
            cpuPercent: p.cpuPercent,
            memoryBytes: p.memoryBytes,
          })),
        }
      : undefined,
  };
}
