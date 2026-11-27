import {SplashService} from "../../bindings/github.com/local/dsh-work/internal/splash";
import {t} from "../i18n";
import {icon} from "../ui/icons";
import {createSplashContainer, type SplashContainer, type SplashInfo} from "./container";
import {builtinScenes} from "./scene";

/** Settings → Splash screen: list, in-place preview, import, use and remove. */
export function mountSplashSettings() {
  const element = <T extends HTMLElement>(id: string) => document.getElementById(id) as T;
  const section = element("manager-section-splash");
  const surface = document.getElementById("manager-surface");
  const list = element("splash-list");
  const status = element("splash-status");
  const previewHome = element("splash-preview-home");
  const preview = element("splash-preview");
  const stage = element("splash-preview-stage");
  const states = element("splash-preview-states");
  const useButton = element<HTMLButtonElement>("splash-use");
  const removeButton = element<HTMLButtonElement>("splash-remove");
  let items: SplashInfo[] = [];
  let openId: string | undefined;
  let container: SplashContainer | undefined;
  let busy = false;
  let refreshRevision = 0;

  let previewState: "waiting" | "ready" | "failed" = "waiting";

  const name = (item: SplashInfo) => item.builtin ? t(builtinScenes[item.id]?.nameKey ?? item.id) : item.name;
  const open = () => items.find(item => item.id === openId);

  function finish(outcome: "ready" | "failed") {
    if (previewState !== "waiting") container?.reset();
    previewState = outcome;
    void container?.finish(outcome);
  }

  function releasePreview() {
    container?.dispose();
    container = undefined;
    previewState = "waiting";
  }

  function closePreview() {
    releasePreview();
    previewHome.append(preview);
  }

  function openPreview(item: SplashInfo) {
    releasePreview();
    container = createSplashContainer();
    void container.mount(stage, item);
  }

  function render() {
    const focused = document.activeElement as HTMLElement | null;
    const focusedId = focused?.dataset?.splashId;
    const focusedPreviewId = focused && preview.contains(focused) ? focused.id : undefined;
    previewHome.append(preview);
    list.replaceChildren();
    for (const item of items) {
      const isOpen = item.id === openId;
      const entry = document.createElement("div");
      entry.className = `splash-entry${isOpen ? " is-open" : ""}`;
      const option = document.createElement("button");
      option.type = "button";
      option.className = "row splash-list-item";
      option.dataset.splashId = item.id;
      option.tabIndex = isOpen || (!openId && item === items[0]) ? 0 : -1;
      option.setAttribute("aria-expanded", String(isOpen));
      if (isOpen) option.setAttribute("aria-controls", preview.id);
      option.disabled = busy;
      const copy = document.createElement("span");
      copy.className = "splash-list-copy";
      const title = document.createElement("strong");
      title.textContent = name(item);
      const detail = document.createElement("span");
      detail.textContent = `${t(`splash.kind.${item.kind}`)}${item.selected ? ` · ${t("splash.inUse")}` : ""}`;
      copy.append(title, detail);
      option.append(copy, icon("chevron-right"));
      option.addEventListener("click", () => browse(item.id));
      entry.append(option);
      if (isOpen) entry.append(preview);
      list.append(entry);
    }
    const current = open();
    element("splash-preview-name").textContent = current ? name(current) : "";
    states.hidden = current?.kind !== "scene";
    useButton.hidden = !current || current.selected;
    removeButton.hidden = !current || current.builtin;
    useButton.disabled = removeButton.disabled = busy;
    element<HTMLButtonElement>("splash-import").disabled = busy;
    for (const button of Array.from(states.querySelectorAll<HTMLButtonElement>("button"))) button.disabled = busy;
    if (focusedId) list.querySelector<HTMLButtonElement>(`[data-splash-id="${CSS.escape(focusedId)}"]`)?.focus({preventScroll: true});
    if (focusedPreviewId) {
      const control = element<HTMLButtonElement>(focusedPreviewId);
      if (!control.disabled && control.getClientRects().length) control.focus({preventScroll: true});
      else if (openId) list.querySelector<HTMLButtonElement>(`[data-splash-id="${CSS.escape(openId)}"]`)?.focus({preventScroll: true});
    }
  }

  function browse(id: string) {
    if (busy) return;
    if (openId === id) {
      openId = undefined;
      closePreview();
      render();
      return;
    }
    openId = id;
    render();
    const item = open();
    if (item) openPreview(item);
  }

  async function refresh() {
    const revision = ++refreshRevision;
    try {
      const next = (await SplashService.List() ?? []) as SplashInfo[];
      if (revision !== refreshRevision) return;
      items = next;
      if (!items.some(item => item.id === openId)) { openId = items.find(item => item.selected)?.id ?? items[0]?.id; closePreview(); }
      render();
      const item = open();
      if (item && !container && visible()) openPreview(item);
    } catch { if (revision === refreshRevision) status.textContent = t("splash.actionFailed"); }
  }

  async function run(work: () => Promise<unknown>, failure: string) {
    if (busy) return;
    busy = true; status.textContent = ""; render();
    try { await work(); }
    catch { status.textContent = t(failure); }
    finally { busy = false; render(); await refresh(); }
  }

  element("splash-import").addEventListener("click", () => void run(async () => {
    const imported = await SplashService.Import();
    if (imported) { openId = imported.id; closePreview(); }
  }, "splash.importFailed"));
  useButton.addEventListener("click", () => {
    const item = open();
    if (item) void run(() => SplashService.Select(item.id), "splash.actionFailed");
  });
  removeButton.addEventListener("click", () => {
    const item = open();
    if (!item || !window.confirm(t("confirm.removeSplash", {name: name(item)}))) return;
    void run(async () => { await SplashService.Remove(item.id); openId = undefined; closePreview(); }, "splash.actionFailed");
  });
  element("splash-preview-waiting").addEventListener("click", () => {
    previewState = "waiting";
    container?.reset();
  });
  element("splash-preview-ready").addEventListener("click", () => finish("ready"));
  element("splash-preview-failed").addEventListener("click", () => finish("failed"));
  list.addEventListener("keydown", event => {
    if (preview.contains(event.target as Node) || busy) return;
    const index = items.findIndex(item => item.id === openId);
    const move = ({ArrowDown: index + 1, ArrowUp: index - 1, Home: 0, End: items.length - 1} as Record<string, number>)[event.key];
    if (move === undefined || items.length === 0) return;
    event.preventDefault();
    const next = items[(move + items.length) % items.length];
    if (next.id !== openId) browse(next.id);
    list.querySelector<HTMLButtonElement>(`[data-splash-id="${CSS.escape(next.id)}"]`)?.focus();
  });

  // The preview runs only while the page is on screen: leaving the section or
  // closing Settings releases it; coming back reloads the list.
  const visible = () => !section.hidden && !surface?.hidden;
  let wasVisible = false;
  const sync = () => {
    const now = visible();
    if (now === wasVisible) return;
    wasVisible = now;
    if (now) { openId = undefined; status.textContent = ""; void refresh(); }
    else closePreview();
  };
  const observer = new MutationObserver(sync);
  observer.observe(section, {attributes: true, attributeFilter: ["hidden"]});
  if (surface) observer.observe(surface, {attributes: true, attributeFilter: ["hidden"]});
  sync();
  window.addEventListener("pagehide", () => { observer.disconnect(); refreshRevision++; closePreview(); }, {once: true});

  return {refresh, renderLocale: render};
}
