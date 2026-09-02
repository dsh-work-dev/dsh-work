import {Events} from "@wailsio/runtime";

import {HostService} from "../bindings/github.com/local/work/internal/app";
import {viewModel, type LifecycleStatus} from "./lifecycle";
import {mountManager} from "./manager";

const surface = new URLSearchParams(window.location.search).get("surface");

if (surface === "manager") {
  document.getElementById("host-surface")?.setAttribute("hidden", "true");
  document.getElementById("manager-surface")?.removeAttribute("hidden");
  mountManager();
} else {
  mountHost();
}

function mountHost() {
  const title = document.getElementById("app-title") as HTMLHeadingElement;
  const dot = document.getElementById("state-dot") as HTMLSpanElement;
  const label = document.getElementById("status-label") as HTMLParagraphElement;
  const message = document.getElementById("status-message") as HTMLParagraphElement;
  const detail = document.getElementById("status-detail") as HTMLParagraphElement;
  const progress = document.getElementById("progress-track") as HTMLDivElement;
  const workspace = document.getElementById("workspace-url") as HTMLParagraphElement;
  const cancel = document.getElementById("cancel") as HTMLButtonElement;
  const retry = document.getElementById("retry") as HTMLButtonElement;
  const quit = document.getElementById("quit") as HTMLButtonElement;

  function render(status: LifecycleStatus) {
    const model = viewModel(status);
    title.textContent = status.state === "Ready" ? "Your local workspace is ready" : "Starting your local workspace";
    dot.className = `state-dot ${model.tone === "ready" ? "ready" : model.tone === "failed" ? "failed" : ""}`;
    label.textContent = model.label;
    message.textContent = model.message;
    detail.textContent = model.detail;
    workspace.textContent = model.workspace;
    progress.className = `progress-track ${model.tone === "ready" ? "complete" : model.tone === "failed" ? "failed" : ""}`;
    progress.setAttribute("aria-valuetext", model.label);
    cancel.hidden = !model.showCancel;
    retry.hidden = !model.showRetry;
    cancel.disabled = status.state === "Stopping";
  }

  async function refresh() {
    try {
      render(await HostService.GetStatus() as LifecycleStatus);
    } catch (error) {
      console.error("Could not read Work lifecycle status", error);
    }
  }

  async function invoke(action: () => Promise<LifecycleStatus>) {
    cancel.disabled = true;
    retry.disabled = true;
    try {
      render(await action());
    } catch (error) {
      console.error("Work host action failed", error);
    } finally {
      retry.disabled = false;
    }
  }

  cancel.addEventListener("click", () => void invoke(() => HostService.Cancel() as Promise<LifecycleStatus>));
  retry.addEventListener("click", () => void invoke(() => HostService.Start() as Promise<LifecycleStatus>));
  quit.addEventListener("click", () => void invoke(() => HostService.Quit() as Promise<LifecycleStatus>));

  Events.On("lifecycle", (event) => render(event.data as LifecycleStatus));
  render({state: "Starting", phase: "configuration", canRetry: false, canCancel: true});
  void refresh();
}
