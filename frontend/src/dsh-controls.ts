import {contrastRatio, parseColor, type Rgba} from "./dsh-theme";

// DSH controls take the look of dsh-work's own controls (docs/architecture.md,
// "Page bootstrap and host plugins"). Rather than restating each theme's switch,
// button and field rules for DSH, the shell renders hidden samples of its own
// controls and reads what the theme resolved them to, theme-specific ornaments
// included (Classic's bevels, Neo-brutalism's hard shadows).

/** The CSS property each DSH control value is checked against, by name suffix. */
export function controlTokenProperty(name: string) {
  if (/-(border|divider)$/.test(name)) return "border";
  if (/-radius$/.test(name)) return "border-radius";
  if (/-image$/.test(name)) return "background-image";
  if (/-shadow$/.test(name)) return "box-shadow";
  if (/-weight$/.test(name)) return "font-weight";
  if (/-font$/.test(name)) return "font-family";
  if (/-tracking$/.test(name)) return "letter-spacing";
  if (/-(w|h|left|top|size)$/.test(name)) return "width";
  return "color";
}

const SAMPLES = `
  <label class="switch" data-sample="switch-off"><input type="checkbox"><span class="switch-track"><span class="switch-thumb"></span></span></label>
  <label class="switch" data-sample="switch-on"><input type="checkbox" checked><span class="switch-track"><span class="switch-thumb"></span></span></label>
  <button class="button" type="button" data-sample="button">Aa</button>
  <button class="button button-primary" type="button" data-sample="primary">Aa</button>
  <input type="text" data-sample="input">
  <select data-sample="select"><option>Aa</option></select>
  <span class="tag" data-sample="tag">Aa</span>
  <header class="page-head"><h1 data-sample="title">Aa</h1></header>
  <div class="section-head"><h2 data-sample="section">Aa</h2></div>
`;

type Style = CSSStyleDeclaration;
const border = (style: Style, side = "Top") => {
  const width = style.getPropertyValue(`border-${side.toLowerCase()}-width`);
  const kind = style.getPropertyValue(`border-${side.toLowerCase()}-style`);
  return kind === "none" || parseFloat(width) === 0 ? "0 solid transparent" : `${width} ${kind} ${style.getPropertyValue(`border-${side.toLowerCase()}-color`)}`;
};
const surface = (prefix: string, style: Style): Record<string, string> => ({
  [`${prefix}-border`]: border(style),
  [`${prefix}-radius`]: style.borderTopLeftRadius,
  [`${prefix}-bg`]: style.backgroundColor,
  [`${prefix}-image`]: style.backgroundImage,
  [`${prefix}-shadow`]: style.boxShadow,
});

/** Measure dsh-work's resolved controls in `doc`; every value is a plain CSS value. */
export function measureControls(doc: Document, read: (name: string) => string): Record<string, string> {
  const host = doc.createElement("div");
  host.setAttribute("aria-hidden", "true");
  host.style.cssText = "position:fixed;left:-10000px;top:0;visibility:hidden;pointer-events:none;contain:layout style";
  host.innerHTML = SAMPLES;
  // Samples are read at rest: no transition midway, no hover.
  const still = doc.createElement("style");
  still.textContent = "[data-dsh-work-samples] *{transition:none!important;animation:none!important}";
  host.dataset.dshWorkSamples = "";
  doc.body.append(still, host);
  const get = (name: string) => host.querySelector<HTMLElement>(`[data-sample="${name}"]`)!;
  const style = (element: Element) => getComputedStyle(element);
  const tokens: Record<string, string> = {};
  try {
    for (const state of ["off", "on"] as const) {
      const sample = get(`switch-${state}`);
      const track = sample.querySelector(".switch-track")!;
      const thumb = sample.querySelector(".switch-thumb")!;
      const trackStyle = style(track);
      const thumbStyle = style(thumb);
      const trackBox = track.getBoundingClientRect();
      const thumbBox = thumb.getBoundingClientRect();
      const inset = parseFloat(trackStyle.borderLeftWidth) || 0;
      const insetTop = parseFloat(trackStyle.borderTopWidth) || 0;
      Object.assign(tokens, surface(`--dsh-work-switch-${state}-track`, trackStyle), surface(`--dsh-work-switch-${state}-thumb`, thumbStyle));
      // Thumb geometry in the track's padding box, where DSH positions its thumb.
      tokens[`--dsh-work-switch-${state}-thumb-left`] = `${(thumbBox.left - trackBox.left - inset).toFixed(2)}px`;
      tokens[`--dsh-work-switch-${state}-thumb-top`] = `${(thumbBox.top - trackBox.top - insetTop).toFixed(2)}px`;
      if (state === "off") {
        tokens["--dsh-work-switch-w"] = `${trackBox.width.toFixed(2)}px`;
        tokens["--dsh-work-switch-h"] = `${trackBox.height.toFixed(2)}px`;
        tokens["--dsh-work-switch-thumb-size"] = `${thumbBox.width.toFixed(2)}px`;
      }
    }
    for (const name of ["button", "primary"] as const) {
      const sample = style(get(name));
      Object.assign(tokens, surface(`--dsh-work-${name}`, sample));
      tokens[`--dsh-work-${name}-fg`] = sample.color;
      tokens[`--dsh-work-${name}-weight`] = sample.fontWeight;
    }
    tokens["--dsh-work-button-hover-bg"] = read("--button-hover-bg");
    tokens["--dsh-work-primary-hover-bg"] = read("--primary-hover-bg");
    for (const name of ["input", "select", "tag"] as const) Object.assign(tokens, surface(`--dsh-work-${name}`, style(get(name))));
    tokens["--dsh-work-tag-fg"] = style(get("tag")).color;
    tokens["--dsh-work-check"] = read("--accent");
    // Page titles and section headings take the theme's heading type.
    for (const name of ["title", "section"] as const) {
      const heading = style(get(name));
      tokens[`--dsh-work-${name}-font`] = heading.fontFamily;
      tokens[`--dsh-work-${name}-weight`] = heading.fontWeight;
      tokens[`--dsh-work-${name}-tracking`] = heading.letterSpacing === "normal" ? "0px" : heading.letterSpacing;
    }
    // Chosen answers read as dsh-work's selected rows and done markers.
    tokens["--dsh-work-selected-bg"] = read("--selection");
    tokens["--dsh-work-selected-fg"] = read("--selection-fg") === "inherit" ? read("--text") : read("--selection-fg");
    // The marker keeps the theme's done fill; its digit takes the first theme colour
    // that reads on that fill (a theme's own pair is sized for icons, not 12px digits).
    const resolve = (value: string) => {
      host.style.color = "";
      host.style.color = value;
      return parseColor(getComputedStyle(host).color);
    };
    const markerFill = resolve(read("--marker-done-bg"));
    const markerText = ["--marker-done-fg", "--on-accent", "--text", "--canvas"].map(read)
      .find(value => { const ink = resolve(value); return !markerFill || !ink || contrastRatio(ink, markerFill) >= 4.5; });
    tokens["--dsh-work-marker-bg"] = read("--marker-done-bg");
    tokens["--dsh-work-marker-fg"] = markerText ?? read("--marker-done-fg");
    // No theme colour reads on the fill: keep the theme's marker text and deepen the fill until it does.
    const ink = resolve(read("--marker-done-fg"));
    if (!markerText && markerFill && ink) {
      let fill = markerFill;
      for (let i = 0; i < 20 && contrastRatio(ink, fill) < 4.5; i++) {
        // Light text needs a darker fill; dark text a lighter one.
        const lightInk = contrastRatio(ink, [0, 0, 0, 1]) > contrastRatio(ink, [255, 255, 255, 1]);
        fill = fill.map((channel, index) => index === 3 ? 1 : lightInk ? channel * 0.9 : channel + (255 - channel) * 0.1) as Rgba;
      }
      tokens["--dsh-work-marker-bg"] = `rgb(${fill.slice(0, 3).map(Math.round).join(", ")})`;
    }
    tokens["--dsh-work-option-bg"] = read("--canvas");
    tokens["--dsh-work-option-fg"] = read("--text");
  } finally {
    host.remove();
    still.remove();
  }
  // A value the browser resolved to an empty string is left to DSH.
  for (const [name, value] of Object.entries(tokens)) if (!value) delete tokens[name];
  return tokens;
}
