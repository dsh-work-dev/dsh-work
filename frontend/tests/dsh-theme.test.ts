import assert from "node:assert/strict";
import test from "node:test";

import {controlTokenProperty} from "../src/dsh-controls";
import {dshDecorTokens, dshThemeTokens, pageFrame, parseColor, windowFrame, type Border} from "../src/dsh-theme";

const isColor = (property: string, value: string) => property !== "color" || !/gradient|url\(/.test(value);

test("DSH tokens come from the dsh-work theme, falling back past non-colour values", () => {
  const theme: Record<string, string> = {
    "--canvas": "#ffffff", "--text": "#0a0a0a", "--muted": "#616161", "--accent": "#6e3df5",
    "--primary-hover-bg": "linear-gradient(135deg, #5a38e0, #1b6ab5)",
  };
  const tokens = dshThemeTokens(name => theme[name] ?? "", isColor);
  assert.equal(tokens["--dsw-alias-bg-base"], "rgb(255, 255, 255)");
  assert.equal(tokens["--dsw-alias-bg-layer-1"], "rgb(255, 255, 255)", "a missing group background falls back to the canvas");
  assert.equal(tokens["--dsw-alias-button-primary-hover"], "#6e3df5", "a gradient hover falls back to the accent");
  assert.equal(tokens["--dsw-alias-label-secondary"], "rgb(64, 64, 64)", "secondary text sits between body text and the surface");
  assert.equal("--dsw-alias-state-error-primary" in tokens, false, "a token without a usable source keeps DSH's value");
  const ratio = (value: string) => { const [r, g, b] = value.match(/\d+/g)!.map(Number); const lum = (c: number) => { c /= 255; return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4; }; const l = 0.2126 * lum(r) + 0.7152 * lum(g) + 0.0722 * lum(b); return 1.05 / (l + 0.05); };
  assert.ok(ratio(tokens["--dsw-alias-link"]) >= 4.5, "a light accent used as text is darkened until it reads");
  assert.ok(Object.keys(tokens).every(name => /^--dsw-[a-z0-9-]+$/.test(name)));
});

test("fills under DSH's white glyphs stay readable when the accent is light", () => {
  const tokens = dshThemeTokens(name => ({"--accent": "#f2f2f2", "--on-ink": "#0b0b0b"})[name] ?? "", isColor);
  const [r, g, b] = tokens["--dsw-alias-button-info-fill"].match(/\d+/g)!.map(Number);
  assert.ok(r < 120 && r === g && g === b, tokens["--dsw-alias-button-info-fill"]);
  assert.equal(tokens["--dsw-alias-label-primary-inverted"], "#0b0b0b");
  const dark = dshThemeTokens(name => ({"--accent": "#0969da"})[name] ?? "", isColor);
  assert.equal(dark["--dsw-alias-button-info-fill"], "rgb(9, 105, 218)");
});

test("DSH body text sits on a surface the theme's text reads on", () => {
  const classic: Record<string, string> = {"--text": "#000000", "--canvas": "#008080", "--group-bg": "#c0c0c0", "--nav-bg": "#c0c0c0"};
  const tokens = dshThemeTokens(name => classic[name] ?? "", isColor);
  assert.equal(tokens["--dsw-alias-bg-base"], "rgb(192, 192, 192)", "the teal desktop is skipped");
  const bauhaus: Record<string, string> = {"--text": "#111111", "--canvas": "#f4efe6", "--nav-bg": "#111111", "--wash": "#ebe4d6"};
  assert.equal(dshThemeTokens(name => bauhaus[name] ?? "", isColor)["--dsw-specific-sidebar-fill"], "rgb(235, 228, 214)");
});

test("the primary button foreground pairs with the fill DSH receives", () => {
  const glass: Record<string, string> = {"--primary-bg": "linear-gradient(135deg, #7c5cff, #2b8cd6)", "--primary-fg": "#0b0d1c", "--accent": "#a78bfa", "--on-accent": "#ffffff"};
  const tokens = dshThemeTokens(name => glass[name] ?? "", isColor);
  const fill = tokens["--dsw-alias-button-primary-fill"].match(/\d+/g)!.map(Number);
  assert.ok(fill[0] < 0xa7, "a light accent under a white label is darkened");
  assert.equal(tokens["--dsw-alias-label-primary-foreground"], "#ffffff");
});

test("badge text pairs with the badge fill DSH receives", () => {
  // Body text does not read on Classic's navy selection, so badges take the quiet fill.
  const classic: Record<string, string> = {"--selection": "#000080", "--wash": "#ffffff", "--accent": "#000080", "--selection-fg": "#ffffff", "--text": "#000000"};
  const tokens = dshThemeTokens(name => classic[name] ?? "", isColor);
  assert.equal(tokens["--dsw-alias-state-business-tertiary"], "rgb(255, 255, 255)");
  assert.equal(tokens["--dsw-alias-label-primary-bluish"], "#000080");
  const github: Record<string, string> = {"--selection": "#ddf4ff", "--accent": "#0969da", "--text": "#1f2328"};
  assert.equal(dshThemeTokens(name => github[name] ?? "", isColor)["--dsw-alias-label-primary-bluish"], "#0969da");
});

test("shapes, shadows and the interface font follow the theme", () => {
  const theme: Record<string, string> = {"--radius-tag": "0", "--radius-control": "10px", "--radius-group": "14px", "--group-shadow": "4px 4px 0 #111111", "--dialog-shadow": "8px 8px 0 #111111", "--font-body": "Tahoma, sans-serif", "--font-mono": "\"Cascadia Mono\", monospace"};
  const tokens = dshThemeTokens(name => theme[name] ?? "", isColor);
  assert.equal(tokens["--dsw-radius-sm"], "10px");
  assert.equal(tokens["--dsw-radius-panel"], "14px");
  assert.equal(tokens["--dsw-shadow-lv3"], "8px 8px 0 #111111");
  assert.equal(tokens["--dsw-font-family"], "Tahoma, sans-serif");
  assert.equal(tokens["--dsw-font-family-brand"], "Tahoma, sans-serif");
  assert.equal(tokens["--dsw-font-mono"], "\"Cascadia Mono\", Tahoma, monospace", "CJK in DSH's mono rows takes the interface font, not a serif fallback");
});

test("theme borders and shadows become DSH's raised and floating decoration", () => {
  const brutal: Record<string, string> = {"--group-shadow": "6px 6px 0 #111111", "--dialog-shadow": "8px 8px 0 #111111"};
  const borders: Record<string, Border> = {"--group-border": {width: "3px", color: "rgb(17, 17, 17)"}, "--dialog-border": {width: "3px", color: "rgb(17, 17, 17)"}};
  const tokens = dshDecorTokens(name => brutal[name] ?? "", name => borders[name]);
  assert.equal(tokens["--dsh-work-raised"], "0 0 0 3px rgb(17, 17, 17), 6px 6px 0 #111111");
  assert.equal(tokens["--dsh-work-floating"], "0 0 0 3px rgb(17, 17, 17), 8px 8px 0 #111111");
});

test("on a theme whose groups lie flat, DSH's raised surfaces keep the theme's floating frame", () => {
  const claude: Record<string, string> = {"--group-shadow": "none", "--dialog-shadow": "0 2px 4px rgb(11 11 11 / 7%)"};
  const borders: Record<string, Border> = {"--input-border": {width: "1px", color: "rgba(11, 11, 11, 0.1)"}, "--dialog-border": {width: "1px", color: "rgba(11, 11, 11, 0.05)"}};
  const tokens = dshDecorTokens(name => claude[name] ?? "", name => borders[name]);
  assert.equal(tokens["--dsh-work-raised"], "0 0 0 1px rgba(11, 11, 11, 0.05), 0 2px 4px rgb(11 11 11 / 7%)");
  assert.equal(tokens["--dsh-work-stroke"], "0 0 0 1px rgba(11, 11, 11, 0.05)");
  assert.equal(tokens["--dsh-work-composer"], "0 0 0 1px rgba(11, 11, 11, 0.1), 0 2px 4px rgb(11 11 11 / 7%)", "the composer is framed as the theme's inputs");
  assert.equal(tokens["--dsh-work-group-border"], "0 solid transparent", "lists drawn as the theme's groups stay flat");
  const bare = dshDecorTokens(name => (name === "--group-shadow" ? "none" : ""), () => undefined);
  assert.equal(bare["--dsh-work-raised"], "none");
  assert.equal(bare["--dsh-work-stroke"], "0 0 0 0 transparent");
});

test("translucent theme fills become opaque DSH surfaces", () => {
  const terminal: Record<string, string> = {"--canvas": "#0c0f0c", "--text": "#c8f7c5", "--group-bg": "transparent", "--wash": "rgb(200 247 197 / 6%)"};
  const resolve = (value: string) => value === "transparent" ? [0, 0, 0, 0] as [number, number, number, number] : parseColor(value);
  const tokens = dshThemeTokens(name => terminal[name] ?? "", isColor, resolve);
  assert.equal(tokens["--dsw-alias-bg-layer-3"], "rgb(12, 15, 12)", "a transparent group sits on the canvas");
  assert.equal(tokens["--dsw-alias-bg-overlay"], "rgb(23, 29, 23)", "a translucent wash is composited");
});

test("control values are checked against the property they feed", () => {
  assert.equal(controlTokenProperty("--dsh-work-switch-on-track-border"), "border");
  assert.equal(controlTokenProperty("--dsh-work-switch-off-thumb-left"), "width");
  assert.equal(controlTokenProperty("--dsh-work-button-image"), "background-image");
  assert.equal(controlTokenProperty("--dsh-work-primary-fg"), "color");
});

test("dsh-work chooses the window frame and gives DSH's sidebar the same fill", () => {
  const theme: Record<string, string> = {"--canvas": "#fff4e0", "--text": "#111111", "--nav-bg": "#ffd23f", "--nav-fg": "#111111", "--nav-hover-bg": "#ffe27a", "--nav-active-bg": "#111111", "--nav-active-fg": "#ffd23f"};
  const read = (name: string) => theme[name] ?? "";
  const chrome = windowFrame(read);
  assert.deepEqual(chrome, {fill: "rgb(255, 210, 63)", text: "#111111", hover: "rgb(255, 226, 122)", active: "rgb(17, 17, 17)", activeText: "#ffd23f"}, "a navigation frame keeps the theme's navigation states");
  assert.equal(dshThemeTokens(read, isColor)["--dsw-specific-sidebar-fill"], chrome!.fill);
});

test("the DSH light frame follows its native sidebar and states", () => {
  const light: Record<string, string> = {
    "--text": "#0f1115", "--nav-bg": "#f9fafb", "--nav-fg": "#0f1115",
    "--nav-hover-bg": "#f1f3f5", "--nav-active-bg": "#ebeef2", "--nav-active-fg": "#0f1115",
  };
  assert.deepEqual(windowFrame(name => light[name] ?? ""), {
    fill: "rgb(249, 250, 251)", text: "#0f1115", hover: "rgb(241, 243, 245)",
    active: "rgb(235, 238, 242)", activeText: "#0f1115",
  });
});

test("the DSH dark frame follows its native sidebar and states", () => {
  const dark: Record<string, string> = {
    "--text": "#f9fafb", "--nav-bg": "#1b1b1c", "--nav-fg": "#f9fafb",
    "--nav-hover-bg": "#2c2c2e", "--nav-active-bg": "#43454a", "--nav-active-fg": "#f9fafb",
  };
  assert.deepEqual(windowFrame(name => dark[name] ?? ""), {
    fill: "rgb(27, 27, 28)", text: "#f9fafb", hover: "rgb(44, 44, 46)",
    active: "rgb(67, 69, 74)", activeText: "#f9fafb",
  });
});

test("a frame that is not the navigation fill does not take the navigation states", () => {
  const bauhaus: Record<string, string> = {"--canvas": "#f4efe6", "--text": "#111111", "--nav-bg": "#111111", "--nav-fg": "#ffffff", "--nav-hover-bg": "#2a2a2a", "--nav-active-bg": "#f5c518", "--nav-active-fg": "#121212", "--wash": "#ebe4d6", "--hover": "#e6e0d2", "--selection": "#f5c518", "--selection-fg": "#121212"};
  assert.deepEqual(windowFrame(name => bauhaus[name] ?? ""), {fill: "rgb(235, 228, 214)", text: "#111111", hover: "rgb(218, 211, 198)", active: "rgb(245, 197, 24)", activeText: "#121212"}, "a fill body text cannot read on falls to a quieter one, with the general selection; a hover that does not stand out from it becomes a tint");
});

test("startup title bar text and button states read on the page canvas", () => {
  const classic: Record<string, string> = {"--canvas": "#008080", "--text": "#000000", "--on-ink": "#ffffff", "--hover": "#d4d0c8", "--selection": "#000080", "--selection-fg": "#ffffff"};
  const page = pageFrame(name => classic[name] ?? "");
  assert.equal(page.text, "#ffffff");
  assert.equal(page.hover, "rgb(0, 118, 118)", "a hover the bar's text cannot read on becomes a tint away from that text, which keeps it readable");
  assert.deepEqual([page.active, page.activeText], ["rgb(0, 0, 128)", "#ffffff"]);
  const brutal: Record<string, string> = {"--canvas": "#fff4e0", "--text": "#111111", "--selection": "#fff08a", "--selection-fg": "inherit"};
  const plain = pageFrame(name => brutal[name] ?? "");
  assert.equal(plain.text, "#111111");
  assert.deepEqual([plain.active, plain.activeText], ["rgb(255, 240, 138)", "#111111"], "a label that names no colour leaves the bar's text");
  assert.equal(plain.hover, "rgb(236, 226, 207)", "a theme without a hover tints the canvas with its text");
});
