import assert from "node:assert/strict";
import {readFileSync} from "node:fs";
import test from "node:test";

import {runningLaunchSelection, startupProfileName, viewModel, type LifecycleStatus} from "../src/lifecycle";
import {buildOverviewModel, sameRunContext} from "../src/overview";
import {runtimePreparationArtifactKind, runtimePreparationProgressPercent} from "../src/manager";
import {hasTranslationInEveryLocale, isStaticCopy} from "../src/i18n";
import {DataDirectoryOwnership, NodeSelectionKind, RuntimeSource, ThemePreference, type Snapshot} from "../bindings/github.com/local/dsh-work/internal/dshmanager";

const status = (overrides: Partial<LifecycleStatus>): LifecycleStatus => ({
  state: "Starting",
  phase: "configuration",
  canRetry: false,
  canCancel: true,
  ...overrides
});

test("starting status exposes bounded progress state", () => {
  const model = viewModel(status({phase: "readiness"}));
  assert.equal(model.label, "Checking readiness");
  assert.equal(model.tone, "progress");
  assert.equal(model.showCancel, true);
});

test("runtime preparation exposes a typed download state", () => {
  const model = viewModel(status({
    phase: "runtime",
    runtimePreparation: {
      state: "acquiring-node",
      operation: "download-node",
      targetVersion: "0.1.2-alpha.3",
      toolchain: "managed-node-npm",
      source: "official",
      receivedBytes: 50,
      totalBytes: 100,
      hasTotal: true,
      canCancel: true
    }
  }));
  assert.equal(model.label, "Checking DSH");
  assert.equal(model.detail, "Downloading the Node.js runtime.");
  assert.equal(model.showCancel, true);
});

test("ready status exposes the trusted workspace URL", () => {
  const model = viewModel(status({
    state: "Ready",
    phase: "workspace",
    canCancel: true,
    workspaceUrl: "http://127.0.0.1:4321/"
  }));
  assert.equal(model.tone, "ready");
  assert.equal(model.workspace, "http://127.0.0.1:4321/");
  assert.equal(model.showRetry, false);
});

test("failed status exposes safe copy without projecting the internal error code", () => {
  const model = viewModel(status({
    state: "Failed",
    phase: "failed",
    canCancel: false,
    canRetry: true,
    error: {
      code: "DSH_READINESS_TIMEOUT",
      summary: "DSH did not become ready.",
      retryable: true,
      effectOccurred: true,
      correlationId: "generation"
    }
  }));
  assert.equal(model.detail, "dsh-work could not start the local workspace.");
  assert.equal(model.message, "DSH did not become ready.");
  assert.equal(model.showRetry, true);
});

const managerSnapshot = (overrides: Partial<Snapshot>): Snapshot => ({
  runtimes: [{
    id: "dsh-current",
    version: "0.1.2",
    path: "dsh.cmd",
    source: RuntimeSource.RuntimeSourceDevelopmentFixture,
    installed: true,
    removable: false
  }],
  dshReleases: [],
  nodes: [],
  dataDirectories: [{
    id: "dsh-work",
    name: "dsh-work DSH data directory",
    path: "dsh-data",
    ownership: DataDirectoryOwnership.DataDirectoryOwnershipDSHWork
  }],
  profiles: [],
  theme: ThemePreference.ThemePreferenceSystem,
  ...overrides
});

test("overview keeps current, configured and known-good contexts separate", () => {
  const model = buildOverviewModel(managerSnapshot({
    current: {
      runtimeId: "dsh-current",
      node: {kind: NodeSelectionKind.NodeSelectionSystem},
      profile: {dataDirectoryId: "dsh-work", name: "web"}
    },
    configured: {
      runtimeId: "dsh-current",
      node: {kind: NodeSelectionKind.NodeSelectionSystem},
      profile: {dataDirectoryId: "dsh-work", name: "coding"}
    },
    knownGood: {
      runtimeId: "dsh-current",
      node: {kind: NodeSelectionKind.NodeSelectionSystem},
      profile: {dataDirectoryId: "dsh-work", name: "web"}
    }
  }));

  assert.equal(model.current.target?.profile.name, "web");
  assert.equal(model.configured?.target?.profile.name, "coding");
  assert.equal(model.knownGood?.target?.profile.name, "web");
  assert.equal(model.state, "active");
});

test("overview shows the same context in each applicable state lane", () => {
  const target = {
    runtimeId: "dsh-current",
    node: {kind: NodeSelectionKind.NodeSelectionSystem},
    profile: {dataDirectoryId: "dsh-work", name: "web"}
  };
  const model = buildOverviewModel(managerSnapshot({current: target, configured: target, knownGood: target}));

  assert.equal(model.configured?.target?.profile.name, "web");
  assert.equal(model.state, "active");
});

test("overview shows the configured context when DSH is stopped", () => {
  const model = buildOverviewModel(managerSnapshot({
    configured: {
      runtimeId: "dsh-current",
      node: {kind: NodeSelectionKind.NodeSelectionSystem},
      profile: {dataDirectoryId: "dsh-work", name: "coding"}
    }
  }));

  assert.equal(model.current.target, null);
  assert.equal(model.configured?.runtime?.id, "dsh-current");
  assert.equal(model.configured?.dataDirectory?.name, "dsh-work DSH data directory");
  assert.equal(model.configured?.target?.profile.name, "coding");
  assert.equal(model.state, "not-running");
});

test("runtime acquisition progress keeps Node and DSH cards independent", () => {
  assert.equal(runtimePreparationArtifactKind({state: "acquiring-node", operation: "download-node", artifactKind: "node"}), "node");
  assert.equal(runtimePreparationArtifactKind({state: "acquiring-dsh", operation: "install-dsh", artifactKind: "dsh"}), "dsh");
  assert.equal(runtimePreparationProgressPercent({state: "acquiring-node", hasTotal: true, receivedBytes: 25, totalBytes: 100}), 25);
  assert.equal(runtimePreparationProgressPercent({state: "installed", hasTotal: false}), 100);
  assert.equal(runtimePreparationProgressPercent({state: "acquiring-dsh", hasTotal: false}), undefined);
});

test("Run context identity includes the independent Node selection", () => {
	const base = {runtimeId: "dsh-current", node: {kind: NodeSelectionKind.NodeSelectionSystem}, profile: {dataDirectoryId: "dsh-work", name: "web"}};
	const managed = {...base, node: {kind: NodeSelectionKind.NodeSelectionManaged, installationId: "node-v26"}};
	assert.equal(sameRunContext(base, managed), false);
	assert.equal(sameRunContext(managed, {...managed}), true);
});

test("snapshot retains exact DSH and Node catalog rows without activating them", () => {
	const snapshot = managerSnapshot({
		dshReleases: [{version: "9.8.7", source: "official" as never, fallbackUsed: false, observedAt: "2026-09-06T00:00:00Z"}],
		nodes: [{id: "node-v26", version: "v26.1.0", platform: "windows", architecture: "x64", nodePath: "node.exe", npmPath: "npm.cmd", ownership: "managed" as never, installSource: "official" as never, installed: true, removable: true, verified: true}]
	});
	assert.equal(snapshot.current, undefined);
	assert.equal(snapshot.dshReleases?.[0].version, "9.8.7");
	assert.equal(snapshot.nodes?.[0].version, "v26.1.0");
});

test("i18n copy is applied only when element text is still static", () => {
  const key = "value.noRuntime";
  const original = "No runtime";
  assert.equal(isStaticCopy(original, key, original), true);
  assert.equal(isStaticCopy("No runtime selected", key, original), true);
  assert.equal(isStaticCopy("未选择 DSH 运行时", key, original), true);
  assert.equal(isStaticCopy("DSH ランタイム未選択", key, original), true);
  assert.equal(isStaticCopy("0.1.2-alpha.3 · 已验证", key, original), false);
  assert.equal(isStaticCopy("", key, original), false);
});

test("i18n copy re-applies over a stale translation of the same key", () => {
  const key = "value.notRunning";
  const original = "Not running";
  assert.equal(isStaticCopy("未运行", key, original), true);
  assert.equal(isStaticCopy("未起動", key, original), true);
  assert.equal(isStaticCopy("运行中", key, original), false);
});

test("every visible HTML translation key exists in all locales", () => {
	const html = readFileSync(new URL("../index.html", import.meta.url), "utf8");
	const keys = Array.from(html.matchAll(/data-i18n(?:-aria-label|-placeholder)?="([^"]+)"/g), (match) => match[1]);
	assert.ok(keys.length > 0);
	for (const key of keys) assert.equal(hasTranslationInEveryLocale(key), true, key);
});

test("acquisition terminal result copy exists in all locales", () => {
	for (const key of ["runtimes.resultSucceeded", "runtimes.resultCancelled", "runtimes.resultFailedRetryable", "runtimes.resultFailed"]) {
		assert.equal(hasTranslationInEveryLocale(key), true, key);
	}
});

test("startup profile follows candidate and rollback rather than last saved selection", () => {
  assert.equal(startupProfileName(status({launchSelection: {runtimeId: "dsh", nodeId: "system", profileName: "web 1"}}), "web"), "web 1");
  assert.equal(startupProfileName(status({launchSelection: {runtimeId: "dsh", nodeId: "system", profileName: "web"}}), "web 1"), "web");
  assert.equal(startupProfileName(status({}), "web 1"), "web 1");
});

test("a failed attempt does not overwrite the next manual runtime selection", () => {
  const launchSelection = {runtimeId: "candidate", nodeId: "node-next", runtimeVersion: "next", nodeVersion: "24", profileName: "web 1"};
  assert.equal(runningLaunchSelection(status({state: "Starting", launchSelection})), launchSelection);
  assert.equal(runningLaunchSelection(status({state: "Failed", launchSelection})), undefined);
  assert.equal(runningLaunchSelection(status({state: "Stopped", launchSelection})), undefined);
});
