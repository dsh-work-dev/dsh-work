import assert from "node:assert/strict";
import test from "node:test";

import {hasTranslationInEveryLocale} from "../src/i18n";

import {issueNewURL, issueURL, problemActions, problemGroup, type ProblemGroup} from "../src/diagnostics-model";

test("problemGroup maps each exact code and representative prefix", () => {
  const cases: Array<[string, ProblemGroup]> = [
    ["DSH_RUNTIME_NOT_FOUND", "runtime"],
    ["DSH_VERSION_CHECK_FAILED", "runtime"],
    ["DSH_UNSUPPORTED_VERSION", "runtime"],
    ["RUNTIME_PROFILE_INCOMPATIBLE", "runtime"],
    ["RUNTIME_INSTALL_FAILED", "runtime"],
    ["DSH_START_FAILED", "start"],
    ["DSH_EARLY_EXIT", "start"],
    ["DSH_READINESS_TIMEOUT", "start"],
    ["DSH_INVALID_READINESS", "start"],
    ["PROCESS_START_FAILED", "start"],
    ["PLUGIN_LOAD_FAILED", "plugin"],
    ["PROFILE_INVALID", "profile"],
    ["WORKSPACE_INVALID", "profile"],
    ["RECOVERY_POINT_SAVE_FAILED", "storage"],
    ["MANAGER_STATE_INVALID", "storage"],
    ["SETTINGS_SAVE_FAILED", "storage"],
    ["UPDATE_DOWNLOAD_FAILED", "update"],
    ["UPDATE_INSTALL_FAILED", "update"],
    ["HOST_PANIC", "other"],
    ["", "other"]
  ];

  for (const [code, expected] of cases) assert.equal(problemGroup(code), expected, code);
});

test("problemActions maps every group to its complete action list", () => {
  const cases: Array<[ProblemGroup, string[]]> = [
    ["runtime", ["runtimes"]],
    ["start", ["restart", "plugins"]],
    ["plugin", ["plugins"]],
    ["profile", ["profiles"]],
    ["storage", ["data-directories"]],
    ["update", ["check-updates"]],
    ["other", []]
  ];

  for (const [group, expected] of cases) assert.deepEqual(problemActions(group), expected, group);
});

test("issueURL without a problem uses the bug report template", () => {
  assert.equal(issueURL(), issueNewURL + "?template=bug_report.yml");
});

test("issueURL encodes the problem title", () => {
  assert.ok(issueURL({code: "DSH_EARLY_EXIT", message: "DSH exited"}).endsWith("&title=DSH_EARLY_EXIT%3A%20DSH%20exited"));
});

test("issueURL limits titles to 120 Unicode code points", () => {
  const url = issueURL({code: "DSH_EARLY_EXIT", message: "错".repeat(200)});
  assert.equal(Array.from(new URL(url).searchParams.get("title") ?? "").length, 120);
});

test("diagnostics copy is translated in every locale", () => {
  const groups = ["runtime", "start", "plugin", "profile", "storage", "update", "other"];
  const keys = [
    "manager.diagnostics",
    "diagnostics.recent",
    "diagnostics.tools",
    "diagnostics.empty",
    "diagnostics.loadFailed",
    "diagnostics.count",
    ...groups.map(group => "diagnostics.group." + group),
    "diagnostics.action.runtimes",
    "diagnostics.action.restart",
    "diagnostics.action.plugins",
    "diagnostics.action.profiles",
    "diagnostics.action.dataDirectories",
    "diagnostics.action.checkUpdates",
    "diagnostics.copyReport",
    "diagnostics.reportOnGitHub",
    "diagnostics.reportCopied",
    "diagnostics.actionFailed",
    "diagnostics.exportBundle",
    "diagnostics.bundleSaved",
    "diagnostics.bundleOmitted",
    "diagnostics.exportFailed",
    "diagnostics.openLogFolder",
    "diagnostics.openFolderFailed",
    "diagnostics.verbose",
    "diagnostics.verboseDetail",
    "diagnostics.verboseFailed",
    "shell.menu.logs",
    "startup.reportProblem",
  ];

  for (const key of keys) assert.equal(hasTranslationInEveryLocale(key), true, key);
});
