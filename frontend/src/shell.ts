import {Browser, Clipboard, Events, Window} from "@wailsio/runtime";
import {HostService, ManagerService, ShellService} from "../bindings/github.com/local/dsh-work/internal/desktopclient";
import {diagnosticsReport} from "./diagnostics";
import {subscribeLocale, t} from "./i18n";
import {parseFrameMessage} from "./shell-frame";
import {createMenuBar} from "./shell-menu";
import type {LinkTarget, MenuAction, MenuState, WindowAction} from "./shell-menu-model";
import {safeModeActive} from "./recovery";
import {icon, type IconName} from "./ui/icons";

const links: Record<LinkTarget, string> = {
  "docs": "https://github.com/dsh-work-dev/dsh-work/tree/main/docs",
  "feedback-desktop": "https://github.com/dsh-work-dev/dsh-work/issues",
  "feedback-dsh": "https://github.com/deepseek-ai/deepseek-harness/issues",
};

const windowActions: Record<WindowAction, () => Promise<void>> = {
  "close": () => Window.Close(),
  "zoom-in": () => Window.ZoomIn(),
  "zoom-out": () => Window.ZoomOut(),
  "zoom-reset": () => Window.ZoomReset(),
  "fullscreen": () => Window.ToggleFullscreen(),
};

/** Trusted shell chrome (top bar and menus) around the framed DSH document. */
export function mountShell() {
  document.documentElement.dataset.shell = "true";
  const bar = document.createElement("header");
  bar.className = "shell-bar";
  const brand = document.createElement("div");
  brand.className = "shell-brand";
  const mark = document.createElement("img");
  mark.src = "/branding/dsh-work-appicon.png";
  mark.alt = "dsh-work";
  brand.append(mark);
  const drag = document.createElement("div");
  drag.className = "shell-drag";
  const controls = document.createElement("div");
  controls.className = "shell-controls";
  const control = (labelKey: string, name: IconName, action: () => unknown) => {
    const button = document.createElement("button");
    button.type = "button";
    button.dataset.labelKey = labelKey;
    button.setAttribute("aria-label", t(labelKey));
    button.append(icon(name));
    button.addEventListener("click", () => void action());
    controls.append(button);
    return button;
  };
  control("shell.window.minimise", "minus", () => Window.Minimise());
  const maximise = control("shell.window.maximise", "square", () => Window.ToggleMaximise());
  control("shell.window.close", "x", () => Window.Close()).classList.add("shell-close");

  const frame = document.createElement("iframe");
  frame.className = "shell-frame";
  frame.title = "DSH";
  frame.setAttribute("sandbox", "allow-scripts allow-same-origin allow-forms allow-popups allow-downloads allow-modals");
  frame.setAttribute("allow", "microphone; clipboard-read; clipboard-write; fullscreen");
  frame.hidden = true;

  let menuState: MenuState = {lifecycle: "Starting", busy: false, pet: {ready: false, visible: false}, safeMode: null, updatePhase: "idle", window: {fullscreen: false, zoom: 1}, framed: false, dsh: null};
  let sendCommand: (id: string) => void = () => {};
  const setMenu = (patch: Partial<MenuState>) => {
    menuState = {...menuState, ...patch};
    menuBar.render(menuState);
  };
  const refreshPet = async () => {
    try {
      setMenu({pet: await ShellService.GetPet()});
    } catch (error) {
      console.warn("pet state unavailable", error);
    }
  };
  const refreshSafeMode = async () => {
    try {
      const snapshot = await ManagerService.GetSnapshot();
      setMenu({safeMode: {available: !!snapshot.configured, active: safeModeActive(snapshot)}});
    } catch (error) {
      console.warn("environment state unavailable", error);
    }
  };
  const copyDiagnostics = async () => {
    const [status, update, snapshot] = await Promise.all([
      HostService.GetStatus().catch(() => undefined), HostService.GetUpdateState().catch(() => undefined), ManagerService.GetSnapshot().catch(() => undefined),
    ]);
    await Clipboard.SetText(diagnosticsReport({update, state: status?.state, snapshot}));
  };
  const busyAction = async (work: () => Promise<unknown>) => {
    if (menuState.busy) return;
    setMenu({busy: true});
    try {
      await work();
    } catch (error) {
      console.warn("menu action failed", error);
    } finally {
      setMenu({busy: false});
    }
  };
  const run = (action: MenuAction) => {
    const report = (what: string) => (error: unknown) => console.warn(`${what} failed`, error);
    switch (action.kind) {
      case "window": void windowActions[action.action]().then(syncWindow).catch(report(action.action)); break;
      case "link": void Browser.OpenURL(links[action.target]).catch(report("open link")); break;
      case "devtools": void Window.OpenDevTools().catch(report("developer tools")); break;
      case "diagnostics": void copyDiagnostics().catch(report("copy diagnostics")); break;
      case "settings": void ShellService.OpenSettings(action.section); break;
      case "update": case "about": void ShellService.OpenSettings("about"); break;
      case "pet": void busyAction(async () => { menuState.pet = await ShellService.SetPetVisible(!menuState.pet.visible); }); break;
      case "refresh": reloadFrame(); break;
      case "restart": void busyAction(async () => { menuState.lifecycle = (await HostService.Restart()).state; }); break;
      case "safe-mode": void busyAction(async () => {
        try {
          const snapshot = await (menuState.safeMode?.active ? ManagerService.ExitSafeMode() : ManagerService.EnterSafeMode());
          menuState.safeMode = {available: !!snapshot.configured, active: safeModeActive(snapshot)};
        } catch (error) {
          // Settings Overview shows why the switch failed.
          void ShellService.OpenSettings("overview");
          throw error;
        }
      }); break;
      case "quit": void busyAction(() => HostService.Quit()); break;
      case "dsh": sendCommand(action.id); break;
    }
  };
  const menuBar = createMenuBar(t, run, id => {
    if (id === "view") { void refreshPet(); void syncWindow(); }
    if (id === "run") void refreshSafeMode();
  }, () => frame.contentWindow?.focus());
  menuBar.render(menuState);
  bar.append(brand, menuBar.element, drag, controls);
  document.body.prepend(bar);
  document.body.append(frame);

  subscribeLocale(() => {
    menuBar.render(menuState);
    controls.querySelectorAll<HTMLElement>("[data-label-key]").forEach(button => button.setAttribute("aria-label", t(button.dataset.labelKey!)));
    void syncMaximised();
  });
  Events.On("lifecycle", event => setMenu({lifecycle: String((event.data as {state?: string} | undefined)?.state ?? menuState.lifecycle)}));
  Events.On("update-state", event => setMenu({updatePhase: String((event.data as {phase?: string} | undefined)?.phase ?? "idle")}));
  void HostService.GetStatus().then(status => setMenu({lifecycle: status.state})).catch(() => {});
  void HostService.GetUpdateState().then(update => setMenu({updatePhase: update.phase})).catch(() => {});
  void refreshPet();
  void refreshSafeMode();

  const host = document.getElementById("host-surface");
  let eventSeen = false;
  const show = (url: string) => {
    if (!url) {
      setMenu({dsh: null, framed: false});
      frame.hidden = true;
      frame.removeAttribute("src");
      host?.removeAttribute("hidden");
      document.documentElement.style.removeProperty("--shell-surface");
      document.documentElement.style.removeProperty("--shell-text");
      return;
    }
    if (frame.getAttribute("src") !== url) {
      // A new DSH document reports its own catalog once it is ready.
      setMenu({dsh: null});
      frame.src = url;
    }
    setMenu({framed: true});
    frame.hidden = false;
    host?.setAttribute("hidden", "true");
  };
  // Reloads the DSH document only; the Worker keeps running.
  const reloadFrame = () => {
    const src = frame.getAttribute("src");
    if (!src) return;
    setMenu({dsh: null});
    frame.setAttribute("src", src);
  };
  Events.On("worker-url", event => {
    eventSeen = true;
    show(String(event.data ?? ""));
  });
  void fetch("/__shell/worker").then(response => response.json()).then((value: {url: string}) => {
    if (!eventSeen) show(value.url);
  }).catch(() => {});

  // Only the framed DSH document talks to the shell: colours, its command catalog and menu keys.
  window.addEventListener("message", event => {
    const src = frame.getAttribute("src");
    if (!src || event.source !== frame.contentWindow || event.origin !== new URL(src).origin) return;
    const message = parseFrameMessage(event.data);
    if (!message) return;
    if (message.type === "catalog") setMenu({dsh: message.commands});
    else if (message.type === "menu-key") menuBar.focus();
    else {
      const root = document.documentElement.style;
      if (CSS.supports("color", message.background)) root.setProperty("--shell-surface", message.background);
      if (CSS.supports("color", message.color)) root.setProperty("--shell-text", message.color);
    }
  });
  sendCommand = id => {
    const src = frame.getAttribute("src");
    if (src) frame.contentWindow?.postMessage({version: 1, type: "dsh-work/command", id}, new URL(src).origin);
  };

  // Alt pressed alone or F10 focuses the menu bar, as in a native window.
  let altAlone = false;
  window.addEventListener("keydown", event => {
    if (event.key === "F10" && !event.ctrlKey && !event.altKey && !event.shiftKey && !event.metaKey) {
      event.preventDefault();
      altAlone = false;
      menuBar.focus();
      return;
    }
    altAlone = event.key === "Alt" && !event.repeat;
  }, true);
  window.addEventListener("keyup", event => {
    if (event.key === "Alt" && altAlone) {
      event.preventDefault();
      menuBar.focus();
    }
    altAlone = false;
  }, true);

  // Full screen hides the window buttons; 视图 → 全屏 leaves it.
  const syncWindow = async () => {
    const [fullscreen, zoom] = await Promise.all([Window.IsFullscreen(), Window.GetZoom()]);
    controls.hidden = fullscreen;
    setMenu({window: {fullscreen, zoom}});
  };
  const syncMaximised = async () => {
    void syncWindow().catch(() => {});
    const maximised = await Window.IsMaximised();
    maximise.replaceChildren(icon(maximised ? "copy" : "square"));
    maximise.dataset.labelKey = maximised ? "shell.window.restore" : "shell.window.maximise";
    maximise.setAttribute("aria-label", t(maximise.dataset.labelKey));
  };
  window.addEventListener("resize", () => void syncMaximised());
  void syncMaximised();
}
