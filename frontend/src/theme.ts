export type Theme = "system" | "light" | "dark";

let activePreference: Theme = "system";
let systemMedia: MediaQueryList | undefined;
let systemListenerMounted = false;

function normalizeTheme(value: unknown): Theme {
  return value === "light" || value === "dark" || value === "system" ? value : "system";
}

function resolvedTheme(preference: Theme): "light" | "dark" {
  return preference === "system"
    ? (systemMedia?.matches ? "dark" : "light")
    : preference;
}

/** Apply the appearance preference owned by the selected DSH home. */
export function applyTheme(value: unknown) {
  const preference = normalizeTheme(value);
  activePreference = preference;
  const resolved = resolvedTheme(preference);
  const root = document.documentElement;
  root.dataset.themePreference = preference;
  root.dataset.theme = resolved;
  root.style.colorScheme = resolved;
}

/**
 * Work has no appearance setting of its own. The host asks DSH for its
 * preference, then this listener only resolves the DSH `system` choice when
 * the operating system changes.
 */
export function mountTheme(initialPreference: unknown = "system") {
  if (!systemMedia && typeof window.matchMedia === "function") {
    systemMedia = window.matchMedia("(prefers-color-scheme: dark)");
  }
  applyTheme(initialPreference);
  if (!systemListenerMounted && typeof systemMedia?.addEventListener === "function") {
    systemMedia.addEventListener("change", () => {
      if (activePreference === "system") {
        applyTheme("system");
      }
    });
    systemListenerMounted = true;
  }
}
