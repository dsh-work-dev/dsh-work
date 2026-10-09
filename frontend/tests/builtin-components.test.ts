import assert from "node:assert/strict";
import test from "node:test";
import {buildBuiltinComponentViews, builtinComponentIds} from "../src/builtin-components";

test("built-in component status is a fixed read-only list sourced from plugin handshakes", () => {
  assert.deepEqual(builtinComponentIds, ["shell", "account", "pet"]);
  const rows = buildBuiltinComponentViews([
    {id: "pet", state: "loaded"},
    {id: "account", state: "failed"},
    {id: "unknown", state: "loaded"},
    {id: "shell", state: "disabled"},
  ]);
  assert.deepEqual(rows.map(({id, state, stateKey}) => [id, state, stateKey]), [
    ["shell", "inactive", "components.status.inactive"],
    ["account", "failed", "components.status.failed"],
    ["pet", "loaded", "components.status.loaded"],
  ]);
  assert.ok(rows.every(row => !("action" in row) && !("enabled" in row)));
  assert.equal(buildBuiltinComponentViews([{id: "shell", state: "loading"}])[0].stateKey, "components.status.loading");
});
