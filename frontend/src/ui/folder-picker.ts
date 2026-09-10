import {t} from "../i18n";

interface FolderPickerOptions {
  labelID: string;
  choose: (currentPath: string) => Promise<string>;
  changed: (path: string) => void;
  open?: () => Promise<void>;
  report: (error: unknown) => void;
}

/** Native folder selection with selectable path text and standard settings actions. */
export function mountFolderPicker(container: HTMLElement, options: FolderPickerOptions) {
  const path = document.createElement("span");
  path.className = "folder-picker-path";
  path.tabIndex = 0;
  path.setAttribute("aria-labelledby", options.labelID);
  const actions = document.createElement("div");
  actions.className = "folder-picker-actions";
  function action(key: string) {
    const button = document.createElement("button");
    button.type = "button";
    button.className = "button button-secondary";
    button.dataset.i18n = key;
    button.textContent = t(key);
    actions.append(button);
    return button;
  }
  const choose = action("storage.choose");
  const open = action("storage.open");
  open.disabled = true;
  container.classList.add("folder-picker");
  container.replaceChildren(path, actions);
  let currentPath = "";
  let disabled = false;
  let busy = false;
  choose.addEventListener("click", () => void (async () => {
    if (disabled || busy) return;
    busy = true;
    choose.disabled = true;
    container.setAttribute("aria-busy", "true");
    try {
      const selected = await options.choose(currentPath);
      if (selected) options.changed(selected);
    } catch (error) { options.report(error); }
    finally {
      busy = false;
      choose.disabled = disabled;
      container.removeAttribute("aria-busy");
    }
  })());
  open.hidden = !options.open;
  open.addEventListener("click", () => void options.open?.().catch(options.report));
  return {
    setPath(value: string) { currentPath = value; path.textContent = value; path.title = value; open.disabled = !value; },
    setDisabled(value: boolean) { disabled = value; choose.disabled = disabled || busy; }
  };
}
