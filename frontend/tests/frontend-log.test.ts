import assert from "node:assert/strict";
import test from "node:test";

import {createFrontendLog, describeError, type LogEntry} from "../src/log";

test("info entries wait for the timer and flush together", () => {
  const writes: LogEntry[][] = [];
  const callbacks: Array<() => void> = [];
  const delays: number[] = [];
  const log = createFrontendLog({
    write: async entries => { writes.push(entries); },
    now: () => 0,
    schedule: (callback, delayMs) => {
      callbacks.push(callback);
      delays.push(delayMs);
      return callbacks.length;
    },
    cancel: () => {}
  });

  log.info("shell", "first");
  log.info("settings", "second");
  assert.equal(writes.length, 0);
  assert.equal(callbacks.length, 1);
  assert.equal(delays[0], 2_000);

  callbacks[0]();
  assert.deepEqual(writes, [[
    {level: "info", module: "shell", message: "first"},
    {level: "info", module: "settings", message: "second"}
  ]]);
});

test("error entries flush immediately", () => {
  const writes: LogEntry[][] = [];
  let cancelled = false;
  const log = createFrontendLog({
    write: async entries => { writes.push(entries); },
    now: () => 0,
    schedule: () => 1,
    cancel: () => { cancelled = true; }
  });

  log.info("frontend", "queued");
  log.error("frontend", "failure", "details", "UI_SCRIPT_ERROR");
  assert.equal(cancelled, true);
  assert.deepEqual(writes, [[
    {level: "info", module: "frontend", message: "queued"},
    {level: "error", module: "frontend", message: "failure", detail: "details", code: "UI_SCRIPT_ERROR"}
  ]]);
});

test("the rate limit reports dropped entries at the start of the next minute", () => {
  const writes: LogEntry[][] = [];
  const callbacks: Array<() => void> = [];
  let time = 100;
  const log = createFrontendLog({
    write: async entries => { writes.push(entries); },
    now: () => time,
    schedule: callback => {
      callbacks.push(callback);
      return callbacks.length;
    },
    cancel: () => {}
  });

  for (let i = 0; i < 51; i++) log.info("frontend", "event " + i);
  assert.equal(writes.length, 0);
  callbacks[0]();
  assert.equal(writes[0].length, 50);

  time = 60_101;
  log.info("frontend", "next minute");
  callbacks[1]();
  assert.deepEqual(writes[1], [
    {level: "warn", module: "frontend", message: "frontend log entries dropped", detail: "1"},
    {level: "info", module: "frontend", message: "next minute"}
  ]);
});

test("flush swallows write failures", async () => {
  const log = createFrontendLog({
    write: async () => { throw new Error("write failed"); },
    now: () => 0,
    schedule: () => 1,
    cancel: () => {}
  });

  log.info("frontend", "queued");
  await assert.doesNotReject(log.flush());
});

test("describeError includes Error details and stringifies other values", () => {
  const error = new Error("failure");
  error.name = "CustomError";
  error.stack = "stack trace";
  assert.equal(describeError(error), "CustomError: failure\nstack trace");
  assert.equal(describeError(42), "42");
  assert.equal(describeError("x".repeat(5_000)).length, 4_000);
});
