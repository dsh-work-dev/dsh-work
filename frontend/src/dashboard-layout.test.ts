import assert from "node:assert/strict";
import test from "node:test";
import {defaultDashboardLayout, normalizeDashboardLayout} from "./dashboard-layout";

test("default layout holds all eleven tiles in three groups", () => {
  const layout = defaultDashboardLayout();
  assert.deepEqual(layout.groups, ["conversations", "usage", "environment"]);
  assert.equal(Object.values(layout.tiles).flat().length, 11);
});

test("saved order stays within each group and appends tiles the save does not mention", () => {
  const layout = normalizeDashboardLayout({
    groups: ["environment", "environment", "unknown"],
    tiles: {conversations: ["timeline", "output", "output", "runtime"], environment: ["health"]},
  });
  assert.deepEqual(layout.groups, ["environment", "conversations", "usage"]);
  assert.deepEqual(layout.tiles.conversations, ["timeline", "output", "orbit", "results"]);
  assert.deepEqual(layout.tiles.environment, ["health", "host", "runtime", "computer"]);
});

test("invalid saved values restore defaults without dropping tiles", () => {
  for (const input of [null, 1, [], {groups: "broken", tiles: {conversations: null}}]) {
    assert.deepEqual(normalizeDashboardLayout(input), defaultDashboardLayout());
  }
});
