import assert from "node:assert/strict";
import test from "node:test";

import {viewModel, type LifecycleStatus} from "../src/lifecycle";
import {buildOverviewModel} from "../src/overview";
import {DataDirectoryOwnership, RuntimeSource, ThemePreference, type Snapshot} from "../bindings/github.com/local/work/internal/dshmanager";

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

test("failed status exposes only the stable error code and summary", () => {
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
  assert.equal(model.detail, "DSH_READINESS_TIMEOUT");
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
  dataDirectories: [{
    id: "work",
    name: "Work DSH data directory",
    path: "dsh-data",
    ownership: DataDirectoryOwnership.DataDirectoryOwnershipWork
  }],
  profiles: [],
  theme: ThemePreference.ThemePreferenceSystem,
  ...overrides
});

test("overview keeps current, configured and known-good contexts separate", () => {
  const model = buildOverviewModel(managerSnapshot({
    current: {
      runtimeId: "dsh-current",
      profile: {dataDirectoryId: "work", name: "web"}
    },
    configured: {
      runtimeId: "dsh-current",
      profile: {dataDirectoryId: "work", name: "coding"}
    },
    knownGood: {
      runtimeId: "dsh-current",
      profile: {dataDirectoryId: "work", name: "web"}
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
    profile: {dataDirectoryId: "work", name: "web"}
  };
  const model = buildOverviewModel(managerSnapshot({current: target, configured: target, knownGood: target}));

  assert.equal(model.configured?.target?.profile.name, "web");
  assert.equal(model.state, "active");
});

test("overview shows the configured context when DSH is stopped", () => {
  const model = buildOverviewModel(managerSnapshot({
    configured: {
      runtimeId: "dsh-current",
      profile: {dataDirectoryId: "work", name: "coding"}
    }
  }));

  assert.equal(model.current.target, null);
  assert.equal(model.configured?.runtime?.id, "dsh-current");
  assert.equal(model.configured?.dataDirectory?.name, "Work DSH data directory");
  assert.equal(model.configured?.target?.profile.name, "coding");
  assert.equal(model.state, "not-running");
});
