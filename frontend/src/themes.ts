// Built-in themes and the light/dark modes each supports. Mirrors
// internal/settings/appearance.go, which rejects anything not listed there.
// Styles live in src/ui/themes/<id>.css; monochrome is the token defaults.
export type ThemeMode = "system" | "light" | "dark";

export interface ThemeInfo {
  id: string;
  label: string;
  modes: readonly ThemeMode[];
}

export const themes: readonly ThemeInfo[] = [
  {id: "monochrome", label: "theme.monochrome", modes: ["system", "light", "dark"]},
  {id: "chatgpt", label: "theme.chatgpt", modes: ["system", "light", "dark"]},
  {id: "claude", label: "theme.claude", modes: ["system", "light", "dark"]},
  {id: "github", label: "theme.github", modes: ["system", "light", "dark"]},
  {id: "lobehub", label: "theme.lobehub", modes: ["system", "light", "dark"]},
  {id: "soft", label: "theme.soft", modes: ["system", "light", "dark"]},
  {id: "swiss", label: "theme.swiss", modes: ["system", "light", "dark"]},
  {id: "paper", label: "theme.paper", modes: ["system", "light", "dark"]},
  {id: "glass", label: "theme.glass", modes: ["system", "light", "dark"]},
  {id: "ink", label: "theme.ink", modes: ["light"]},
  {id: "classic", label: "theme.classic", modes: ["light"]},
  {id: "terminal", label: "theme.terminal", modes: ["dark"]},
  {id: "neon", label: "theme.neon", modes: ["dark"]},
  {id: "brutal", label: "theme.brutal", modes: ["light"]},
  {id: "bauhaus", label: "theme.bauhaus", modes: ["light"]},
  {id: "deco", label: "theme.deco", modes: ["dark"]},
];

export function themeInfo(id: string | undefined): ThemeInfo {
  return themes.find(theme => theme.id === id) ?? themes[0];
}

/** Keep a mode when the theme supports it, otherwise use the theme's first mode. */
export function supportedMode(id: string | undefined, mode: string | undefined): ThemeMode {
  const modes = themeInfo(id).modes;
  return modes.includes(mode as ThemeMode) ? mode as ThemeMode : modes[0];
}
