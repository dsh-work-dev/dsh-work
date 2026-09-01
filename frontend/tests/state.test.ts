import assert from "node:assert/strict";
import test from "node:test";

import {viewModel, type LifecycleStatus} from "../src/lifecycle";

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
