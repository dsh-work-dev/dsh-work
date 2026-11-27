import {SplashController} from "./controller";
import {builtinScenes, type BuiltinScene, type Outcome, type SplashScene} from "./scene";
import type {PublicStep} from "./steps";

/** Longest a scene or media file may take to show its first frame. */
export const FIRST_FRAME_TIMEOUT_MS = 3000;

export interface SplashInfo { id: string; name: string; kind: string; url: string; builtin: boolean; selected: boolean }

export interface SplashContainer {
  /** Loads `splash`, or the current choice when omitted, into `host`. */
  mount(host: HTMLElement, splash?: SplashInfo): Promise<void>;
  progress(step: PublicStep, within: number | null): void;
  /** Resolves when the closing animation ends, at most FINISH_TIMEOUT_MS later; never rejects. */
  finish(outcome: Outcome): Promise<void>;
  reset(): void;
  dispose(): void;
}

export interface ContainerDeps { current: () => Promise<SplashInfo>; scenes: Record<string, BuiltinScene> }

const WHALE: SplashInfo = {id: "builtin-whale", name: "", kind: "scene", url: "", builtin: true, selected: true};
const reducedQuery = () => window.matchMedia("(prefers-reduced-motion: reduce)");

class DomSplashContainer implements SplashContainer {
  private host?: HTMLElement;
  private controller = new SplashController(() => {});
  private scene?: SplashScene;
  private element?: HTMLCanvasElement | HTMLImageElement | HTMLVideoElement;
  private cleanups: Array<() => void> = [];
  private epoch = 0;

  constructor(private readonly deps: ContainerDeps) {}

  async mount(host: HTMLElement, splash?: SplashInfo) {
    this.dispose();
    const epoch = this.epoch;
    this.host = host;
    this.controller = new SplashController(dim => host.toggleAttribute("data-dim", dim));
    let info = splash;
    if (!info) info = await this.deps.current().catch(() => WHALE);
    if (epoch !== this.epoch) return;
    if (info.kind === "scene" ? !this.deps.scenes[info.id] : !info.url) info = WHALE;
    const sceneInfo = info;
    host.classList.add("splash-host");
    if (sceneInfo.kind === "scene") await this.mountScene(host, sceneInfo.id, epoch);
    else this.mountMedia(host, sceneInfo);
    if (epoch !== this.epoch) return;
    const query = reducedQuery();
    const onMotion = () => this.applyMotion(query.matches);
    query.addEventListener("change", onMotion);
    const onVisibility = () => {
      if (document.hidden) { this.scene?.pause(); if (this.element instanceof HTMLVideoElement) this.element.pause(); }
      else { this.scene?.resume(); if (this.element instanceof HTMLVideoElement && !query.matches) void this.element.play().catch(() => {}); }
    };
    document.addEventListener("visibilitychange", onVisibility);
    this.cleanups.push(() => query.removeEventListener("change", onMotion), () => document.removeEventListener("visibilitychange", onVisibility));
  }

  progress(step: PublicStep, within: number | null) { this.controller.progress(step, within); }
  finish(outcome: Outcome) { return this.controller.finish(outcome); }
  reset() { this.controller.reset(); }

  dispose() {
    this.epoch++;
    this.scene?.dispose();
    this.scene = undefined;
    if (this.element instanceof HTMLVideoElement) this.element.pause();
    this.element?.remove();
    this.element = undefined;
    for (const cleanup of this.cleanups.splice(0)) cleanup();
    if (this.host) {
      this.host.removeAttribute("data-dim");
      this.host.style.removeProperty("background");
      this.host.classList.remove("splash-host");
    }
    this.controller.dispose();
  }

  private async mountScene(host: HTMLElement, id: string, epoch: number) {
    const entry = this.deps.scenes[id];
    if (!entry) { console.warn("Splash scene unavailable", id); return; }
    host.style.background = entry.background;
    const canvas = document.createElement("canvas");
    canvas.className = "splash-media splash-scene";
    canvas.setAttribute("aria-hidden", "true");
    host.append(canvas);
    this.element = canvas;
    let scene: SplashScene | undefined;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let started = false;
    try {
      scene = entry.create();
      this.scene = scene;
      const timeout = new Promise<false>(resolve => {
        timer = setTimeout(() => resolve(false), FIRST_FRAME_TIMEOUT_MS);
        this.cleanups.push(() => { clearTimeout(timer); resolve(false); });
      });
      started = await Promise.race([scene.start(canvas, {reducedMotion: reducedQuery().matches}).then(() => true, () => false), timeout]);
    } catch { started = false; }
    clearTimeout(timer);
    if (epoch !== this.epoch) return;
    if (!started || !scene) {
      try { scene?.dispose(); } catch { /* already unusable */ }
      this.scene = undefined;
      canvas.remove(); this.element = undefined;
      console.warn("Splash scene unavailable", id);
      return;
    }
    this.scene = scene;
    scene.setReducedMotion(reducedQuery().matches);
    if (document.hidden) scene.pause();
    this.controller.attach(scene);
  }

  private mountMedia(host: HTMLElement, info: SplashInfo) {
    host.style.background = "#000";
    const epoch = this.epoch;
    const element = info.kind === "video" ? document.createElement("video") : document.createElement("img");
    element.className = "splash-media";
    element.setAttribute("aria-hidden", "true");
    const fail = () => {
      if (this.element !== element || epoch !== this.epoch) return;
      if (element instanceof HTMLVideoElement) element.pause();
      element.remove();
      this.element = undefined;
      void this.mountScene(host, WHALE.id, epoch);
    };
    const timer = window.setTimeout(fail, FIRST_FRAME_TIMEOUT_MS);
    const loaded = () => window.clearTimeout(timer);
    const error = () => { loaded(); fail(); };
    const ready = () => {
      loaded();
      if (element instanceof HTMLVideoElement && this.element === element && !reducedQuery().matches && !document.hidden) void element.play().catch(() => {});
    };
    element.addEventListener("error", error);
    const readyEvent = element instanceof HTMLVideoElement ? "loadeddata" : "load";
    element.addEventListener(readyEvent, ready);
    if (element instanceof HTMLVideoElement) {
      element.muted = true; element.loop = true; element.playsInline = true;
    } else {
      element.alt = ""; element.decoding = "async";
    }
    element.src = info.url;
    host.append(element);
    this.element = element;
    this.cleanups.push(() => {
      window.clearTimeout(timer);
      element.removeEventListener("error", error);
      element.removeEventListener(readyEvent, ready);
    });
  }

  private applyMotion(reduced: boolean) {
    this.scene?.setReducedMotion(reduced);
    if (document.hidden) this.scene?.pause();
    if (this.element instanceof HTMLVideoElement) {
      if (reduced || document.hidden) this.element.pause(); else void this.element.play().catch(() => {});
    }
  }
}

async function loadCurrent(): Promise<SplashInfo> {
  const {SplashService} = await import("../../bindings/github.com/local/dsh-work/internal/splash");
  return await SplashService.Current() as SplashInfo;
}

export function createSplashContainer(deps: Partial<ContainerDeps> = {}): SplashContainer {
  return new DomSplashContainer({current: deps.current ?? loadCurrent, scenes: deps.scenes ?? builtinScenes});
}

let shared: SplashContainer | undefined;
/** The startup surface's container, shared by startup.ts and shell.ts. */
export function splashContainer(): SplashContainer {
  return shared ??= createSplashContainer();
}
