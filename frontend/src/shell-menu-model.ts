// Shell menu bar content as data: host state and the DSH catalog in, menus out.

export const sessionCommands = ["session.new", "workspace.add", "terminal.new", "browser.new"] as const;
export const viewCommands = ["sidebar.left.toggle", "sidebar.right.toggle", "shortcuts.open", "settings.open"] as const;
export const dshMenuCommands: readonly string[] = [...sessionCommands, ...viewCommands];

export interface DshCommand { id: string; keys: string[]; bound: boolean }

export interface MenuState {
  lifecycle: string;
  busy: boolean;
  pet: {ready: boolean; visible: boolean};
  updatePhase: string;
  /** Reported by the DSH plugin; null until DSH is ready. */
  dsh: Map<string, DshCommand> | null;
}

export type MenuAction = {kind: "settings"} | {kind: "pet"} | {kind: "restart"} | {kind: "quit"} | {kind: "update"} | {kind: "about"} | {kind: "dsh"; id: string};
export interface MenuItem { type: "item"; key: string; label: string; enabled: boolean; checked?: boolean; keys?: string[]; hint?: string; action: MenuAction }
export interface MenuSeparator { type: "separator" }
export type MenuEntry = MenuItem | MenuSeparator;
export type MenuId = "app" | "session" | "view" | "help";
export interface Menu { id: MenuId; label: string; badge: boolean; items: MenuEntry[] }

const updating = new Set(["checking", "downloading", "verifying", "installing"]);

function updateLabelKey(phase: string): string {
  if (phase === "ready") return "shell.menu.updateAvailable";
  return updating.has(phase) ? "shell.menu.updating" : "shell.menu.checkUpdates";
}

export function buildMenus(state: MenuState, t: (key: string) => string): Menu[] {
  const restartable = !state.busy && state.lifecycle !== "Starting" && state.lifecycle !== "Stopping";
  const quittable = !state.busy && state.lifecycle !== "Stopping";
  const dsh = (id: string): MenuItem => {
    const command = state.dsh?.get(id);
    return {
      type: "item", key: id, label: t(`shell.command.${id}`), enabled: command?.bound === true,
      keys: command?.bound ? command.keys : undefined, hint: command && !command.bound ? t("shell.menu.unbound") : undefined,
      action: {kind: "dsh", id},
    };
  };
  return [
    {id: "app", label: t("shell.menu.app"), badge: false, items: [
      {type: "item", key: "settings", label: t("shell.menu.settings"), enabled: true, action: {kind: "settings"}},
      {type: "item", key: "pet", label: t("shell.menu.showPet"), enabled: !state.busy && state.pet.ready, checked: state.pet.visible, action: {kind: "pet"}},
      {type: "separator"},
      {type: "item", key: "restart", label: t("shell.menu.restart"), enabled: restartable, action: {kind: "restart"}},
      {type: "item", key: "quit", label: t("shell.menu.quit"), enabled: quittable, action: {kind: "quit"}},
    ]},
    {id: "session", label: t("shell.menu.session"), badge: false, items: sessionCommands.map(dsh)},
    {id: "view", label: t("shell.menu.view"), badge: false, items: viewCommands.map(dsh)},
    {id: "help", label: t("shell.menu.help"), badge: state.updatePhase === "ready", items: [
      {type: "item", key: "update", label: t(updateLabelKey(state.updatePhase)), enabled: true, action: {kind: "update"}},
      {type: "item", key: "about", label: t("shell.menu.about"), enabled: true, action: {kind: "about"}},
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
