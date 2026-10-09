import assert from "node:assert/strict";
import test from "node:test";
import {buildBuiltinComponentViews, builtinComponentIds} from "../src/builtin-components";

test("built-in component status is a fixed read-only list sourced from plugin handshakes", () => {
  assert.deepEqual(builtinComponentIds, ["shell", "account", "activity"]);
  const rows = buildBuiltinComponentViews([
    {id: "activity", state: "loaded"},
    {id: "account", state: "loading"},
    {id: "unknown", state: "loaded"},
    {id: "shell", state: "disabled"},
  ]);
  assert.deepEqual(rows.map(({id, state, stateKey}) => [id, state, stateKey]), [
    ["shell", "inactive", "components.status.inactive"],
    ["account", "loading", "components.status.loading"],
    ["activity", "loaded", "components.status.loaded"],
  ]);
  assert.ok(rows.every(row => !("action" in row) && !("enabled" in row)));
});
