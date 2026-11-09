import assert from "node:assert/strict";
import test from "node:test";
import {buildDashboardData, capability, contextKey} from "./overview-data";
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
  return {dshPayload: JSON.stringify({sessions: {items: []}}), profileKey: contextKey(target as never), events: [], automaticRecoveries: 0, recoveryTrackingSince: "", ...extra} as never;
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
    source: source({profileKey: contextKey(otherTarget as never), computer: {sampledAt: "", cpuPercent: 12, agents: {processes: []}, history: []}}),
  }));
  assert.equal(data.sessions, undefined);
  assert.equal(data.computer?.cpuPercent, 12);
});

test("only failed writes are reported as not saved", () => {
  const readOnly = buildDashboardData(sources({source: source({persistenceErrors: {recoverySource: "manager busy", cacheLoad: "too large"}})}));
  const saved = buildDashboardData(sources({source: source({persistenceErrors: {cacheSave: "disk full"}})}));
  assert.equal(readOnly.sourceError, undefined);
  assert.ok(saved.sourceError);
});

test("plugin updates are counted only after every plugin was checked", () => {
  const checked = buildDashboardData(sources({plugins: [{updateCheck: "available"}, {updateCheck: "current", disabled: true}] as never}));
  const pending = buildDashboardData(sources({plugins: [{updateCheck: "available"}, {updateCheck: "checking"}] as never}));
  assert.equal(checked.runtime?.pluginUpdates, 1);
  assert.equal(checked.runtime?.disabledPlugins, 1);
  assert.equal(pending.runtime?.pluginUpdates, undefined);
});

test("agent processes carry a readable role", () => {
  const data = buildDashboardData(sources({
    source: source({computer: {sampledAt: "", agents: {processes: [{pid: 1, name: "node.exe", role: "worker", cpuPercent: 1, memoryBytes: 2}]}, history: []}}),
  }));
  assert.equal(data.computer?.processes?.[0].role, "DSH");
});
