import {Events} from "@wailsio/runtime";

import {HostService} from "../bindings/github.com/local/dsh-work/internal/app";
import {viewModel, type LifecycleStatus} from "./lifecycle";
import {applyLocale, defaultLocale, mountLocale, subscribeLocale, t} from "./i18n";
import {mountManager} from "./manager";
import {applyTheme, mountTheme} from "./theme";

const surface = new URLSearchParams(window.location.search).get("surface");

applyLocale(defaultLocale);
mountLocale();
document.title = surface === "settings" ? "设置" : "dsh-work";

if (surface === "settings") {
  document.getElementById("host-surface")?.setAttribute("hidden", "true");
  document.getElementById("manager-surface")?.removeAttribute("hidden");
  mountTheme("system");
  mountManager();
} else {
  mountTheme("system");
  mountHost();
}

function mountHost() {
  const title = document.getElementById("app-title") as HTMLHeadingElement;
  const dot = document.getElementById("state-dot") as HTMLSpanElement;
  const label = document.getElementById("status-label") as HTMLElement;
  const message = document.getElementById("status-message") as HTMLElement;
  const detail = document.getElementById("status-detail") as HTMLElement;
  const progress = document.getElementById("progress-track") as HTMLDivElement;
  const workspace = document.getElementById("workspace-url") as HTMLElement;
  const cancel = document.getElementById("cancel") as HTMLButtonElement;
  const retry = document.getElementById("retry") as HTMLButtonElement;
  const quit = document.getElementById("quit") as HTMLButtonElement;
  const startupOutput = document.getElementById("startup-output") as HTMLPreElement;
  const copyStartupOutput = document.getElementById("copy-startup-output") as HTMLButtonElement;
  const startupSteps = Array.from(document.querySelectorAll<HTMLElement>("[data-startup-step]"));
  let lastStartupStep = 1;
  let latestOutput = "";
  let latestOutputData: {stdout?: string; stderr?: string} = {};
  let latestStatus: LifecycleStatus | undefined;
  let copyResetTimer: number | undefined;
  let outputTimer: number | undefined;
  let themeTimer: number | undefined;

  const phaseIndexes: Record<string, number> = {
    configuration: 1,
    runtime: 2,
    worker: 3,
    readiness: 4,
    workspace: 5
  };

  function formatStartupOutput(output: {stdout?: string; stderr?: string}): string {
    const stdout = output.stdout?.trimEnd() ?? "";
    const stderr = output.stderr?.trimEnd() ?? "";
    if (!stdout && !stderr) {
      return t("host.noOutput");
    }
    return [
      stdout ? `[stdout]\n${stdout}` : `[stdout]\n${t("host.stdoutEmpty")}`,
      stderr ? `[stderr]\n${stderr}` : `[stderr]\n${t("host.stderrEmpty")}`
    ].join("\n\n");
  }

  function renderStartupOutput(output: {stdout?: string; stderr?: string}) {
    latestOutputData = output;
    latestOutput = formatStartupOutput(output);
    startupOutput.textContent = latestOutput;
    copyStartupOutput.disabled = !output.stdout?.trim() && !output.stderr?.trim();
  }

  async function refreshStartupOutput() {
    try {
      renderStartupOutput(await HostService.GetStartupOutput());
    } catch (error) {
      console.error("Could not read DSH startup output", error);
    }
  }

  async function copyOutput() {
    if (!latestOutput || copyStartupOutput.disabled) {
      return;
    }
    try {
      if (navigator.clipboard?.writeText) {
        await navigator.clipboard.writeText(latestOutput);
      } else {
        const textarea = document.createElement("textarea");
        textarea.value = latestOutput;
        textarea.setAttribute("readonly", "true");
        textarea.style.position = "fixed";
        textarea.style.opacity = "0";
        document.body.append(textarea);
        textarea.select();
        const copied = document.execCommand("copy");
        textarea.remove();
        if (!copied) {
          throw new Error("Clipboard is unavailable.");
        }
      }
      copyStartupOutput.textContent = t("common.copied");
      if (copyResetTimer !== undefined) {
        window.clearTimeout(copyResetTimer);
      }
      copyResetTimer = window.setTimeout(() => {
        copyStartupOutput.textContent = t("common.copy");
        copyResetTimer = undefined;
      }, 1400);
    } catch (error) {
      copyStartupOutput.textContent = t("common.copyFailed");
      console.error("Could not copy DSH startup output", error);
    }
  }

  function render(status: LifecycleStatus) {
    latestStatus = status;
    const model = viewModel(status, (key, fallback) => {
      const translated = t(key);
      return translated === key ? fallback : translated;
    });
    title.textContent = status.state === "Ready"
      ? t("host.titleReady")
      : status.state === "Failed"
        ? t("host.titleFailed")
        : status.state === "Stopped"
          ? t("host.titleStopped")
          : status.state === "Stopping"
            ? t("host.titleStopping")
            : t("host.titleStarting");
    dot.className = `state-dot ${model.tone === "ready" ? "ready" : model.tone === "failed" ? "failed" : ""}`;
    label.textContent = model.label;
    message.textContent = model.message;
    detail.textContent = model.detail;
    workspace.textContent = model.workspace;
    progress.className = `progress-track ${model.tone === "ready" ? "complete" : model.tone === "failed" ? "failed" : ""}`;
    progress.setAttribute("aria-valuetext", model.label);
    const currentStep = status.state === "Ready"
      ? startupSteps.length
      : status.state === "Failed"
        ? lastStartupStep
        : status.state === "Starting"
          ? phaseIndexes[status.phase] ?? lastStartupStep
          : 0;
    if (status.state === "Starting" && phaseIndexes[status.phase]) {
      lastStartupStep = phaseIndexes[status.phase];
    }
    progress.setAttribute("aria-valuenow", String(currentStep));
    progress.style.setProperty("--progress", `${(currentStep / startupSteps.length) * 100}%`);
    for (let index = 0; index < startupSteps.length; index += 1) {
      const step = startupSteps[index];
      const stepNumber = index + 1;
      const complete = status.state === "Ready" || (currentStep > 0 && stepNumber < currentStep);
      const current = currentStep > 0 && !complete && stepNumber === currentStep;
      const failed = status.state === "Failed" && current;
      step.classList.toggle("is-complete", complete);
      step.classList.toggle("is-current", current);
      step.classList.toggle("is-failed", failed);
      if (current) {
        step.setAttribute("aria-current", "step");
      } else {
        step.removeAttribute("aria-current");
      }
    }
    cancel.hidden = !model.showCancel;
    retry.hidden = !model.showRetry;
    cancel.disabled = status.state === "Stopping";
    if (status.state === "Ready" && outputTimer !== undefined) {
      window.clearInterval(outputTimer);
      outputTimer = undefined;
    }
    if (status.state === "Ready" && themeTimer !== undefined) {
      window.clearInterval(themeTimer);
      themeTimer = undefined;
    }
  }

  async function refresh() {
    try {
      render(await HostService.GetStatus() as LifecycleStatus);
    } catch (error) {
      console.error("Could not read dsh-work lifecycle status", error);
    }
  }

  async function refreshTheme() {
    try {
      applyTheme(await HostService.GetTheme());
    } catch (error) {
      console.error("Could not read DSH theme preference", error);
    }
  }

  async function refreshLocale() {
    try {
      applyLocale(await HostService.GetLocale());
    } catch (error) {
      console.error("Could not read dsh-work language", error);
    }
  }

  async function invoke(action: () => Promise<LifecycleStatus>) {
    cancel.disabled = true;
    retry.disabled = true;
    try {
      render(await action());
    } catch (error) {
      console.error("dsh-work host action failed", error);
    } finally {
      retry.disabled = false;
    }
  }

  cancel.addEventListener("click", () => void invoke(() => HostService.Cancel() as Promise<LifecycleStatus>));
  retry.addEventListener("click", () => void invoke(() => HostService.Start() as Promise<LifecycleStatus>));
  quit.addEventListener("click", () => void invoke(() => HostService.Quit() as Promise<LifecycleStatus>));
  copyStartupOutput.addEventListener("click", () => void copyOutput());
  subscribeLocale(() => {
    if (latestStatus) {
      render(latestStatus);
    }
    renderStartupOutput(latestOutputData);
  });

  Events.On("lifecycle", (event) => render(event.data as LifecycleStatus));
  render({state: "Starting", phase: "configuration", canRetry: false, canCancel: true});
  void refresh();
  void refreshLocale();
  void refreshTheme();
  void refreshStartupOutput();
  outputTimer = window.setInterval(() => void refreshStartupOutput(), 400);
  themeTimer = window.setInterval(() => void refreshTheme(), 1000);
  window.addEventListener("focus", () => void refreshTheme());
  document.addEventListener("visibilitychange", () => {
    if (document.visibilityState === "visible") {
      void refreshTheme();
    }
  });
  window.setTimeout(() => {
    if (outputTimer !== undefined) {
      window.clearInterval(outputTimer);
      outputTimer = undefined;
    }
  }, 120000);
}
