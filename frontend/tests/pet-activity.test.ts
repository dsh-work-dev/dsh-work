import assert from "node:assert/strict";
import {after, test} from "node:test";

import {applyLocale} from "../src/i18n";
import {activityText, type Activity} from "../src/pet_activity";

const previousDocument = Object.getOwnPropertyDescriptor(globalThis, "document");
Object.defineProperty(globalThis, "document", {
  configurable: true,
  value: {
    documentElement: {lang: ""},
    querySelectorAll: () => []
  } as unknown as Document
});

after(() => {
  if (previousDocument) {
    Object.defineProperty(globalThis, "document", previousDocument);
  } else {
    Reflect.deleteProperty(globalThis, "document");
  }
});

const activity = (overrides: Partial<Activity>): Activity => ({
  sessionId: "session-1",
  title: "Conversation",
  state: "idle",
  outcome: "",
  summary: "",
  queued: 0,
  steering: 0,
  unread: false,
  ...overrides
});

test("pet activity shows unread completion with its summary", () => {
  applyLocale("zh-CN");
  assert.equal(activityText(activity({
    state: "ready",
    outcome: "completed",
    unread: true,
    summary: "已完成的摘要"
  })), "已完成 · 未读 · 已完成的摘要");
});

test("pet activity shows unread completion without a summary", () => {
  applyLocale("zh-CN");
  assert.equal(activityText(activity({
    state: "ready",
    outcome: "completed",
    unread: true
  })), "已完成 · 未读");
});

test("pet activity keeps the ordinary completed copy after it is read", () => {
  applyLocale("zh-CN");
  assert.equal(activityText(activity({
    state: "idle",
    outcome: "completed",
    summary: "已完成的摘要"
  })), "已完成 · 已完成的摘要");
});

test("pet activity keeps interaction copy ahead of unread completion", () => {
  applyLocale("zh-CN");
  assert.equal(activityText(activity({
    state: "ready",
    outcome: "completed",
    unread: true,
    interaction: {kind: "question", text: "请确认"}
  })), "等你回答：请确认");
});

test("pet activity keeps running copy ahead of completion outcome", () => {
  applyLocale("zh-CN");
  assert.equal(activityText(activity({
    state: "running",
    outcome: "completed",
    workPhase: "working",
    toolActivity: "testing"
  })), "正在检查修改");
});
