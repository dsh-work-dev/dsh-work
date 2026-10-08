import assert from "node:assert/strict";
import test from "node:test";

import {buildMenus, stepItem, type Menu, type MenuItem, type MenuState} from "../src/shell-menu-model";

const t = (key: string) => key;
const state = (overrides: Partial<MenuState> = {}): MenuState => ({
  lifecycle: "Ready", busy: false, pet: {ready: true, visible: false}, updatePhase: "idle", dsh: null, ...overrides,
});
const item = (menus: Menu[], key: string) => menus.flatMap(menu => menu.items).find(entry => entry.type === "item" && entry.key === key) as MenuItem;

test("menus follow the 应用／会话／视图／帮助 order", () => {
  assert.deepEqual(buildMenus(state(), t).map(menu => menu.id), ["app", "session", "view", "help"]);
});

test("host items follow the native menu availability rules", () => {
  const ready = buildMenus(state(), t);
  assert.equal(item(ready, "restart").enabled, true);
  assert.equal(item(ready, "quit").enabled, true);
  const starting = buildMenus(state({lifecycle: "Starting"}), t);
  assert.equal(item(starting, "restart").enabled, false);
  assert.equal(item(starting, "quit").enabled, true);
  const stopping = buildMenus(state({lifecycle: "Stopping"}), t);
  assert.equal(item(stopping, "quit").enabled, false);
  const busy = buildMenus(state({busy: true}), t);
  for (const key of ["restart", "quit", "pet"]) assert.equal(item(busy, key).enabled, false, key);
  assert.equal(item(busy, "settings").enabled, true);
});

test("pet item mirrors selection and visibility", () => {
  assert.equal(item(buildMenus(state({pet: {ready: false, visible: false}}), t), "pet").enabled, false);
  const visible = item(buildMenus(state({pet: {ready: true, visible: true}}), t), "pet");
  assert.equal(visible.enabled, true);
  assert.equal(visible.checked, true);
});

test("DSH items wait for the catalog and show the current binding", () => {
  assert.equal(item(buildMenus(state(), t), "session.new").enabled, false);
  const dsh = new Map([
    ["session.new", {id: "session.new", keys: ["Ctrl", "Alt", "N"], bound: true}],
    ["terminal.new", {id: "terminal.new", keys: [], bound: false}],
  ]);
  const menus = buildMenus(state({dsh}), t);
  const created = item(menus, "session.new");
  assert.equal(created.enabled, true);
  assert.deepEqual(created.keys, ["Ctrl", "Alt", "N"]);
  assert.equal(created.label, "shell.command.session.new");
  const unbound = item(menus, "terminal.new");
  assert.equal(unbound.enabled, false);
  assert.equal(unbound.hint, "shell.menu.unbound");
  assert.equal(item(menus, "workspace.add").enabled, false);
});

test("help reflects the update phase", () => {
  const ready = buildMenus(state({updatePhase: "ready"}), t);
  assert.equal(ready.find(menu => menu.id === "help")?.badge, true);
  assert.equal(item(ready, "update").label, "shell.menu.updateAvailable");
  assert.equal(item(buildMenus(state({updatePhase: "downloading"}), t), "update").label, "shell.menu.updating");
  assert.equal(item(buildMenus(state({updatePhase: "error"}), t), "update").label, "shell.menu.checkUpdates");
});

test("keyboard steps skip separators and wrap", () => {
  const items = buildMenus(state(), t)[0].items;
  assert.equal(items[2].type, "separator");
  assert.equal(stepItem(items, 1, 1), 3);
  assert.equal(stepItem(items, 3, -1), 1);
  assert.equal(stepItem(items, 4, 1), 0);
  assert.equal(stepItem(items, -1, 1), 0);
});
