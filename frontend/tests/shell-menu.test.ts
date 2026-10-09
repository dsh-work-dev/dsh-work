import assert from "node:assert/strict";
import test from "node:test";

import {hasTranslationInEveryLocale} from "../src/i18n";
import {parseFrameMessage} from "../src/shell-frame";
import {buildMenus, formatKeys, stepItem, type Menu, type MenuItem, type MenuState} from "../src/shell-menu-model";

const t = (key: string) => key;
const state = (overrides: Partial<MenuState> = {}): MenuState => ({
  lifecycle: "Ready", busy: false, pet: {ready: true, visible: false}, safeMode: {available: true, active: false}, updatePhase: "idle", window: {fullscreen: false, zoom: 1}, framed: true, dsh: null, ...overrides,
});
const item = (menus: Menu[], key: string) => menus.flatMap(menu => menu.items).find(entry => entry.type === "item" && entry.key === key) as MenuItem;

const keysOf = (menu: Menu) => menu.items.map(entry => entry.type === "item" ? entry.key : "-");

test("menus follow the 文件／视图／运行／设置／帮助 layout", () => {
  const menus = buildMenus(state(), t);
  assert.deepEqual(menus.map(menu => menu.id), ["file", "view", "run", "settings", "help"]);
  assert.deepEqual(menus.map(keysOf), [
    ["session.new", "session.search", "workspace.add", "-", "terminal.new", "browser.new", "-", "closeWindow", "quit"],
    ["sidebar.left.toggle", "sidebar.right.toggle", "-", "zoomIn", "zoomOut", "zoomReset", "fullscreen", "-", "showPet"],
    ["refresh", "restart", "-", "safeMode"],
    ["settings.overview", "-", "settings.settings", "settings.notifications", "settings.pets", "-", "settings.runtimes", "settings.profiles", "settings.plugins", "-", "settings.data-directories", "-", "settings.open"],
    ["docs", "shortcuts.open", "-", "feedbackDesktop", "feedbackDsh", "-", "copyDiagnostics", "devtools", "-", "update", "about"],
  ]);
});

test("safe mode follows the environment and the restart rules", () => {
  const enter = item(buildMenus(state(), t), "safeMode");
  assert.equal(enter.label, "shell.menu.enterSafeMode");
  assert.equal(enter.enabled, true);
  assert.equal(item(buildMenus(state({safeMode: {available: true, active: true}}), t), "safeMode").label, "shell.menu.exitSafeMode");
  assert.equal(item(buildMenus(state({safeMode: null}), t), "safeMode").enabled, false);
  assert.equal(item(buildMenus(state({safeMode: {available: false, active: false}}), t), "safeMode").enabled, false);
  assert.equal(item(buildMenus(state({lifecycle: "Stopping"}), t), "safeMode").enabled, false);
});

test("full screen shows its state and zoom stops at actual size", () => {
  const actual = buildMenus(state(), t);
  assert.equal(item(actual, "fullscreen").checked, false);
  assert.equal(item(actual, "zoomIn").enabled, true);
  assert.equal(item(actual, "zoomOut").enabled, false);
  assert.equal(item(actual, "zoomReset").enabled, false);
  const zoomed = buildMenus(state({window: {fullscreen: true, zoom: 1.1}}), t);
  assert.equal(item(zoomed, "fullscreen").checked, true);
  assert.equal(item(zoomed, "zoomOut").enabled, true);
  assert.equal(item(zoomed, "zoomReset").enabled, true);
});

test("refresh needs a loaded DSH document", () => {
  assert.equal(item(buildMenus(state(), t), "refresh").enabled, true);
  assert.equal(item(buildMenus(state({framed: false}), t), "refresh").enabled, false);
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
  for (const key of ["restart", "quit", "showPet", "safeMode"]) assert.equal(item(busy, key).enabled, false, key);
  assert.equal(item(busy, "closeWindow").enabled, true);
  assert.equal(item(busy, "settings.settings").enabled, true);
});

test("pet item mirrors selection and visibility", () => {
  assert.equal(item(buildMenus(state({pet: {ready: false, visible: false}}), t), "showPet").enabled, false);
  const visible = item(buildMenus(state({pet: {ready: true, visible: true}}), t), "showPet");
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
  const items = buildMenus(state(), t)[2].items;
  assert.equal(items[2].type, "separator");
  assert.equal(stepItem(items, 1, 1), 3);
  assert.equal(stepItem(items, 3, -1), 1);
  assert.equal(stepItem(items, 3, 1), 0);
  assert.equal(stepItem(items, -1, 1), 0);
});

test("every shell menu label is translated in every locale", () => {
  const keys = [
    "shell.menu.bar", "shell.menu.unbound", "shell.menu.updateAvailable", "shell.menu.updating",
    "shell.menu.enterSafeMode", "shell.menu.exitSafeMode",
    ...buildMenus(state(), t).flatMap(menu => [menu.label, ...menu.items.flatMap(entry => entry.type === "item" ? [entry.label] : [])])
      .filter(key => !key.startsWith("manager.")),
    "shell.window.minimise", "shell.window.maximise", "shell.window.restore", "shell.window.close",
  ];
  for (const key of keys) assert.equal(hasTranslationInEveryLocale(key), true, key);
});

test("frame catalog keeps only menu commands with well-formed entries", () => {
  const parsed = parseFrameMessage({version: 1, type: "dsh-work/catalog", commands: [
    {id: "session.new", keys: ["Ctrl", "N"], bound: true},
    {id: "plugin.private", keys: [], bound: true},
    {id: "terminal.new", keys: "Ctrl+T", bound: true},
  ]});
  assert.equal(parsed?.type, "catalog");
  assert.deepEqual(Array.from((parsed as {commands: Map<string, unknown>}).commands.keys()), ["session.new"]);
});

test("frame messages are rejected unless they match a known shape", () => {
  assert.equal(parseFrameMessage(null), null);
  assert.equal(parseFrameMessage({type: "dsh-work/command", id: "session.new"}), null);
  assert.equal(parseFrameMessage({type: "dsh-work/menu-key", key: "Tab"}), null);
  assert.deepEqual(parseFrameMessage({version: 1, type: "dsh-work/menu-key", key: "F10"}), {type: "menu-key"});
  assert.deepEqual(parseFrameMessage({type: "dsh-work/surface", background: "rgb(1, 2, 3)", color: "red"}), {type: "surface", background: "rgb(1, 2, 3)", color: "red"});
});

test("shortcut keys render as DSH shows them, without extra separators", () => {
  assert.equal(formatKeys(["Ctrl", "+", "Alt", "+", "B"]), "Ctrl+Alt+B");
  assert.equal(formatKeys(["Ctrl", "+", "/"]), "Ctrl+/");
  assert.equal(formatKeys(["⌃", "⌥", "N"]), "⌃⌥N");
  assert.equal(formatKeys(undefined), "");
});
