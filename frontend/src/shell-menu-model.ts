// Shell menu bar content as data: host state and the DSH catalog in, menus out.

export const dshMenuCommands: readonly string[] = [
  "session.new", "session.search", "workspace.add", "terminal.new", "browser.new",
  "sidebar.left.toggle", "sidebar.right.toggle", "settings.open", "shortcuts.open",
];

export interface DshCommand { id: string; keys: string[]; bound: boolean }

export interface MenuState {
  lifecycle: string;
  busy: boolean;
  pet: {ready: boolean; visible: boolean};
  /** Null until the environment snapshot is read. */
  safeMode: {available: boolean; active: boolean} | null;
  updatePhase: string;
  /** The workbench window's state; zoom is the WebView factor, 1 at actual size. */
  window: {fullscreen: boolean; zoom: number};
  /** Whether the DSH document is loaded in the frame. */
  framed: boolean;
  /** Reported by the DSH plugin; null until DSH is ready. */
  dsh: Map<string, DshCommand> | null;
}

export type SettingsSection = "overview" | "settings" | "notifications" | "pets" | "runtimes" | "profiles" | "plugins" | "data-directories" | "about";
export type WindowAction = "close" | "zoom-in" | "zoom-out" | "zoom-reset" | "fullscreen";
export type LinkTarget = "docs" | "feedback-desktop" | "feedback-dsh";
export type MenuAction =
  | {kind: "settings"; section: SettingsSection} | {kind: "window"; action: WindowAction} | {kind: "link"; target: LinkTarget}
  | {kind: "pet"} | {kind: "refresh"} | {kind: "restart"} | {kind: "safe-mode"} | {kind: "quit"}
  | {kind: "update"} | {kind: "about"} | {kind: "diagnostics"} | {kind: "devtools"} | {kind: "dsh"; id: string};
export interface MenuItem { type: "item"; key: string; label: string; enabled: boolean; checked?: boolean; keys?: string[]; hint?: string; action: MenuAction }
export interface MenuSeparator { type: "separator" }
export type MenuEntry = MenuItem | MenuSeparator;
export type MenuId = "file" | "view" | "run" | "settings" | "help";
export interface Menu { id: MenuId; label: string; badge: boolean; items: MenuEntry[] }

const updating = new Set(["checking", "downloading", "verifying", "installing"]);
const separator: MenuSeparator = {type: "separator"};

function updateLabelKey(phase: string): string {
  if (phase === "ready") return "shell.menu.updateAvailable";
  return updating.has(phase) ? "shell.menu.updating" : "shell.menu.checkUpdates";
}

export function buildMenus(state: MenuState, t: (key: string) => string): Menu[] {
  const restartable = !state.busy && state.lifecycle !== "Starting" && state.lifecycle !== "Stopping";
  const quittable = !state.busy && state.lifecycle !== "Stopping";
  const host = (key: string, action: MenuAction, enabled = true): MenuItem => ({type: "item", key, label: t(`shell.menu.${key}`), enabled, action});
  const settings = (section: SettingsSection, label: string): MenuItem => ({
    type: "item", key: `settings.${section}`, label: t(`manager.${label}`), enabled: true,
    action: {kind: "settings", section},
  });
  const dsh = (id: string): MenuItem => {
    const command = state.dsh?.get(id);
    return {
      type: "item", key: id, label: t(`shell.command.${id}`), enabled: command?.bound === true,
      keys: command?.bound ? command.keys : undefined, hint: command && !command.bound ? t("shell.menu.unbound") : undefined,
      action: {kind: "dsh", id},
    };
  };
  return [
    {id: "file", label: t("shell.menu.file"), badge: false, items: [
      dsh("session.new"), dsh("session.search"), dsh("workspace.add"),
      separator,
      dsh("terminal.new"), dsh("browser.new"),
      separator,
      host("closeWindow", {kind: "window", action: "close"}),
      host("quit", {kind: "quit"}, quittable),
    ]},
    {id: "view", label: t("shell.menu.view"), badge: false, items: [
      dsh("sidebar.left.toggle"), dsh("sidebar.right.toggle"),
      separator,
      host("zoomIn", {kind: "window", action: "zoom-in"}),
      // WebView2 zoom in Wails stops at actual size, so there is nothing below it.
      host("zoomOut", {kind: "window", action: "zoom-out"}, state.window.zoom > 1),
      host("zoomReset", {kind: "window", action: "zoom-reset"}, state.window.zoom !== 1),
      {...host("fullscreen", {kind: "window", action: "fullscreen"}), checked: state.window.fullscreen},
      separator,
      {...host("showPet", {kind: "pet"}, !state.busy && state.pet.ready), checked: state.pet.visible},
    ]},
    {id: "run", label: t("shell.menu.run"), badge: false, items: [
      host("refresh", {kind: "refresh"}, state.framed),
      host("restart", {kind: "restart"}, restartable),
      separator,
      {...host(state.safeMode?.active ? "exitSafeMode" : "enterSafeMode", {kind: "safe-mode"}, restartable && state.safeMode?.available === true), key: "safeMode"},
    ]},
    {id: "settings", label: t("shell.menu.settings"), badge: false, items: [
      settings("overview", "overview"),
      separator,
      settings("settings", "general"), settings("notifications", "notifications"), settings("pets", "pets"),
      separator,
      settings("runtimes", "runtimes"), settings("profiles", "profiles"), settings("plugins", "plugins"),
      separator,
      settings("data-directories", "dataDirectories"),
      separator,
      dsh("settings.open"),
    ]},
    {id: "help", label: t("shell.menu.help"), badge: state.updatePhase === "ready", items: [
      host("docs", {kind: "link", target: "docs"}), dsh("shortcuts.open"),
      separator,
      host("feedbackDesktop", {kind: "link", target: "feedback-desktop"}),
      host("feedbackDsh", {kind: "link", target: "feedback-dsh"}),
      separator,
      host("copyDiagnostics", {kind: "diagnostics"}),
      host("devtools", {kind: "devtools"}),
      separator,
      {...host("checkUpdates", {kind: "update"}), key: "update", label: t(updateLabelKey(state.updatePhase))},
      host("about", {kind: "about"}),
    ]},
  ];
}

/** Next item for arrow-key navigation; separators are skipped and focus wraps. */
export function stepItem(items: readonly MenuEntry[], from: number, delta: 1 | -1): number {
  const count = items.length;
  for (let step = 1; step <= count; step++) {
    const index = (((from + delta * step) % count) + count) % count;
    if (items[index].type === "item") return index;
  }
  return from;
}

/** DSH's readable keys already carry their separators ("Ctrl", "+", "B" on Windows; "⌃", "B" on macOS). */
export function formatKeys(keys: readonly string[] | undefined): string {
  return keys?.join("") ?? "";
}
