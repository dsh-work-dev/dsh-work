import {DiagnosticsService, SettingsService} from "../bindings/github.com/local/dsh-work/internal/desktopclient";
import type {Problem} from "../bindings/github.com/local/dsh-work/internal/diagnostics/models";
import {copyReport, reportOnGitHub} from "./diagnostics";
import {problemActions, problemGroup, type ProblemAction} from "./diagnostics-model";
import {subscribeLocale, t} from "./i18n";

type Feedback = (message: string, tone?: "neutral" | "success" | "error", display?: boolean | "transient") => void;
export interface DiagnosticsPanelOptions {
  feedback: Feedback;
  open(section: "runtimes" | "plugins" | "profiles" | "data-directories"): void;
  restart(): Promise<unknown>;
  checkUpdates(): void;
}
export function mountDiagnostics(options: DiagnosticsPanelOptions): {refresh(): Promise<void>} {
  const status = document.getElementById("diagnostics-status") as HTMLParagraphElement;
  const problemList = document.getElementById("diagnostics-problems") as HTMLDivElement;
  const verboseSwitch = document.getElementById("settings-verbose-logging") as HTMLInputElement;
  const openFolder = document.getElementById("diagnostics-open-folder") as HTMLButtonElement;
  const exportButton = document.getElementById("diagnostics-export") as HTMLButtonElement;
  const actionLabels: Record<ProblemAction, string> = {
    runtimes: "diagnostics.action.runtimes",
    restart: "diagnostics.action.restart",
    plugins: "diagnostics.action.plugins",
    profiles: "diagnostics.action.profiles",
    "data-directories": "diagnostics.action.dataDirectories",
    "check-updates": "diagnostics.action.checkUpdates",
  };
  let verboseLogging = false;
  let settingsLoaded = false;
  verboseSwitch.disabled = true;

  async function refreshVerboseLogging(): Promise<void> {
    verboseSwitch.disabled = true;
    try {
      const settings = await SettingsService.GetSettings();
      verboseLogging = settings.verboseLogging;
      verboseSwitch.checked = verboseLogging;
      settingsLoaded = true;
      verboseSwitch.disabled = false;
    } catch {
      settingsLoaded = false;
      verboseSwitch.disabled = true;
    }
  }

  async function saveVerboseLogging(): Promise<void> {
    const previous = verboseLogging;
    const requested = verboseSwitch.checked;
    verboseSwitch.disabled = true;
    try {
      const settings = await SettingsService.SetVerboseLogging(requested);
      verboseLogging = settings.verboseLogging;
      verboseSwitch.checked = verboseLogging;
      settingsLoaded = true;
    } catch {
      verboseSwitch.checked = previous;
      options.feedback(t("diagnostics.verboseFailed"), "error");
    } finally {
      verboseSwitch.disabled = !settingsLoaded;
    }
  }
  verboseSwitch.addEventListener("change", () => { void saveVerboseLogging(); });

  async function runAction(action: ProblemAction): Promise<void> {
    try {
      switch (action) {
        case "runtimes": options.open("runtimes"); break;
        case "plugins": options.open("plugins"); break;
        case "profiles": options.open("profiles"); break;
        case "data-directories": options.open("data-directories"); break;
        case "restart": await options.restart(); break;
        case "check-updates": options.checkUpdates(); break;
      }
    } catch {
      options.feedback(t("diagnostics.actionFailed"), "error");
    }
  }

  function makeButton(label: string, className: string, handler: () => void): HTMLButtonElement {
    const button = document.createElement("button");
    button.type = "button";
    button.className = className;
    button.textContent = t(label);
    button.addEventListener("click", handler);
    return button;
  }

  function renderProblem(problem: Problem): HTMLDetailsElement {
    const group = problemGroup(problem.code);
    const details = document.createElement("details");
    details.className = "diagnostics-problem";

    const summary = document.createElement("summary");
    const title = document.createElement("span");
    title.className = "diagnostics-problem-title";
    title.textContent = t("diagnostics.group." + group);
    const meta = document.createElement("span");
    meta.className = "diagnostics-problem-meta";
    meta.textContent = new Date(problem.lastTime).toLocaleString() + (problem.count > 1 ? " · " + t("diagnostics.count", {count: problem.count}) : "");
    summary.append(title, meta);

    const detail = document.createElement("p");
    detail.className = "note diagnostics-problem-detail";
    detail.textContent = problem.code + " · " + problem.message;

    const actions = document.createElement("div");
    actions.className = "actions";
    for (const action of problemActions(group)) {
      actions.append(makeButton(actionLabels[action], "button", () => { void runAction(action); }));
    }
    actions.append(makeButton("diagnostics.copyReport", "button", () => {
      void (async () => {
        try {
          await copyReport(problem);
          options.feedback(t("common.copied"), "success", "transient");
        } catch {
          options.feedback(t("common.copyFailed"), "error");
        }
      })();
    }));
    actions.append(makeButton("diagnostics.reportOnGitHub", "button button-primary", () => {
      void (async () => {
        try {
          await reportOnGitHub(problem);
          options.feedback(t("diagnostics.reportCopied"), "neutral", true);
        } catch {
          options.feedback(t("diagnostics.actionFailed"), "error");
        }
      })();
    }));

    details.append(summary, detail, actions);
    return details;
  }

  openFolder.addEventListener("click", () => {
    void (async () => {
      try {
        await DiagnosticsService.OpenLogFolder();
      } catch {
        options.feedback(t("diagnostics.openFolderFailed"), "error");
      }
    })();
  });
  exportButton.addEventListener("click", () => {
    void (async () => {
      exportButton.disabled = true;
      try {
        const result = await DiagnosticsService.ExportBundle();
        if (!result.path) return;
        const omitted = result.omitted?.length ? " " + t("diagnostics.bundleOmitted", {files: result.omitted.join(", ")}) : "";
        options.feedback(t("diagnostics.bundleSaved", {path: result.path}) + omitted, "success", true);
      } catch {
        options.feedback(t("diagnostics.exportFailed"), "error");
      } finally {
        exportButton.disabled = false;
      }
    })();
  });

  async function refresh(): Promise<void> {
    status.textContent = t("view.loading");
    problemList.hidden = true;
    const problemsRequest = DiagnosticsService.GetProblems();
    const settingsRequest = refreshVerboseLogging();
    try {
      const problems = (await problemsRequest) ?? [];
      if (problems.length === 0) {
        status.textContent = t("diagnostics.empty");
        problemList.hidden = true;
      } else {
        status.textContent = "";
        problemList.hidden = false;
        problemList.replaceChildren(...problems.map(renderProblem));
      }
    } catch {
      status.textContent = t("diagnostics.loadFailed");
      problemList.hidden = true;
    }
    await settingsRequest;
  }

  subscribeLocale(() => { void refresh(); });
  return {refresh};
}
