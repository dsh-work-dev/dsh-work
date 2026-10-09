import {buildMenus, formatKeys, stepItem, type Menu, type MenuAction, type MenuEntry, type MenuId, type MenuItem, type MenuState} from "./shell-menu-model";
import {icon} from "./ui/icons";

export interface MenuBar {
  element: HTMLElement;
  render(state: MenuState): void;
  /** Focus the menu bar from the keyboard (Alt or F10). */
  focus(): void;
}

/** ARIA menu bar drawn in the shell document; its menus overlay the DSH frame. */
export function createMenuBar(t: (key: string) => string, run: (action: MenuAction) => void, onOpen: (id: MenuId) => void, onLeave: () => void): MenuBar {
  const bar = document.createElement("div");
  bar.className = "shell-menubar";
  bar.setAttribute("role", "menubar");
  let menus: Menu[] = [];
  let openIndex = -1;
  let popup: HTMLElement | null = null;
  const buttons = () => Array.from(bar.querySelectorAll<HTMLButtonElement>(".shell-menu-button"));
  const rows = () => (popup ? Array.from(popup.querySelectorAll<HTMLButtonElement>(".shell-menu-item")) : []);
  const focusRow = (index: number) => rows().find(row => Number(row.dataset.index) === index)?.focus();
  const focusedRow = () => {
    const active = document.activeElement as HTMLElement | null;
    return popup?.contains(active) ? Number(active?.dataset.index ?? -1) : -1;
  };

  const close = (refocus: boolean) => {
    const index = openIndex;
    popup?.remove();
    popup = null;
    openIndex = -1;
    buttons().forEach(button => button.setAttribute("aria-expanded", "false"));
    if (refocus && index >= 0) buttons()[index]?.focus();
  };
  const activate = (item: MenuItem) => {
    if (!item.enabled) return;
    close(false);
    run(item.action);
  };
  const row = (entry: MenuEntry, index: number): HTMLElement => {
    if (entry.type === "separator") {
      const separator = document.createElement("div");
      separator.className = "shell-menu-separator";
      separator.setAttribute("role", "separator");
      return separator;
    }
    const button = document.createElement("button");
    button.type = "button";
    button.className = "shell-menu-item";
    button.tabIndex = -1;
    button.dataset.index = String(index);
    button.setAttribute("role", entry.checked === undefined ? "menuitem" : "menuitemcheckbox");
    if (entry.checked !== undefined) button.setAttribute("aria-checked", String(entry.checked));
    if (!entry.enabled) button.setAttribute("aria-disabled", "true");
    const label = document.createElement("span");
    label.className = "shell-menu-label";
    label.textContent = entry.label;
    // Trailing slot: the check mark for toggles, otherwise the shortcut or why it is off.
    const trailing = document.createElement("span");
    trailing.className = "shell-menu-trailing";
    if (entry.checked) {
      trailing.classList.add("shell-menu-checked");
      trailing.append(icon("check"));
    }
    else trailing.textContent = entry.hint ?? formatKeys(entry.keys);
    button.append(label, trailing);
    button.addEventListener("click", () => activate(entry));
    button.addEventListener("pointerenter", () => button.focus());
    return button;
  };
  const onMenuKey = (event: KeyboardEvent) => {
    const items = menus[openIndex].items;
    const current = focusedRow();
    const count = menus.length;
    if (event.key === "ArrowDown") focusRow(stepItem(items, current, 1));
    else if (event.key === "ArrowUp") focusRow(stepItem(items, current < 0 ? 0 : current, -1));
    else if (event.key === "Home") focusRow(stepItem(items, -1, 1));
    else if (event.key === "End") focusRow(stepItem(items, 0, -1));
    else if (event.key === "ArrowRight") show((openIndex + 1) % count, true);
    else if (event.key === "ArrowLeft") show((openIndex - 1 + count) % count, true);
    else if (event.key === "Escape") close(true);
    else if (event.key === "Tab") close(false);
    else if (event.key === "Enter" || event.key === " ") {
      const item = items[current];
      if (item?.type === "item") activate(item);
    } else return;
    event.preventDefault();
  };
  const open = (index: number, focusFirst: boolean) => {
    const keep = openIndex === index ? focusedRow() : -1;
    popup?.remove();
    openIndex = index;
    const menu = menus[index];
    buttons().forEach((button, i) => button.setAttribute("aria-expanded", String(i === index)));
    const next = document.createElement("div");
    next.className = "shell-menu";
    next.setAttribute("role", "menu");
    next.setAttribute("aria-label", menu.label);
    menu.items.forEach((entry, i) => next.append(row(entry, i)));
    const rect = buttons()[index].getBoundingClientRect();
    next.style.left = `${rect.left}px`;
    next.style.top = `${rect.bottom}px`;
    next.addEventListener("keydown", onMenuKey);
    document.body.append(next);
    popup = next;
    if (keep >= 0) focusRow(keep);
    else if (focusFirst) focusRow(stepItem(menu.items, -1, 1));
  };
  const show = (index: number, focusFirst: boolean) => {
    onOpen(menus[index].id);
    open(index, focusFirst);
  };

  bar.addEventListener("keydown", event => {
    const index = buttons().indexOf(event.target as HTMLButtonElement);
    if (index < 0) return;
    const count = menus.length;
    if (event.key === "ArrowRight" || event.key === "ArrowLeft") {
      const next = (index + (event.key === "ArrowRight" ? 1 : count - 1)) % count;
      buttons()[next].focus();
      if (openIndex >= 0) show(next, false);
    } else if (event.key === "ArrowDown" || event.key === "Enter" || event.key === " ") show(index, true);
    else if (event.key === "Escape") {
      close(false);
      onLeave();
    } else return;
    event.preventDefault();
  });
  document.addEventListener("pointerdown", event => {
    if (openIndex >= 0 && !bar.contains(event.target as Node) && !popup?.contains(event.target as Node)) close(false);
  }, true);
  // Focus moving into the DSH frame blurs the shell window.
  window.addEventListener("blur", () => close(false));

  const render = (state: MenuState) => {
    menus = buildMenus(state, t);
    bar.setAttribute("aria-label", t("shell.menu.bar"));
    if (buttons().length !== menus.length) {
      bar.replaceChildren(...menus.map((_, index) => {
        const button = document.createElement("button");
        button.type = "button";
        button.className = "shell-menu-button";
        button.setAttribute("role", "menuitem");
        button.setAttribute("aria-haspopup", "menu");
        button.setAttribute("aria-expanded", "false");
        button.tabIndex = index === 0 ? 0 : -1;
        button.addEventListener("click", () => (openIndex === index ? close(false) : show(index, false)));
        button.addEventListener("pointerenter", () => { if (openIndex >= 0 && openIndex !== index) show(index, false); });
        return button;
      }));
    }
    buttons().forEach((button, index) => {
      button.textContent = menus[index].label;
      button.classList.toggle("shell-menu-badge", menus[index].badge);
    });
    if (openIndex >= 0) open(openIndex, false);
  };

  return {element: bar, render, focus: () => buttons()[0]?.focus()};
}
