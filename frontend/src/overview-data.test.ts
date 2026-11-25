import assert from "node:assert/strict";
import test from "node:test";
import {availablePluginUpgrades, buildDashboardData, capability} from "./overview-data";
import type {OverviewSources} from "./overview-data";

const target = {runtimeId: "dsh-0.2.1", node: {kind: "bundled"}, profile: {dataDirectoryId: "dsh-work", name: "dsh-work"}};
const otherTarget = {runtimeId: "dsh-0.2.1", node: {kind: "bundled"}, profile: {dataDirectoryId: "dsh-work", name: "other"}};

function sources(extra: Partial<OverviewSources> = {}): OverviewSources {
  return {
    period: "today",
    locale: "zh-CN",
    now: new Date("2026-11-09T08:00:00Z"),
    manager: {current: target, configured: target, runtimes: [], profiles: [], restorePoints: {points: []}} as never,
    ...extra,
  };
}

function source(extra: Record<string, unknown> = {}) {
  return {
    dshPayload: JSON.stringify({
      schemaVersion: 1, collectedAt: "2026-11-09T07:00:00.000Z",
      availability: {sessions: {state: "ready"}}, data: {sessions: {items: []}},
    }),
    profileKey: JSON.stringify(target), events: [], automaticRecoveries: 0, recoveryTrackingSince: "", ...extra,
  } as never;
}

test("a prepared capability without a live connection is on, with the reason as detail", () => {
  assert.deepEqual(capability("browser", {enabled: true, dependency: "prepared", availability: "available"}), {id: "browser", state: "ready"});
  const idle = capability("browser", {enabled: true, dependency: "prepared", availability: "unavailable"});
  assert.equal(idle.state, "ready");
  assert.ok(idle.detail);
  assert.equal(capability("computer", {enabled: false, dependency: "prepared", availability: "disabled"}).state, "disabled");
  assert.equal(capability("computer", {enabled: true, dependency: "failed", availability: "unavailable"}).state, "error");
  assert.equal(capability("computer", undefined).state, "unknown");
});

test("an unlabelled restore point is named by its time, and only this profile's points become events", () => {
  const points = [
    {id: "auto", label: "", createdAt: "2026-11-06T02:51:00Z", target: {profile: target.profile}},
    {id: "other", label: "x", createdAt: "2026-11-07T00:00:00Z", target: {profile: otherTarget.profile}},
  ];
  const data = buildDashboardData(sources({manager: {current: target, runtimes: [], profiles: [], restorePoints: {points}} as never}));
  assert.equal(data.host?.restorePoint?.id, "auto");
  assert.ok(data.host?.restorePoint?.label);
  assert.deepEqual(data.events?.map((e) => e.id), ["point-auto"]);
});

test("another Run context's DSH data is dropped but this computer's data stays", () => {
  const data = buildDashboardData(sources({
    source: source({profileKey: JSON.stringify(otherTarget), staleAt: "2026-11-09T07:00:00.000Z", computer: {sampledAt: "", cpuPercent: 12, agents: {processes: []}, history: []}}),
  }));
  assert.equal(data.sessions, undefined);
  assert.equal(data.dshStaleAt, undefined);
  assert.equal(data.computer?.cpuPercent, 12);
});

test("cached scheduled entries do not turn unavailable conversations into a known-empty list", () => {
  const collectedAt = "2026-11-09T07:00:00.000Z";
  const scheduled = {id: "schedule:review", title: "Review", state: "scheduled"};
  const project = (state: "ready" | "unavailable", cached: boolean) => buildDashboardData(sources({source: source({
    staleAt: cached ? collectedAt : "",
    dshPayload: JSON.stringify({
      schemaVersion: 1, collectedAt, availability: {sessions: {state}, schedules: {state: "ready"}},
      data: {sessions: {items: [scheduled]}},
    }),
  })}));

  const live = project("unavailable", false);
  assert.equal(live.availability?.orbit.state, "partial");
  assert.equal(live.sessions?.listCoverage, "unavailable");
  const cached = project("unavailable", true);
  assert.equal(cached.dshStaleAt, collectedAt);
  assert.deepEqual(cached.sessions?.items, [scheduled]);
  assert.equal(cached.sessions?.listCoverage, "unavailable");
  assert.equal(cached.availability?.sessions.state, "unavailable");
  const cachedEmpty = project("ready", true);
  assert.equal(cachedEmpty.sessions?.listCoverage, "ready");
  assert.equal(cachedEmpty.availability?.sessions.state, "unavailable");
});

test("cached workspaces retain their synchronization time without a session list", () => {
  const collectedAt = "2026-11-09T07:00:00.000Z";
  const workspaces = [{id: "work", name: "Work"}, {id: "clean", name: "Clean", branch: "main", changedFiles: 0}];
  const data = buildDashboardData(sources({source: source({
    staleAt: collectedAt,
    dshPayload: JSON.stringify({
      schemaVersion: 1, collectedAt,
      availability: {sessions: {state: "unavailable"}, workspaces: {state: "ready"}, git: {state: "partial"}},
      data: {runtime: {workspaces}},
    }),
  })}));
  assert.equal(data.sessions, undefined);
  assert.equal(data.dshStaleAt, collectedAt);
  assert.deepEqual(data.runtime?.workspaces, workspaces);
  assert.equal(data.availability?.runtime.state, "unavailable");
});

test("only failed writes are reported as not saved", () => {
  const readOnly = buildDashboardData(sources({source: source({persistenceErrors: {recoverySource: "manager busy", cacheLoad: "too large"}})}));
  const saved = buildDashboardData(sources({source: source({persistenceErrors: {cacheSave: "disk full"}})}));
  assert.equal(readOnly.sourceError, undefined);
  assert.ok(saved.sourceError);
});

test("upgrade count and action agree on known updates when other checks are unknown", () => {
  const plugins = [
    {package: "@test/alpha", updateCheck: "available", incompatible: {version: "1.0.0"}},
    {package: "@test/alpha", updateCheck: "available"},
    {package: "@test/beta", updateCheck: "unknown"},
    {package: "@test/gamma", updateCheck: "current", disabled: true},
  ] as never;
  const checked = buildDashboardData(sources({plugins}));
  assert.deepEqual(availablePluginUpgrades(plugins), ["@test/alpha"]);
  assert.equal(checked.runtime?.pluginUpdates, 1);
  assert.equal(checked.runtime?.disabledPlugins, 1);
  const noResult = buildDashboardData(sources({plugins: [{package: "@test/beta", updateCheck: "unknown"}] as never}));
  assert.equal(noResult.runtime?.pluginUpdates, undefined);
  const allCurrent = buildDashboardData(sources({plugins: [{package: "@test/beta", updateCheck: "current"}] as never}));
  assert.equal(allCurrent.runtime?.pluginUpdates, 0);
});

test("retry names an enabled suspect only for the displayed failed environment", () => {
  const selected = {...target, node: {kind: "system"}};
  const manager = {configured: selected, dataDirectories: [{id: "dsh-work", path: "C:/DSH"}]} as never;
  const host = {
    state: "Failed",
    generationId: "failed-run",
    launchSelection: {runtimeId: selected.runtimeId, nodeId: "system", profileName: selected.profile.name, dataDirectoryPath: "C:/DSH"},
    pluginFault: {plugins: [
      {package: "@test/disabled", enabled: false, suspected: true},
      {package: "@test/not-suspected", enabled: true},
      {package: "@test/fault", enabled: true, suspected: true},
    ]},
  };
  assert.equal(buildDashboardData(sources({manager, host: host as never})).host?.suspectPluginId, "@test/fault");
  const priorFailure = {...host, launchSelection: {...host.launchSelection, profileName: "other"}};
  assert.equal(buildDashboardData(sources({manager, host: priorFailure as never})).host?.suspectPluginId, undefined);
});

test("agent processes carry a readable role", () => {
  const data = buildDashboardData(sources({
    source: source({computer: {sampledAt: "", agents: {processes: [{pid: 1, name: "node.exe", role: "worker", cpuPercent: 1, memoryBytes: 2}]}, history: []}}),
  }));
  assert.equal(data.computer?.processes?.[0].role, "DSH");
});
