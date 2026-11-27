import type {PublicStep} from "./steps";
import {WhaleScene} from "./whale";

export type Outcome = "ready" | "failed";
export interface SceneProgress { step: PublicStep; stepIndex: number; progress: number }
export interface SplashScene {
  /** Starts rendering on the canvas; resolves after the first frame is drawn. */
  start(canvas: HTMLCanvasElement, options: {reducedMotion: boolean}): Promise<void>;
  progress(update: SceneProgress): void;
  /** Plays the closing animation; resolves when it ends. */
  finish(outcome: Outcome): Promise<void>;
  /** Returns to the waiting loop (retry). */
  reset(): void;
  setReducedMotion(value: boolean): void;
  pause(): void;
  resume(): void;
  dispose(): void;
}
export interface BuiltinScene { nameKey: string; background: string; create(): SplashScene }

export const WHALE_BACKGROUND = "var(--canvas)";
export const builtinScenes: Record<string, BuiltinScene> = {
  "builtin-whale": {nameKey: "splash.whale", background: WHALE_BACKGROUND, create: () => new WhaleScene()},
};
