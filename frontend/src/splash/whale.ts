import {prepareWithSegments, setLocale as setPretextLocale} from "@chenglou/pretext";
import {currentLocale, subscribeLocale, type Locale} from "../i18n";
import type {Outcome, SceneProgress, SplashScene} from "./scene";
import {
  CONTACT,
  STILL,
  WATERLINE,
  WhaleSimulation,
  projectTail,
  samplePose,
  type Pose,
} from "./whale-sim";

// Offscreen 3D shading buffer dimensions (each pixel = one typographic cell).
const STAGE_MIN_X = -320;
const STAGE_W = 640;
const STAGE_MIN_Y = -215;
const STAGE_H = 345;
const COLS = 108;
const ROWS = 46;
const CELL_W = STAGE_W / COLS; // ~5.93 logical px
const CELL_H = STAGE_H / ROWS; // ~7.50 logical px

// Luminance-ordered brand shading ramp (heaviest ink -> lightest edge feather):
// every letter comes from DSH WORK + clean typographic shading marks so 3D curvature
// and specular highlights read clearly like a real 3D sculpture.
const SHADE_RAMP = [".", "·", "-", "~", ":", ";", "/", "=", "+", "*", "S", "R", "O", "K", "D", "H", "#", "W"] as const;
const CJK_CORE: Record<Locale, string> = {
  "zh-CN": "本地工作台独立守护隐私优先可恢复环境",
  en: "",
  "ja-JP": "ローカル信頼起動プライバシー復元環境",
};

const SURFACE_GLYPHS = ["·", "–", "~", "≈", "·", "—", ":", "·"] as const;
const FOAM_GLYPHS = ["·", ":", "~", "≈", "+", "*", "·"] as const;

export class WhaleScene implements SplashScene {
  private canvas?: HTMLCanvasElement;
  private ctx?: CanvasRenderingContext2D;
  private offCanvas: HTMLCanvasElement;
  private offCtx: CanvasRenderingContext2D | null;
  private resizeObserver?: ResizeObserver;
  private themeObserver?: MutationObserver;
  private offLocale?: () => void;

  private sim = new WhaleSimulation();
  private grid = "monospace";
  private ink = "#111";
  private waveInk = "#444";
  private width = 0;
  private height = 0;
  private inStartup = false;

  private denseTokens: string[] = [];
  private isWideDense = false;
  private tokensKey = "";

  private last?: number;
  private raf = 0;
  private reduced = false;
  private reducedState: "waiting" | Outcome = "waiting";
  private paused = false;

  constructor() {
    this.offCanvas = document.createElement("canvas");
    this.offCanvas.width = COLS;
    this.offCanvas.height = ROWS;
    this.offCtx = this.offCanvas.getContext("2d", {willReadFrequently: true});
  }

  async start(canvas: HTMLCanvasElement, {reducedMotion}: {reducedMotion: boolean}) {
    const ctx = canvas.getContext("2d");
    if (!ctx) throw new Error("Splash canvas unavailable");
    this.canvas = canvas;
    this.ctx = ctx;
    this.reduced = reducedMotion;
    this.reducedState = "waiting";
    this.inStartup = !!canvas.closest(".startup");

    this.resizeObserver = new ResizeObserver(() => this.resize());
    this.resizeObserver.observe(canvas);
    this.themeObserver = new MutationObserver(() => this.syncTheme());
    this.themeObserver.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ["data-theme", "data-theme-id"],
    });
    this.offLocale = subscribeLocale(() => {
      this.tokensKey = "";
      this.ensureTokens();
      this.paint();
    });

    this.resize();
    this.schedule();
  }

  progress(update: SceneProgress) {
    this.sim.setProgress(update.progress);
  }

  finish(outcome: Outcome): Promise<void> {
    this.reducedState = outcome;
    if (this.reduced || this.paused || !this.ctx) {
      const done = this.sim.finish(outcome, true);
      this.paint();
      return done;
    }
    const done = this.sim.finish(outcome, false);
    this.schedule();
    return done;
  }

  reset() {
    this.reducedState = "waiting";
    this.sim.reset();
    this.last = undefined;
    this.paint();
    this.schedule();
  }

  setReducedMotion(value: boolean) {
    this.reduced = value;
    if (value) {
      this.stop();
      this.sim.settleTransition();
    }
    this.paint();
    this.schedule();
  }

  pause() {
    this.paused = true;
    this.stop();
    this.sim.settleTransition();
  }

  resume() {
    this.paused = false;
    this.schedule();
  }

  dispose() {
    this.pause();
    this.resizeObserver?.disconnect();
    this.themeObserver?.disconnect();
    this.offLocale?.();
    this.offLocale = undefined;
    this.canvas = this.ctx = undefined;
  }

  private stop() {
    cancelAnimationFrame(this.raf);
    this.raf = 0;
    this.last = undefined;
  }

  private schedule() {
    if (this.raf || !this.ctx || this.paused || this.reduced || !this.sim.isActive) return;
    this.raf = requestAnimationFrame(now => {
      this.raf = 0;
      const dt = Math.min(48, now - (this.last ?? now));
      this.last = now;
      this.sim.step(dt);
      this.paint();
      this.schedule();
    });
  }

  private syncTheme() {
    const canvas = this.canvas;
    if (!canvas) return;
    const style = getComputedStyle(canvas);
    this.ink = style.color || "#111";
    this.waveInk = style.getPropertyValue("--splash-water-ink").trim() || this.ink;
    this.grid = style.fontFamily || "monospace";
    this.ensureTokens();
    this.paint();
  }

  private resize() {
    const canvas = this.canvas;
    if (!canvas || !this.ctx) return;
    this.width = canvas.clientWidth;
    this.height = canvas.clientHeight;
    if (!this.width || !this.height) return;
    const dpr = Math.min(window.devicePixelRatio || 1, 2);
    canvas.width = Math.round(this.width * dpr);
    canvas.height = Math.round(this.height * dpr);
    this.ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    this.syncTheme();
  }

  /** Uses @chenglou/pretext to segment & measure locale-aware brand core glyphs. */
  private ensureTokens() {
    const locale = currentLocale();
    const font = `700 8.6px ${this.grid}`;
    const key = `${locale}|${font}`;
    if (this.tokensKey === key) return;
    this.tokensKey = key;
    setPretextLocale(locale);
    const core = CJK_CORE[locale] ?? "";
    if (!core) {
      this.denseTokens = [];
      this.isWideDense = false;
      return;
    }
    const prep = prepareWithSegments(core, font);
    this.denseTokens = Array.from(core);
    const sampleW = prep.widths[0] ?? 6;
    this.isWideDense = sampleW > CELL_W * 1.15;
  }

  private activePose(): Pose {
    if (!this.reduced) return this.sim.pose;
    if (this.reducedState === "failed") {
      return { ...samplePose(0), sinkY: -260 };
    }
    if (this.reducedState === "ready") {
      return samplePose(CONTACT.time + 380);
    }
    return samplePose(STILL);
  }

  private paint() {
    const {ctx, width, height} = this;
    if (!ctx || !width || !height) return;
    this.ensureTokens();

    const usableHeight = this.inStartup ? Math.max(120, height - 56) : height;
    // Bold framing so the 3D character-shaded whale tail commands the stage.
    const scale = Math.min((width * 0.96) / 560, (usableHeight * 0.92) / 275, 1.85);
    const centerY = usableHeight * (this.inStartup ? 0.58 : 0.56);

    ctx.clearRect(0, 0, width, height);
    ctx.save();
    ctx.translate(width / 2, centerY);
    ctx.scale(scale, scale);
    ctx.textBaseline = "middle";
    ctx.textAlign = "center";

    // Distant horizon water bands behind the whale.
    this.paintSurfaceBands(ctx, 0, 2, null);

    const pose = this.activePose();
    const proj = projectTail(pose);
    if (proj.triangles.length > 0) {
      this.paintVolumetricCharacterTail(ctx, proj);
    }

    // Foreground rolling sea + frothy meniscus collar occluding the tail base naturally.
    this.paintSurfaceBands(ctx, 2, 7, proj.waterlineCollar);
    if (proj.waterlineCollar) {
      this.paintWaterlineCollar(ctx, proj.waterlineCollar);
    }

    if (!this.reduced) {
      this.paintFoam(ctx);
      this.paintDroplets(ctx);
    }

    ctx.restore();
  }

  /** Live wave-curved waterline height at horizontal position x (opaque sea-level crest). */
  private waveCrestY(x: number, collar: { x0: number; x1: number } | null): number {
    const phase = this.reduced ? STILL : this.sim.waveMs;
    const swell = Math.sin(x * 0.026 - phase * 0.0018 + 2.3) * 2.4
      + Math.sin(x * 0.063 + phase * 0.0013 - 1.7) * 1.1;
    let lift = 0;
    if (collar) {
      const cx = (collar.x0 + collar.x1) * 0.5;
      const hw = Math.max(24, (collar.x1 - collar.x0) * 0.65);
      lift = Math.exp(-((Math.abs(x - cx) / (hw + 26)) ** 2)) * 4.5;
    }
    return WATERLINE + swell - lift;
  }

  /**
   * Real-time 3D-to-character volumetric shader:
   * Rasterizes our sculpted 3D whale mesh into a tiny COLS x ROWS offscreen shading buffer,
   * clipped by the live rolling ocean wave crest so the tail sits organically behind the water!
   */
  private paintVolumetricCharacterTail(ctx: CanvasRenderingContext2D, proj: ReturnType<typeof projectTail>) {
    const off = this.offCtx;
    if (!off) return;

    off.save();
    off.setTransform(1, 0, 0, 1, 0, 0);
    off.clearRect(0, 0, COLS, ROWS);
    off.scale(COLS / STAGE_W, ROWS / STAGE_H);
    off.translate(-STAGE_MIN_X, -STAGE_MIN_Y);

    // Clip against the live rolling wave crest (never a ruler-straight horizontal cut!).
    off.beginPath();
    off.moveTo(STAGE_MIN_X, STAGE_MIN_Y);
    off.lineTo(STAGE_MIN_X + STAGE_W, STAGE_MIN_Y);
    for (let x = STAGE_MIN_X + STAGE_W; x >= STAGE_MIN_X; x -= 16) {
      off.lineTo(x, this.waveCrestY(x, proj.waterlineCollar));
    }
    off.closePath();
    off.clip();

    // Paint each camera-facing 3D triangle with R = 3D shade, G = dorsal/ventral flag, B = depth.
    for (const tri of proj.triangles) {
      const r = Math.round(Math.min(1, Math.max(0, tri.shade)) * 255);
      const g = tri.dorsal ? 245 : 75;
      const b = Math.round(Math.min(1, Math.max(0.2, 1 - (tri.depth + 160) / 380)) * 255);
      off.fillStyle = `rgb(${r},${g},${b})`;
      off.beginPath();
      off.moveTo(tri.a.x, tri.a.y);
      off.lineTo(tri.b.x, tri.b.y);
      off.lineTo(tri.c.x, tri.c.y);
      off.closePath();
      off.fill();
    }
    off.restore();

    const data = off.getImageData(0, 0, COLS, ROWS).data;
    const denseLen = this.denseTokens.length;
    const rampMax = SHADE_RAMP.length - 1;

    const boldFont = `700 8.5px ${this.grid}`;
    const regFont = `600 8.4px ${this.grid}`;
    const lightFont = `500 8.0px ${this.grid}`;
    let activeFont = "";

    for (let row = 0; row < ROWS; row++) {
      const y = STAGE_MIN_Y + (row + 0.5) * CELL_H;
      if (y > WATERLINE - 1) continue;
      const waterFade = Math.min(1, (WATERLINE - y + 3) / 14);

      for (let col = 0; col < COLS; col++) {
        const idx = (row * COLS + col) * 4;
        const cov = data[idx + 3] / 255;
        if (cov < 0.14) continue;

        const shade = data[idx] / 255;       // 3D directional lighting (brighter where facing light)
        const isDorsal = data[idx + 1] > 160;
        const depthCue = data[idx + 2] / 255;

        // Luminance-to-ink density:
        // Dorsal skin = rich high-contrast ink; ventral skin = slightly softer luminous tone;
        // 3D curvature & shadow directly drive glyph weight from '.' up to 'W / #' / CJK core!
        const darkness = Math.min(
          1,
          Math.max(0, ((isDorsal ? 1.05 : 0.84) * (1.16 - shade * 0.58) * (0.76 + depthCue * 0.24)) * cov),
        );

        const x = STAGE_MIN_X + (col + 0.5) * CELL_W;
        const seed = (row * 17 + col * 5) & 0x7fffffff;

        // Weave localized CJK brand characters into the deepest 3D core cells (non-overlapping).
        if (denseLen > 0 && cov > 0.82 && darkness > 0.72 && (row + col) % 3 === 0) {
          const canPlaceWide = !this.isWideDense || (col + 1 < COLS && data[idx + 7] > 180);
          if (canPlaceWide) {
            const glyph = this.denseTokens[seed % denseLen];
            if (activeFont !== boldFont) { ctx.font = boldFont; activeFont = boldFont; }
            ctx.fillStyle = this.ink;
            ctx.globalAlpha = Math.min(1, (0.78 + darkness * 0.22) * waterFade);
            ctx.fillText(glyph, this.isWideDense ? x + CELL_W * 0.5 : x, y);
            if (this.isWideDense) col++;
            continue;
          }
        }

        // Smooth luminance-ordered brand ASCII shading ramp!
        const rampIdx = Math.min(rampMax, Math.max(0, Math.round(darkness * rampMax)));
        const glyph = SHADE_RAMP[rampIdx];
        const targetFont = rampIdx >= 11 ? boldFont : rampIdx >= 5 ? regFont : lightFont;
        if (activeFont !== targetFont) { ctx.font = targetFont; activeFont = targetFont; }
        ctx.fillStyle = rampIdx >= 7 ? this.ink : this.waveInk;
        ctx.globalAlpha = Math.min(0.96, (0.34 + darkness * 0.64) * waterFade);
        ctx.fillText(glyph, x, y);
      }
    }
  }

  private paintWaterlineCollar(ctx: CanvasRenderingContext2D, collar: { x0: number; x1: number }) {
    const span = collar.x1 - collar.x0;
    const count = Math.max(8, Math.min(24, Math.round(span / 5.5)));
    ctx.fillStyle = this.waveInk;
    for (let i = 0; i <= count; i++) {
      const u = i / count;
      const x = collar.x0 - 16 + u * (span + 32);
      const arch = Math.sin(u * Math.PI);
      const crestY = this.waveCrestY(x, collar);
      // Frothy breaking meniscus hugging the exact curved wave crest where the tail enters the sea
      ctx.font = `700 ${9 + arch * 1.5}px ${this.grid}`;
      ctx.globalAlpha = 0.28 + arch * 0.56;
      ctx.fillText(i % 3 === 0 ? "≈" : i % 3 === 1 ? "~" : ":", x, crestY);
      if (arch > 0.4 && i % 2 === 0) {
        ctx.font = `600 8.5px ${this.grid}`;
        ctx.globalAlpha = 0.22 + arch * 0.36;
        ctx.fillText("·", x + 3, crestY + 4.5);
      }
    }
  }

  private paintSurfaceBands(
    ctx: CanvasRenderingContext2D,
    startRow: number,
    endRow: number,
    collar: { x0: number; x1: number } | null,
  ) {
    ctx.fillStyle = this.waveInk;
    const phase = this.reduced ? STILL : this.sim.waveMs;
    const reducedReady = this.reduced && this.reducedState === "ready";
    const collarCenter = collar ? (collar.x0 + collar.x1) * 0.5 : 0;
    const collarHalf = collar ? Math.max(24, (collar.x1 - collar.x0) * 0.65) : 0;

    for (let row = startRow; row < endRow; row++) {
      const depthT = row / 6;
      const fontSize = 8.2 + depthT * 2.6;
      ctx.font = `600 ${fontSize.toFixed(1)}px ${this.grid}`;
      const stepX = 6.8 + row * 0.85;
      const cols = Math.round(330 / stepX);
      const baseY = WATERLINE - 5 + row * 8.4;
      const rowAlpha = 0.44 + Math.sin(depthT * Math.PI * 0.85) * 0.56;

      for (let i = -cols; i <= cols; i++) {
        // Organic horizontal phase jitter so the sea reads as rolling water rather than a rigid grid.
        const jitter = Math.sin(i * 1.73 + row * 2.91) * 2.2;
        const x = i * stepX + (row % 2) * (stepX * 0.5) + jitter;
        const envelope = Math.max(0, 1 - (Math.abs(x) / 338) ** 1.35);
        if (envelope <= 0.02) continue;

        let pulse = 0;
        let dist = Math.abs(x - CONTACT.x);
        if (this.reduced) {
          if (reducedReady) {
            pulse = Math.exp(-(((dist - (64 + row * 8)) / 46) ** 2)) * 0.72;
          }
        } else {
          const w = this.sim.wakePulseAt(x, row);
          pulse = w.pulse;
          dist = w.dist;
        }

        // Real-time local swell around where the whale pierces the waterline!
        let collarSwell = 0;
        if (collar && row <= 3) {
          const cd = Math.abs(x - collarCenter);
          collarSwell = Math.exp(-((cd / (collarHalf + 26 + row * 12)) ** 2)) * (1 - row * 0.22);
        }

        // Two-octave organic ocean swell (primary rolling wave + secondary cross-chop)
        const primary = Math.sin(x * 0.026 - phase * 0.0018 + row * 1.15);
        const chop = Math.sin(x * 0.063 + phase * 0.0013 - row * 0.85) * 0.45;
        const swell = primary + chop;

        // Impact cavity & wake crest lift
        const wakeWave = Math.sin(dist * 0.045 - phase * 0.0032 + row) * pulse * 9.5 - pulse * 3.0;
        const y = baseY + swell * (1.2 + depthT * 0.9) + wakeWave - collarSwell * 4.2;

        const agitation = Math.min(1, pulse * 0.75 + collarSwell * 0.45);
        ctx.globalAlpha = Math.min(1, envelope * (0.17 + 0.16 * (primary * 0.5 + 0.5) + agitation * 0.62) * rowAlpha);
        const glyph = agitation > 0.34
          ? (i % 2 === 0 ? "≈" : "~")
          : SURFACE_GLYPHS[(i + 64 + row * 3) % SURFACE_GLYPHS.length];
        ctx.fillText(glyph, x, y);
      }
    }
  }

  private paintFoam(ctx: CanvasRenderingContext2D) {
    if (this.sim.foams.length === 0) return;
    ctx.font = `700 10px ${this.grid}`;
    ctx.fillStyle = this.waveInk;
    for (const burst of this.sim.foams) {
      const age = burst.age;
      const foam = Math.max(0, 1 - age / burst.life);
      if (foam <= 0.01) continue;
      for (let i = -24; i <= 24; i++) {
        const edge = Math.abs(i) / 25;
        const x = burst.x + age * 46 + i * (4.1 + age * 3.8);
        for (let r = 0; r < 3; r++) {
          ctx.globalAlpha = foam * (1 - edge ** 2) * (0.62 + 0.16 * r);
          const y = WATERLINE + r * 5.5 - (1 - edge) * Math.sin(i * 1.9 + r) * (5 + 14 * foam);
          ctx.fillText(FOAM_GLYPHS[(i + r + 64) % FOAM_GLYPHS.length], x, y);
        }
      }
    }
  }

  private paintDroplets(ctx: CanvasRenderingContext2D) {
    if (this.sim.droplets.length === 0) return;
    ctx.fillStyle = this.waveInk;
    let lastSize = -1;
    for (const d of this.sim.droplets) {
      if (d.age < 0) continue;
      const fade = Math.max(0, 1 - d.age / d.life);
      if (fade <= 0.01) continue;
      if (d.size !== lastSize) {
        ctx.font = `600 ${d.size}px ${this.grid}`;
        lastSize = d.size;
      }
      ctx.globalAlpha = d.alpha * fade;
      ctx.fillText(d.glyph, d.x, d.y);
    }
  }
}
