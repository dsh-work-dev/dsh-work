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

test("overview keeps current and next launch targets separate", () => {
  const model = buildOverviewModel(managerSnapshot({
    active: {
      runtimeId: "dsh-current",
      profile: {dataDirectoryId: "work", name: "web"}
    },
    desired: {
      runtimeId: "dsh-current",
      profile: {dataDirectoryId: "work", name: "coding"}
    }
  }));

  assert.equal(model.current.target?.profile.name, "web");
  assert.equal(model.next?.target?.profile.name, "coding");
  assert.equal(model.state, "restart-required");
});

test("overview does not duplicate an unchanged active target", () => {
  const target = {
    runtimeId: "dsh-current",
    profile: {dataDirectoryId: "work", name: "web"}
  };
  const model = buildOverviewModel(managerSnapshot({active: target, desired: target}));

  assert.equal(model.next, null);
  assert.equal(model.state, "active");
});

test("overview keeps the desired target in next launch when DSH is stopped", () => {
  const model = buildOverviewModel(managerSnapshot({
    desired: {
      runtimeId: "dsh-current",
      profile: {dataDirectoryId: "work", name: "coding"}
    }
  }));

  assert.equal(model.current.target, null);
  assert.equal(model.next?.runtime?.id, "dsh-current");
  assert.equal(model.next?.dataDirectory?.name, "Work DSH data directory");
  assert.equal(model.next?.target?.profile.name, "coding");
  assert.equal(model.state, "not-running");
});
