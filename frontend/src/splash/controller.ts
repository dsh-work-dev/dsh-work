import type {Outcome, SceneProgress} from "./scene";
import {publicSteps, type PublicStep} from "./steps";

/** Longest the startup surface waits for a scene's closing animation. */
export const FINISH_TIMEOUT_MS = 1200;

export interface ControlledScene {
  progress(update: SceneProgress): void;
  finish(outcome: Outcome): Promise<void>;
  reset(): void;
}

/** DOM-free rules: progress only moves forward, finish is bounded, reset replays. */
export class SplashController {
  private scene?: ControlledScene;
  private step: PublicStep = "node";
  private best = 0;
  private sentStep?: PublicStep;
  private sent = -1;
  private outcome?: Outcome;
  private finishing?: Promise<void>;
  private disposed = false;

  constructor(private readonly onDim: (dim: boolean) => void) {}

  attach(scene: ControlledScene | undefined) {
    if (this.disposed) return;
    this.scene = scene;
    this.sentStep = undefined;
    this.sent = -1;
    if (scene && this.outcome) {
      const outcome = this.outcome;
      try { void Promise.resolve(scene.finish(outcome)).catch(() => {}); } catch { /* a broken scene must not break startup */ }
      return;
    }
    this.flush();
  }

  /** Detaches the scene and cancels any pending finish side effects. */
  dispose() {
    this.disposed = true;
    this.scene = undefined;
    this.outcome = undefined;
    this.finishing = undefined;
  }

  progress(step: PublicStep, within: number | null) {
    if (this.outcome || this.disposed) return;
    const index = publicSteps.indexOf(step);
    if (index >= publicSteps.indexOf(this.step)) this.step = step;
    const fraction = within === null || !Number.isFinite(within) ? 0 : Math.min(1, Math.max(0, within));
    this.best = Math.max(this.best, (index + fraction) / publicSteps.length);
    this.flush();
  }

  finish(outcome: Outcome): Promise<void> {
    if (this.disposed) return Promise.resolve();
    if (this.outcome === outcome && this.finishing) return this.finishing;
    this.outcome = outcome;
    const scene = this.scene;
    let work = Promise.resolve();
    // Start now so a Reset also cancels a close requested in the same turn.
    if (scene) {
      try { work = scene.finish(outcome).catch(() => {}); } catch { /* a broken scene must not break startup */ }
    }
    const finishing: Promise<void> = new Promise<void>(resolve => {
      const timer = setTimeout(resolve, FINISH_TIMEOUT_MS);
      void work.then(() => { clearTimeout(timer); resolve(); });
    }).then(() => {
      if (this.finishing === finishing && outcome === "failed") this.onDim(true);
    });
    this.finishing = finishing;
    return finishing;
  }

  reset() {
    if (!this.outcome || this.disposed) return;
    const dimmed = this.outcome === "failed";
    this.outcome = undefined;
    this.finishing = undefined;
    if (dimmed) this.onDim(false);
    this.scene?.reset();
    this.sentStep = undefined;
    this.sent = -1;
    this.flush();
  }

  private flush() {
    if (!this.scene || this.outcome) return;
    if (this.step === this.sentStep && Math.abs(this.best - this.sent) < 0.01) return;
    this.sentStep = this.step;
    this.sent = this.best;
    this.scene.progress({step: this.step, stepIndex: publicSteps.indexOf(this.step), progress: this.best});
  }
}
