import {Events, Window} from "@wailsio/runtime";
import {icon, type IconName} from "./ui/icons";

/** R-50 prototype: trusted shell chrome around the framed DSH document. */
export function mountShell() {
  document.documentElement.dataset.shell = "true";
  const bar = document.createElement("header");
  bar.className = "shell-bar";
  const brand = document.createElement("div");
  brand.className = "shell-brand";
  brand.textContent = "dsh-work";
  const drag = document.createElement("div");
  drag.className = "shell-drag";
  const controls = document.createElement("div");
  controls.className = "shell-controls";
  const control = (label: string, name: IconName, action: () => unknown) => {
    const button = document.createElement("button");
    button.type = "button";
    button.setAttribute("aria-label", label);
    button.append(icon(name));
    button.addEventListener("click", () => void action());
    controls.append(button);
    return button;
  };
  control("最小化", "minus", () => Window.Minimise());
  const maximise = control("最大化", "square", () => Window.ToggleMaximise());
  control("关闭", "x", () => Window.Close()).classList.add("shell-close");
  bar.append(brand, drag, controls);

  const frame = document.createElement("iframe");
  frame.className = "shell-frame";
  frame.title = "DSH";
  frame.setAttribute("sandbox", "allow-scripts allow-same-origin allow-forms allow-popups allow-downloads allow-modals");
  frame.setAttribute("allow", "microphone; clipboard-read; clipboard-write; fullscreen");
  frame.hidden = true;
  document.body.prepend(bar);
  document.body.append(frame);

  const host = document.getElementById("host-surface");
  let eventSeen = false;
  const show = (url: string) => {
    if (!url) {
      frame.hidden = true;
      frame.removeAttribute("src");
      host?.removeAttribute("hidden");
      bar.style.removeProperty("background-color");
      bar.style.removeProperty("color");
      return;
    }
    if (frame.getAttribute("src") !== url) frame.src = url;
    frame.hidden = false;
    host?.setAttribute("hidden", "true");
  };
  Events.On("worker-url", event => {
    eventSeen = true;
    show(String(event.data ?? ""));
  });
  void fetch("/__shell/worker").then(response => response.json()).then((value: {url: string}) => {
    if (!eventSeen) show(value.url);
  }).catch(() => {});

  // Only the framed DSH document may recolour the chrome, and only with colours.
  window.addEventListener("message", event => {
    const src = frame.getAttribute("src");
    if (!src || event.source !== frame.contentWindow || event.origin !== new URL(src).origin) return;
    const data = event.data as {type?: unknown; background?: unknown; color?: unknown};
    if (data?.type !== "dsh-work/surface" || typeof data.background !== "string" || typeof data.color !== "string") return;
    bar.style.backgroundColor = CSS.supports("color", data.background) ? data.background : "";
    bar.style.color = CSS.supports("color", data.color) ? data.color : "";
  });

  const syncMaximised = async () => {
    const maximised = await Window.IsMaximised();
    maximise.replaceChildren(icon(maximised ? "copy" : "square"));
    maximise.setAttribute("aria-label", maximised ? "还原" : "最大化");
  };
  window.addEventListener("resize", () => void syncMaximised());
  void syncMaximised();
}
