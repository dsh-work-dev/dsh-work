import assert from "node:assert/strict";
import {mock, test} from "node:test";
import {readFileSync} from "node:fs";
import {FINISH_TIMEOUT_MS, SplashController} from "../src/splash/controller";
import {builtinScenes, type Outcome, type SceneProgress} from "../src/splash/scene";
import {currentStep, overallProgress, stepStates, type StepInput} from "../src/splash/steps";

const input = (overrides: Partial<StepInput>): StepInput => ({
  ready: false, starting: true, phase: "configuration", failed: false, errorCode: "",
  missing: false, nodeInstalled: true, active: true, busy: false, ...overrides,
});

test("phases map to the four public steps", () => {
  const cases: Array<[string, number]> = [["configuration", 0], ["node", 0], ["runtime", 1], ["profile", 2], ["worker", 3], ["readiness", 3], ["checkpoint", 3], ["workspace", 3]];
  for (const [phase, step] of cases) assert.equal(currentStep(input({phase})), step, phase);
});

test("acquisition wins over the phase", () => {
  assert.equal(currentStep(input({phase: "profile", acquisition: "node"})), 0);
  assert.equal(currentStep(input({phase: "profile", acquisition: "dsh"})), 1);
});

test("failure codes pick the failed step", () => {
  const failed = (errorCode: string) => currentStep(input({failed: true, starting: false, active: false, errorCode}));
  assert.equal(failed("NODE_MISSING"), 0);
  assert.equal(failed("RUNTIME_INVALID"), 1);
  assert.equal(failed("VERSION_MISMATCH"), 1);
  assert.equal(failed("PROFILE_BROKEN"), 2);
  assert.equal(failed("DSH_PROCESS_EXITED"), 3);
  assert.equal(failed("SOMETHING_ELSE"), -1);
});

test("missing components point at the component to install", () => {
  assert.equal(currentStep(input({missing: true, active: false, starting: false, nodeInstalled: false})), 0);
  assert.equal(currentStep(input({missing: true, active: false, starting: false, nodeInstalled: true})), 1);
});

test("step states", () => {
  assert.deepEqual(stepStates(input({phase: "profile"}), 2), ["done", "done", "active", "pending"]);
  assert.deepEqual(stepStates(input({failed: true, starting: false}), 3), ["done", "done", "done", "failed"]);
  assert.deepEqual(stepStates(input({ready: true, starting: false}), 3), ["done", "done", "done", "done"]);
});

test("overall progress", () => {
  assert.equal(overallProgress(0, null, false), 0);
  assert.equal(overallProgress(2, 0.5, false), 0.625);
  assert.equal(overallProgress(-1, null, false), 0);
  assert.equal(overallProgress(1, 7, false), 0.5);
  assert.equal(overallProgress(1, Number.NaN, false), 0.25);
  assert.equal(overallProgress(0, null, true), 1);
});

function fakeScene() {
  const calls: string[] = [];
  const updates: SceneProgress[] = [];
  let settle: (() => void) | undefined;
  return {
    calls, updates, settle: () => settle?.(),
    progress(update: SceneProgress) { updates.push(update); },
    finish(outcome: Outcome) { calls.push(`finish:${outcome}`); return new Promise<void>(resolve => { settle = resolve; }); },
    reset() { calls.push("reset"); },
  };
}

test("progress only moves forward and skips tiny changes", () => {
  const scene = fakeScene();
  const controller = new SplashController(() => {});
  controller.attach(scene);
  controller.progress("dsh", 0.5);
  controller.progress("node", null);
  controller.progress("dsh", 0.52);
  controller.progress("dsh", 0.75);
  assert.deepEqual(scene.updates.map(u => [u.step, u.progress]), [["node", 0], ["dsh", 0.375], ["dsh", 0.4375]]);
});

test("finish resolves when the scene finishes", async () => {
  const scene = fakeScene();
  const dims: boolean[] = [];
  const controller = new SplashController(dim => dims.push(dim));
  controller.attach(scene);
  const done = controller.finish("failed");
  scene.settle();
  await done;
  assert.deepEqual(dims, [true]);
});

test("finish gives up after the timeout", async () => {
  mock.timers.enable({apis: ["setTimeout"]});
  try {
    const controller = new SplashController(() => {});
    controller.attach(fakeScene());
    let finished = false;
    const done = controller.finish("ready").then(() => { finished = true; });
    mock.timers.tick(FINISH_TIMEOUT_MS - 1);
    await Promise.resolve();
    assert.equal(finished, false);
    mock.timers.tick(1);
    await done;
    assert.equal(finished, true);
  } finally { mock.timers.reset(); }
});

test("repeated finish and reset are idempotent", async () => {
  const scene = fakeScene();
  const dims: boolean[] = [];
  const controller = new SplashController(dim => dims.push(dim));
  controller.attach(scene);
  const a = controller.finish("failed"), b = controller.finish("failed");
  assert.equal(a, b);
  scene.settle(); await a;
  controller.reset(); controller.reset();
  assert.deepEqual(scene.calls, ["finish:failed", "reset"]);
  assert.deepEqual(dims, [true, false]);
});

test("reset replays the current progress", () => {
  const scene = fakeScene();
  const controller = new SplashController(() => {});
  controller.attach(scene);
  controller.progress("profile", null);
  void controller.finish("failed");
  controller.progress("service", null);
  controller.reset();
  scene.settle();
  assert.deepEqual(scene.calls, ["finish:failed", "reset"]);
  assert.deepEqual(scene.updates.map(u => u.step), ["node", "profile", "profile"]);
});

test("without a scene finish resolves at once", async () => {
  const controller = new SplashController(() => {});
  await controller.finish("ready");
});

test("a throwing scene still finishes", async () => {
  const controller = new SplashController(() => {});
  controller.attach({progress() {}, reset() {}, finish() { throw new Error("boom"); }});
  await controller.finish("ready");
});

test("built-in scenes match the Go list", () => {
  const go = readFileSync(new URL("../../internal/splash/builtin.go", import.meta.url), "utf8");
  const line = go.split("\n").find(l => l.startsWith("var builtinIDs"))!;
  const ids = Array.from(line.matchAll(/"(builtin-[a-z0-9-]+)"/g), m => m[1]).sort();
  assert.deepEqual(ids, Object.keys(builtinScenes).sort());
});

test("a scene attached after finish is told to finish", async () => {
  const controller = new SplashController(() => {});
  await controller.finish("failed");
  const scene = fakeScene();
  controller.attach(scene);
  assert.deepEqual(scene.calls, ["finish:failed"]);
  assert.equal(scene.updates.length, 0);
});

test("dispose cancels a pending finish", async () => {
  mock.timers.enable({apis: ["setTimeout"]});
  try {
    const dims: boolean[] = [];
    const controller = new SplashController(dim => dims.push(dim));
    controller.attach(fakeScene());
    const done = controller.finish("failed");
    controller.dispose();
    mock.timers.tick(FINISH_TIMEOUT_MS);
    await done;
    assert.deepEqual(dims, []);
  } finally { mock.timers.reset(); }
});

import {startupAttention} from "../src/splash/steps";
const attention = (o: Partial<Parameters<typeof startupAttention>[0]>) => startupAttention({failed: false, stopped: false, error: false, connectionFailed: false, missing: false, active: false, busy: false, ...o});

test("recovery panel visibility", () => {
  assert.equal(attention({}), false);
  assert.equal(attention({failed: true}), true);
  assert.equal(attention({stopped: true}), true);
  assert.equal(attention({error: true}), true);
  assert.equal(attention({connectionFailed: true}), true);
  assert.equal(attention({missing: true}), true);
  assert.equal(attention({missing: true, active: true}), false);
  assert.equal(attention({missing: true, busy: true}), false);
  assert.equal(attention({failed: true, busy: true}), false);
});

import {createReveal} from "../src/splash/reveal";

function revealHarness() {
  const log: string[] = [];
  let settle: (() => void) | undefined;
  const reveal = createReveal({
    finish: () => { log.push("finish"); return new Promise<void>(resolve => { settle = resolve; }); },
    fadeOut: () => { log.push("fade"); return Promise.resolve(); },
    hide: () => log.push("hide"),
    show: () => log.push("show"),
    reset: () => log.push("reset"),
    dispose: () => log.push("dispose"),
    mount: () => log.push("mount"),
  });
  return {log, reveal, settle: () => settle?.()};
}
const flush = () => new Promise(resolve => setTimeout(resolve, 0));

test("reveal waits for the closing animation, then hides and disposes", async () => {
  const h = revealHarness();
  h.reveal.reveal();
  assert.deepEqual(h.log, ["finish"]);
  h.settle();
  await flush();
  assert.deepEqual(h.log, ["finish", "fade", "hide", "dispose"]);
});

test("a worker drop during the reveal restores the splash", async () => {
  const h = revealHarness();
  h.reveal.reveal();
  h.reveal.restore();
  h.settle();
  await flush();
  assert.deepEqual(h.log, ["finish", "show", "reset"]);
});

test("restore after a completed reveal mounts a fresh splash", async () => {
  const h = revealHarness();
  h.reveal.reveal(); h.settle();
  await flush();
  h.reveal.restore();
  assert.deepEqual(h.log.slice(-2), ["show", "mount"]);
});

test("repeated reveal and restore while shown do nothing extra", async () => {
  const h = revealHarness();
  h.reveal.restore();
  assert.deepEqual(h.log, ["show"]);
  h.reveal.reveal(); h.reveal.reveal();
  assert.deepEqual(h.log, ["show", "finish"]);
});

import {
  CONTACT,
  WhaleSimulation,
  buildUnifiedTailMesh,
  projectTail,
  samplePose,
} from "../src/splash/whale-sim";

test("unified 3D whale mesh is a continuous dorsal/ventral surface and submerges cleanly", () => {
  const raised = buildUnifiedTailMesh(samplePose(1580));
  assert.ok(raised.spine.length >= 16, "spine should have smooth lengthwise stations");
  assert.ok(raised.flukeOutline3D.length >= 80, "real Bezier fluke silhouette should be high-resolution");
  assert.ok(raised.triangles.some(t => t.dorsal) && raised.triangles.some(t => !t.dorsal), "mesh must have distinct dorsal and ventral faces");
  assert.ok(projectTail(samplePose(1580)).triangles.length > 50);

  const submerged = projectTail({...samplePose(0), sinkY: -260});
  assert.equal(submerged.triangles.length, 0);
});

test("failure during active swing or impact clears all spray and submerges the tail", async () => {
  for (const triggerMs of [1200, CONTACT.time + 40]) {
    const sim = new WhaleSimulation();
    while (sim.clock < triggerMs) sim.step(16);
    assert.ok(sim.droplets.length > 0, `expected airborne droplets at ${triggerMs}ms`);

    let settled = false;
    const done = sim.finish("failed").then(() => { settled = true; });
    for (let i = 0; i < 80 && !settled; i++) sim.step(16);
    await done;

    assert.equal(settled, true);
    assert.equal(sim.droplets.length, 0, "no frozen droplets may remain after failure");
    assert.equal(sim.foams.length, 0, "no frozen foam may remain after failure");
    assert.equal(sim.wakes.length, 0, "no frozen wake may remain after failure");
    assert.equal(projectTail(sim.pose).triangles.length, 0, "tail must be fully submerged");
  }
});

test("ready completes an in-progress slap and settles promptly after impact or on warm start", async () => {
  const simDown = new WhaleSimulation();
  while (simDown.clock < 2900) simDown.step(16);
  let resolvedDown = false;
  const doneDown = simDown.finish("ready").then(() => { resolvedDown = true; });
  for (let i = 0; i < 60 && !resolvedDown; i++) simDown.step(16);
  await doneDown;
  assert.equal(resolvedDown, true);
  assert.ok(simDown.wakes.length > 0, "completing a downswing should leave a wake pulse");

  const simEarly = new WhaleSimulation();
  simEarly.step(32);
  let resolvedEarly = false;
  let elapsedEarly = 0;
  const doneEarly = simEarly.finish("ready").then(() => { resolvedEarly = true; });
  while (simEarly.mode !== "settled" && elapsedEarly < 1000) { simEarly.step(16); elapsedEarly += 16; }
  await doneEarly;
  assert.equal(resolvedEarly, true);
  assert.ok(elapsedEarly <= 450, `warm start ready should settle swiftly, got ${elapsedEarly}ms`);
});

test("immediate finish produces distinct reduced-motion ready and failed states", async () => {
  const sim = new WhaleSimulation();
  await sim.finish("failed", true);
  assert.equal(sim.droplets.length, 0);
  assert.equal(sim.wakes.length, 0);
  assert.equal(projectTail(sim.pose).triangles.length, 0);

  await sim.finish("ready", true);
  assert.ok(sim.wakes.length > 0);
});
