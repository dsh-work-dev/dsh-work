import assert from "node:assert/strict";
import test from "node:test";

import {viewModel, type LifecycleStatus} from "../src/lifecycle";
import {buildOverviewModel} from "../src/overview";
import {HomeOwnership, RuntimeSource, ThemePreference, type Snapshot} from "../bindings/github.com/local/work/internal/dshmanager";

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
  homes: [{
    id: "work",
    name: "Work DSH home",
    path: "dsh-home",
    ownership: HomeOwnership.HomeOwnershipWork
  }],
  profiles: [],
  theme: ThemePreference.ThemePreferenceSystem,
  ...overrides
});

test("overview keeps current and next launch selections separate", () => {
  const model = buildOverviewModel(managerSnapshot({
    active: {
      runtimeId: "dsh-current",
      profile: {homeId: "work", name: "web"},
      workspace: "project-a"
    },
    desired: {
      runtimeId: "dsh-current",
      profile: {homeId: "work", name: "coding"},
      workspace: "project-b"
    }
  }));

  assert.equal(model.current.selection?.profile.name, "web");
  assert.equal(model.next?.selection?.profile.name, "coding");
  assert.equal(model.state, "restart-required");
});

test("overview does not duplicate an unchanged active selection", () => {
  const selection = {
    runtimeId: "dsh-current",
    profile: {homeId: "work", name: "web"},
    workspace: "project-a"
  };
  const model = buildOverviewModel(managerSnapshot({active: selection, desired: selection}));

  assert.equal(model.next, null);
  assert.equal(model.state, "active");
});

test("overview keeps the desired selection in next launch when DSH is stopped", () => {
  const model = buildOverviewModel(managerSnapshot({
    desired: {
      runtimeId: "dsh-current",
      profile: {homeId: "work", name: "coding"},
      workspace: "project-b"
    }
  }));

  assert.equal(model.current.selection, null);
  assert.equal(model.next?.runtime?.id, "dsh-current");
  assert.equal(model.next?.home?.name, "Work DSH home");
  assert.equal(model.next?.selection?.profile.name, "coding");
  assert.equal(model.state, "not-running");
});
