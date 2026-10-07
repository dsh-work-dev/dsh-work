import {Events} from "@wailsio/runtime";

import type {Appearance} from "../bindings/github.com/local/dsh-work/internal/settings/models";
import {supportedMode, themeInfo} from "./themes";

export type AppearanceMode = "system" | "light" | "dark";

// dsh-work owns the appearance of its own windows; the Host persists it and
// publishes "appearance" after every saved change. The copy in localStorage is
// only a paint cache read by index.html before scripts load.
const cacheKey = "dsh-work.appearance";
let current: {theme: string; mode: AppearanceMode} = {theme: "monochrome", mode: "system"};
let systemMedia: MediaQueryList | undefined;
let mounted = false;

type AppearanceInput = {theme?: string; mode?: string} | undefined;

function normalize(value: AppearanceInput): {theme: string; mode: AppearanceMode} {
  const theme = themeInfo(value?.theme).id;
  return {theme, mode: supportedMode(theme, value?.mode)};
}

function resolved(mode: AppearanceMode): "light" | "dark" {
  return mode === "system" ? (systemMedia?.matches ? "dark" : "light") : mode;
}

/** Paint an appearance on this document and remember it for the next load. */
export function applyAppearance(value: AppearanceInput) {
  current = normalize(value);
  const root = document.documentElement;
  const theme = resolved(current.mode);
  root.dataset.themeId = current.theme;
  root.dataset.themePreference = current.mode;
  root.dataset.theme = theme;
  root.style.colorScheme = theme;
  try { localStorage.setItem(cacheKey, JSON.stringify(current)); } catch { /* cache only */ }
}

export function currentAppearance() {
  return {...current};
}

/**
 * Follow the Host appearance: read it once, apply published changes, and
 * re-resolve "system" when the operating system switches light/dark.
 */
export function mountAppearance(read: () => Promise<Appearance>) {
  if (!systemMedia && typeof window.matchMedia === "function") {
    systemMedia = window.matchMedia("(prefers-color-scheme: dark)");
  }
  let cached: AppearanceInput;
  try { cached = JSON.parse(localStorage.getItem(cacheKey) ?? "null") ?? undefined; } catch { cached = undefined; }
  applyAppearance(cached);
  if (mounted) return;
  mounted = true;
  systemMedia?.addEventListener("change", () => {
    if (current.mode === "system") applyAppearance(current);
  });
  // A published change is newer than an initial read still in flight.
  let published = false;
  Events.On("appearance", event => { published = true; applyAppearance(event.data as Appearance); });
  void read()
    .then(value => { if (!published) applyAppearance(value); })
    .catch(error => console.error("Could not read dsh-work appearance", error));
}
