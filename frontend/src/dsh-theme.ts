// dsh-work paints DSH from its own theme (docs/architecture.md, "Page bootstrap
// and host plugins"). Each DSH token names the dsh-work tokens it is built from,
// first usable value wins; a token with no usable source keeps DSH's own value.
// Every DSH surface is opaque and carries text that reads (4.5:1): some themes
// use translucent fills, or colours never meant to carry DSH's body text.
// Beyond colours, shapes, shadows and the interface font follow the theme; DSH's
// conversation text keeps its own font (the plugin pins it).

/** A colour resolved to sRGB channels 0-255 and alpha 0-1. */
export type Rgba = [number, number, number, number];
type Read = (name: string) => string;
type Resolve = (value: string) => Rgba | undefined;
interface Palette {
  read: Read;
  /** A theme token resolved to a colour, or undefined when it is not one. */
  color(name: string): Rgba | undefined;
}
type Paint = (palette: Palette) => string;
type Source = string | Paint;

/** Resolves hex and rgb()/rgba() values; the shell passes a browser resolver for the rest. */
export function parseColor(value: string): Rgba | undefined {
  const hex = /^#([0-9a-f]{3,4}|[0-9a-f]{6}|[0-9a-f]{8})$/i.exec(value.trim())?.[1];
  if (hex) {
    const full = hex.length <= 4 ? hex.split("").map(digit => digit + digit).join("") : hex;
    const channel = (at: number) => parseInt(full.slice(at, at + 2), 16);
    return [channel(0), channel(2), channel(4), full.length === 8 ? channel(6) / 255 : 1];
  }
  const match = /^rgba?\(\s*([\d.]+)[\s,]+([\d.]+)[\s,]+([\d.]+)\s*(?:[,/]\s*([\d.]+)(%?)\s*)?\)$/i.exec(value.trim());
  if (!match) return undefined;
  const alpha = match[4] === undefined ? 1 : Number(match[4]) / (match[5] ? 100 : 1);
  return [Number(match[1]), Number(match[2]), Number(match[3]), alpha];
}

const css = ([r, g, b, a]: Rgba) => a >= 1
  ? `rgb(${Math.round(r)}, ${Math.round(g)}, ${Math.round(b)})`
  : `rgba(${Math.round(r)}, ${Math.round(g)}, ${Math.round(b)}, ${+a.toFixed(3)})`;
const over = ([r, g, b, a]: Rgba, [R, G, B]: Rgba): Rgba => [r * a + R * (1 - a), g * a + G * (1 - a), b * a + B * (1 - a), 1];
const blend = (a: Rgba, b: Rgba, weight: number): Rgba => [
  a[0] * weight + b[0] * (1 - weight), a[1] * weight + b[1] * (1 - weight), a[2] * weight + b[2] * (1 - weight), 1,
];

function luminance([r, g, b]: Rgba) {
  const linear = (channel: number) => {
    const c = channel / 255;
    return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
  };
  return 0.2126 * linear(r) + 0.7152 * linear(g) + 0.0722 * linear(b);
}

/** WCAG contrast of two opaque colours. */
export function contrastRatio(a: Rgba, b: Rgba) {
  const [light, dark] = [luminance(a), luminance(b)].sort((p, q) => q - p);
  return (light + 0.05) / (dark + 0.05);
}

const WHITE: Rgba = [255, 255, 255, 1];
const readable = (text: Rgba | undefined, surface: Rgba) => !text || contrastRatio(over(text, surface), surface) >= 4.5;

// DSH body text sits on the canvas unless the theme's text does not read there
// (Classic's canvas is a desktop colour).
function base(palette: Palette): Rgba {
  const canvas = over(palette.color("--canvas") ?? WHITE, WHITE);
  for (const name of ["--canvas", "--group-bg"]) {
    const value = palette.color(name);
    if (!value) continue;
    const solid = over(value, canvas);
    if (readable(palette.color("--text"), solid)) return solid;
  }
  return canvas;
}

const baseColor: Source = palette => css(base(palette));

// The window frame: dsh-work's title bar and the DSH sidebar beneath it are one
// surface, and dsh-work decides it. It is the theme's navigation fill, or the
// first quieter fill the theme's text reads on, made opaque.
const FRAME_CANDIDATES = ["--nav-bg", "--wash", "--group-bg"];
function frame(palette: Palette): {fill: string; source: string} | undefined {
  const under = base(palette);
  for (const name of FRAME_CANDIDATES) {
    const value = palette.color(name);
    if (!value) continue;
    const solid = over(value, under);
    if (readable(palette.color("--text"), solid)) return {fill: css(solid), source: name};
  }
  return undefined;
}

/** Fills for title bar buttons under the pointer and with their menu open, and the open button's text. */
export type FrameStates = {hover: string; active: string; activeText: string};

// Button states on a frame. The theme's navigation states belong to its
// navigation fill; on any other frame the bar uses the theme's general hover and
// selection. A state must read with its text and stand out from the frame,
// otherwise the frame's text is laid over it as a tint.
function frameStates(palette: Palette, fill: Rgba, text: string, nav: boolean): FrameStates {
  const textColor = palette.color(text) ?? palette.color("--text") ?? [0, 0, 0, 1];
  const pick = (pairs: [string, string][], tint: number) => {
    for (const [name, label] of pairs) {
      const value = palette.color(name);
      if (!value) continue;
      const solid = over(value, fill);
      // a label such as "inherit" names no colour of its own
      const ink = palette.color(label) && !/^(inherit|initial|unset|revert|currentcolor)$/i.test(palette.read(label)) ? label : text;
      if (contrastRatio(solid, fill) >= 1.05 && readable(palette.color(ink) ?? textColor, solid)) return {fill: css(solid), text: palette.read(ink)};
    }
    // toward the text, or away from it where that would cost the text its contrast
    const ink = over(textColor, fill);
    const toward = blend(ink, fill, tint);
    const away = blend(luminance(ink) > luminance(fill) ? [0, 0, 0, 1] : WHITE, fill, tint);
    return {fill: css(readable(ink, toward) ? toward : away), text: palette.read(text)};
  };
  const hover = pick([...(nav ? [["--nav-hover-bg", text]] as [string, string][] : []), ["--hover", text]], 0.08);
  const active = pick([...(nav ? [["--nav-active-bg", "--nav-active-fg"]] as [string, string][] : []), ["--selection", "--selection-fg"]], 0.14);
  return {hover: hover.fill, active: active.fill, activeText: active.text};
}

const paletteOf = (read: Read, resolve: Resolve): Palette => ({read, color: name => { const value = read(name); return value ? resolve(value) : undefined; }});

/** The window frame colours dsh-work paints its title bar with and gives DSH's sidebar. */
export function windowFrame(read: Read, resolve: Resolve = parseColor): ({fill: string; text: string} & FrameStates) | undefined {
  const palette = paletteOf(read, resolve);
  const chosen = frame(palette);
  const fill = chosen && parseColor(chosen.fill);
  if (!chosen || !fill) return undefined;
  // Navigation text belongs to the navigation fill; a quieter fill takes body text.
  const nav = chosen.source === "--nav-bg";
  const text = nav && read("--nav-fg") ? "--nav-fg" : "--text";
  return {fill: chosen.fill, text: read(text), ...frameStates(palette, fill, text, nav)};
}

/** The title bar on the startup page, where the bar is the page's own canvas. */
export function pageFrame(read: Read, resolve: Resolve = parseColor): {text: string} & FrameStates {
  const palette = paletteOf(read, resolve);
  const ground = over(palette.color("--canvas") ?? WHITE, WHITE);
  const text = ["--text", "--on-ink", "--canvas"].find(name => {
    const ink = palette.color(name);
    return ink && contrastRatio(over(ink, ground), ground) >= 4.5;
  }) ?? "--text";
  return {text: read(text), ...frameStates(palette, ground, text, false)};
}

// A surface DSH draws over its base: the first candidate the theme's text reads
// on, made opaque. A translucent theme fill would let the page show through
// DSH's dialogs and menus, and text would vanish where DSH inverts colours.
const surface = (...candidates: string[]): Paint => palette => {
  const under = base(palette);
  for (const name of candidates) {
    const value = palette.color(name);
    if (!value) continue;
    const solid = over(value, under);
    if (readable(palette.color("--text"), solid)) return css(solid);
  }
  return "";
};

// A tone between a theme colour and DSH's base surface (placeholders, scrollbars).
const toward = (name: string, weight: number): Source => palette => {
  const value = palette.color(name);
  const under = base(palette);
  return value ? css(blend(over(value, under), under, weight)) : "";
};

// A colour DSH also uses as text (links, active tabs, status labels): moved
// toward the theme's text colour until it reads on DSH's base surface, with a
// check against the tints DSH lays under it (tags use 10-20% of the colour). A theme
// accent is often a fill colour (Neo-brutalism's pink) that cannot carry text.
const legible = (source: string, target = 5): Source => palette => {
  const value = source.startsWith("--") ? palette.color(source) : parseColor(source);
  if (!value) return "";
  const under = base(palette);
  const text = over(palette.color("--text") ?? [0, 0, 0, 1], under);
  const start = over(value, under);
  // DSH puts this text on its base, on raised cards, on tints of itself and on badge fills.
  const surfaces = [under, ...[surface("--group-bg")(palette), badgeFill(palette)].flatMap(value => {
    const resolved = value ? parseColor(value) : undefined;
    return resolved ? [resolved] : [];
  })];
  for (let weight = 1; weight >= 0; weight -= 0.05) {
    const candidate = blend(start, text, weight);
    if (contrastRatio(candidate, under) < target) continue;
    if (surfaces.every(fill => contrastRatio(candidate, fill) >= 4.5 && contrastRatio(candidate, blend(candidate, fill, 0.2)) >= 4.5)) return css(candidate);
  }
  return css(text);
};

// Text on a fill DSH receives: the first candidate that reads on it.
const textOn = (fill: Source, ...candidates: string[]): Source => palette => {
  const painted = typeof fill === "string" ? palette.read(fill) : fill(palette);
  const under = painted ? parseColor(painted) : undefined;
  for (const name of candidates) {
    const value = palette.color(name);
    if (!value) continue;
    if (!under || readable(value, over(under, base(palette)))) return palette.read(name);
  }
  return "";
};

// DSH draws a white glyph on these fills, so a light accent is darkened until the glyph reads.
const underWhite = (name: string) => (palette: Palette) => {
  const value = palette.color(name);
  if (!value) return "";
  let colour = over(value, base(palette));
  while (contrastRatio(colour, WHITE) < 4.5) colour = [colour[0] * 0.9, colour[1] * 0.9, colour[2] * 0.9, 1];
  return css(colour);
};

// The accent behind the theme's own on-accent label, darkened when that label is white.
const accentFill: Source = palette => {
  const label = palette.color("--on-accent");
  return label && contrastRatio(over(label, WHITE), WHITE) < 1.5 ? underWhite("--accent")(palette) : palette.read("--accent");
};

// Badges (DSH's "Preview" tag) sit on the selection tint, or a quieter fill when body text does not read on it.
const badgeFill = surface("--selection", "--wash", "--hover");

// A monospace stack whose CJK text uses the interface font: Latin mono faces
// carry no CJK glyphs, and the generic monospace family maps CJK to a serif face.
const monoWithText: Paint = palette => {
  const mono = palette.read("--font-mono");
  const body = palette.read("--font-body");
  if (!mono) return "";
  const families = (value: string) => value.split(",").map(family => family.trim()).filter(Boolean);
  const generic = new Set(["monospace", "sans-serif", "serif", "system-ui", "ui-monospace"]);
  const named = [...families(mono), ...families(body)].filter(family => !generic.has(family.toLowerCase()));
  return named.filter((family, index) => named.indexOf(family) === index).concat("monospace").join(", ");
};

const isPaint = (value: string) => value !== "" && !/gradient|url\(/.test(value);

const dshTokenSources: Record<string, Source[]> = {
  // surfaces
  "--dsw-alias-bg-base": [baseColor],
  "--dsw-alias-bg-layer-1": [surface("--group-bg"), baseColor],
  "--dsw-alias-bg-layer-2": [surface("--group-bg"), baseColor],
  "--dsw-alias-bg-layer-3": [surface("--group-bg"), baseColor],
  "--dsw-alias-settings-card-fill": [surface("--group-bg"), baseColor],
  "--dsw-specific-menu": [surface("--group-bg"), baseColor],
  "--dsw-menu-surface-fill": [surface("--group-bg"), baseColor],
  "--dsw-alias-bg-overlay": [surface("--wash", "--hover"), baseColor],
  // control fills (selects, key caps, chips) are DSH's quiet grey, not the sidebar colour
  "--dsw-alias-bg-module-platform": [surface("--wash", "--hover"), baseColor],
  "--dsw-alias-bg-multi-select": [surface("--wash", "--hover"), baseColor],
  "--dsw-alias-bg-mask-1": ["--backdrop"],
  "--dsw-specific-sidebar-fill": [palette => frame(palette)?.fill ?? ""],
  "--dsw-specific-sidebar-nav-item-hover": [surface("--nav-hover-bg", "--hover")],
  "--dsw-specific-sidebar-nav-item-active": [surface("--nav-active-bg", "--selection")],
  "--dsw-specific-sidebar-nav-item-active-accent": [surface("--selection")],
  "--dsw-specific-input-major": [surface("--input-bg"), baseColor],
  // raised buttons (New session, floating actions) take the theme's button fill
  "--dsw-alias-button-elevated-fill": [surface("--button-bg", "--group-bg"), baseColor],
  "--dsw-alias-button-floating-fill": [surface("--button-bg", "--group-bg"), baseColor],
  "--dsw-alias-button-floating-hover": [surface("--button-hover-bg", "--hover"), baseColor],
  "--dsw-specific-selector": [surface("--wash", "--hover")],
  "--dsw-specific-bubble": [surface("--wash", "--hover")],
  "--dsw-alias-markdown-code-block": [surface("--wash", "--hover")],
  "--dsw-alias-markdown-code-block-banner": [surface("--wash", "--hover")],
  "--dsw-alias-markdown-inline-code": [surface("--wash", "--hover")],
  // lines
  "--dsw-alias-border-l1": ["--line"],
  "--dsw-alias-border-l2": ["--line"],
  "--dsw-alias-border-l3": ["--line"],
  "--dsw-alias-border-l4": ["--line"],
  "--dsw-alias-border-l2-darkmode-thin": ["--line"],
  // a text glyph ("·") between status items, so it takes secondary text
  "--dsw-alias-separator-primary": [legible("--muted")],
  "--dsw-alias-settings-card-stroke": ["--line"],
  "--dsw-alias-scrollbar-bg-l1": [toward("--text", 0.22)],
  "--dsw-alias-scrollbar-bg-l2": [toward("--text", 0.22)],
  "--dsw-alias-scrollbar-hover-l1": [toward("--text", 0.38)],
  "--dsw-alias-scrollbar-hover-l2": [toward("--text", 0.38)],
  // text
  "--dsw-alias-label-primary": ["--text"],
  "--dsw-alias-label-primary-dimmed": ["--text"],
  "--dsw-alias-brand-text": ["--text"],
  "--dsw-alias-label-secondary": [toward("--text", 0.78), "--muted"],
  // secondary text also sits on raised controls, so it keeps a margin over the base
  "--dsw-alias-label-tertiary": [legible("--muted", 5)],
  "--dsw-alias-label-caption": [legible("--muted", 5)],
  "--dsw-alias-label-dimmed": [toward("--muted", 0.7)],
  "--dsw-alias-label-quaternary": [toward("--muted", 0.7)],
  "--dsw-alias-menu-icon": ["--muted"],
  // accent
  "--dsw-alias-brand-primary": ["--accent"],
  // DSH mixes this with the secondary error colour for trajectory kind-tag text
  "--dsw-alias-brand-primary-new-colorprimary-new-color": [legible("--accent")],
  "--dsw-alias-label-deep-diving": [legible("--accent")],
  "--dsw-alias-label-deep-diving-shimmer": [toward("--accent", 0.45)],
  "--dsw-alias-button-primary-fill": ["--primary-bg", accentFill],
  "--dsw-alias-button-primary-hover": ["--primary-hover-bg", accentFill],
  "--dsw-alias-button-info-fill": [underWhite("--accent")],
  "--dsw-alias-button-info-hover": [underWhite("--accent")],
  // The foreground pairs with whichever fill the primary button ended up with.
  "--dsw-alias-label-primary-foreground": [palette => isPaint(palette.read("--primary-bg")) ? palette.read("--primary-fg") : palette.read("--on-accent")],
  "--dsw-alias-label-primary-inverted": ["--on-ink", baseColor],
  "--dsw-alias-link": [legible("--accent")],
  "--dsw-alias-state-business-primary": [legible("--accent")],
  "--dsw-alias-state-business-tertiary": [badgeFill],
  // Badge text on that fill: the accent when it reads there, body text otherwise.
  "--dsw-alias-label-primary-bluish": [textOn(badgeFill, "--accent", "--selection-fg", "--text", "--on-ink")],
  "--dsw-focus-ring-color": ["--focus"],
  // interaction
  "--dsw-alias-interactive-bg-hover": ["--hover"],
  "--dsw-alias-interactive-bg-hover-solid": [surface("--hover")],
  "--dsw-alias-interactive-bg-active": ["--selection"],
  // status: error follows the theme; success and warning keep DSH's hues
  "--dsw-alias-state-error-primary": [legible("--failed")],
  "--dsw-alias-label-error": [legible("--failed")],
  // only DSH's decorative violet marks use it, mixed with the accent above; a mix
  // of two hues is darker than either, so it takes the same legible accent
  "--dsw-alias-state-error-secondary": [legible("--accent")],
  "--dsw-alias-state-success-primary": [legible("#22c55e")],
  "--dsw-alias-state-warn-label": [legible("#dd8629")],
  // shapes: tags, controls, then groups and dialogs
  "--dsw-radius-xs": ["--radius-tag"],
  "--dsw-radius-sm": ["--radius-control"],
  "--dsw-radius-md": ["--radius-control"],
  "--dsw-radius-lg": ["--radius-group"],
  "--dsw-radius-xl": ["--radius-group"],
  "--dsw-radius-panel": ["--radius-group"],
  // shadows: raised groups, then floating menus and dialogs
  "--dsw-shadow-lv1": ["--group-shadow"],
  "--dsw-shadow-lv2": ["--dialog-shadow"],
  "--dsw-shadow-lv3": ["--dialog-shadow"],
  "--dsw-elevation-stroke-color": ["--line"],
  "--dsw-menu-backdrop-filter": ["--surface-blur"],
  // interface type
  "--dsw-font-family": ["--font-body"],
  "--dsw-font-family-brand": ["--font-heading", "--font-body"],
  // DSH reads this for package names and module rows but never defines it; its
  // fallback (ui-monospace) is unknown on Windows, where CJK falls to a serif face
  "--dsw-font-mono": [monoWithText],
  // floating labels
  "--dsw-alias-tooltip-bg": ["--ink"],
  "--dsw-alias-toast-bg": ["--ink"],
  "--dsw-alias-toast-label": ["--on-ink"],
};

/** The CSS property a DSH token's value is checked against. */
export function dshTokenProperty(name: string) {
  if (name.endsWith("-border") || name.endsWith("-divider")) return "border";
  if (name === "--dsh-work-backdrop") return "background-image";
  if (name === "--dsh-work-blur" || name === "--dsw-menu-backdrop-filter") return "backdrop-filter";
  if (name.startsWith("--dsh-work-")) return "box-shadow";
  if (name.startsWith("--dsw-radius-")) return "border-radius";
  if (name.startsWith("--dsw-shadow-")) return "box-shadow";
  if (name.startsWith("--dsw-font-family") || name === "--dsw-font-mono") return "font-family";
  return "color";
}

/** The DSH token values for the dsh-work theme painted on `root`. */
export function dshThemeTokens(read: Read, supports: (property: string, value: string) => boolean, resolve: Resolve = parseColor): Record<string, string> {
  const palette: Palette = {read, color: name => { const value = read(name); return value ? resolve(value) : undefined; }};
  const tokens: Record<string, string> = {};
  for (const [name, sources] of Object.entries(dshTokenSources)) {
    for (const source of sources) {
      const value = typeof source === "string" ? read(source) : source(palette);
      if (value && supports(dshTokenProperty(name), value)) {
        tokens[name] = value;
        break;
      }
    }
  }
  return tokens;
}

/** A resolved border: width and colour, or undefined when the theme draws none. */
export type Border = {width: string; color: string} | undefined;

/**
 * Decoration values for DSH's raised and floating surfaces. DSH's border widths
 * have no token, so a theme border becomes a box-shadow ring of the same width.
 */
export function dshDecorTokens(read: Read, border: (name: string) => Border): Record<string, string> {
  const ring = (name: string) => {
    const resolved = border(name);
    return resolved ? `0 0 0 ${resolved.width} ${resolved.color}` : "";
  };
  const shadow = (name: string) => {
    const value = read(name);
    return value && value !== "none" ? value : "";
  };
  const join = (...parts: string[]) => parts.filter(Boolean).join(", ") || "none";
  const line = (name: string) => {
    const resolved = border(name);
    return resolved ? `${resolved.width} solid ${resolved.color}` : "0 solid transparent";
  };
  // DSH's raised surfaces (menus, popovers, floating buttons, cards) take the
  // theme's group frame. Where groups lie flat on the page (Claude, Swiss) that
  // frame is empty and the surfaces would lose their edges, so they take the
  // theme's floating frame instead.
  const group = join(ring("--group-border"), shadow("--group-shadow"));
  const flat = group === "none";
  return {
    // DSH lists drawn as the theme's groups (plugin list, automation tasks)
    "--dsh-work-group-border": line("--group-border"),
    "--dsh-work-group-shadow": shadow("--group-shadow") || "none",
    "--dsh-work-row-divider": line("--row-divider"),
    "--dsh-work-stroke": ring("--group-border") || (flat && ring("--dialog-border")) || "0 0 0 0 transparent",
    "--dsh-work-raised": flat ? join(ring("--dialog-border"), shadow("--dialog-shadow")) : group,
    "--dsh-work-floating": join(ring("--dialog-border"), shadow("--dialog-shadow")),
    // The composer is a text field: on flat groups it keeps the theme's input frame
    "--dsh-work-composer": join(ring("--group-border") || ring("--input-border"), shadow("--group-shadow") || (flat ? shadow("--dialog-shadow") : "")),
    "--dsh-work-button": join(ring("--button-border"), shadow("--button-shadow")),
    "--dsh-work-backdrop": read("--aurora") || "none",
    "--dsh-work-blur": read("--surface-blur") || "none",
  };
}
