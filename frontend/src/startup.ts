import {mountRestorePoints} from "./restore-points";
import {Events} from "@wailsio/runtime";
import {safeModeActive} from "./recovery";
import {HostService, ManagerService} from "../bindings/github.com/local/dsh-work/internal/app";
import {NodeSelectionKind, type Snapshot} from "../bindings/github.com/local/dsh-work/internal/dshmanager";
import type {OperationStatus} from "../bindings/github.com/local/dsh-work/internal/acquisition/models";
import {runningLaunchSelection, startupProfileName, type LifecycleStatus} from "./lifecycle";
import {acquisitionPreparation, runtimePreparationText, formatRuntimeBytes} from "./acquisition-view";
import {mountOperationLog} from "./operation-log";
import {applyLocale, subscribeLocale, t} from "./i18n";
import {applyTheme} from "./theme";

export function mountHost() {
  const element = <T extends HTMLElement>(id: string) => document.getElementById(id) as T;
  const node = element<HTMLSelectElement>("startup-node");
  const dsh = element<HTMLSelectElement>("startup-dsh");
  const retry = element<HTMLButtonElement>("retry");
  const cancel = element<HTMLButtonElement>("cancel");
  const downloadNode = element<HTMLButtonElement>("download-node");
  const downloadDsh = element<HTMLButtonElement>("download-dsh");
  const releases = element<HTMLButtonElement>("refresh-releases");
  const errorPanel = element("startup-action-error");
  const output = element("startup-output");
  const resolution = element("startup-resolution");
  const diagnostics = element("startup-diagnostics");
  const restoreDialog = element<HTMLDialogElement>("startup-restore-dialog");
  const restoreButton = element<HTMLButtonElement>("open-startup-restore");
  let logsRequested = false;
  const copy = element<HTMLButtonElement>("copy-startup-output");
  const panels = {node: element("startup-node-progress"), dsh: element("startup-dsh-progress")};
  const logs = {node: mountOperationLog(panels.node), dsh: mountOperationLog(panels.dsh)};
  let snapshot: Snapshot | undefined;
  let status: LifecycleStatus = {state: "Starting", phase: "configuration", canRetry: false, canCancel: true};
  let connectionFailed = false;
  let busy = false;
  let acquiring = false;
  let cancelRequested = false;
  let polling = false;
  let disposed = false;
  let selectedNode = "";
  let selectedRuntime = "";
  let lastError = "";
  let lastPhase = "configuration";
  let acquisitionKind: "node" | "dsh" | undefined;
  let starting: ReturnType<typeof ManagerService.SetRunContext> | undefined;
  const progressPanel = element("startup-progress");
  const progressMessage = element("startup-progress-message");
  const progressBar = element<HTMLProgressElement>("startup-progress-bar");

  const installedNode = () => selectedNode === "system" ? !!snapshot?.systemNode : !!snapshot?.nodes?.some(n => n.id === selectedNode && n.installed && n.verified);
  const installedRuntime = () => snapshot?.runtimes?.find(r => r.id === selectedRuntime && r.installed);
  const downloadableVersion = () => snapshot?.dshReleases?.some(r => r.version === selectedVersion());
  const selectedVersion = () => snapshot?.runtimes?.find(r => r.id === selectedRuntime)?.version ?? snapshot?.dshReleases?.find(r => `dsh-${r.version}` === selectedRuntime)?.version;
  const active = () => status.state === "Starting" || status.state === "Stopping" || status.state === "Ready";

  const versionPoints = mountRestorePoints(element("startup-restore-points"), next => {snapshot=next;renderEnvironment();}, true);
  function render() {
    const launch = runningLaunchSelection(status);
    if (launch && !acquiring) {
      selectedRuntime = launch.runtimeId;
      selectedNode = launch.nodeId;
    }
    const missing = snapshot && (!installedNode() || !installedRuntime());
    const failed = status.state === "Failed" && !busy;
    const stopped = status.state === "Stopped" && !busy;
    element("app-title").textContent = t(status.state === "Stopping" ? "startup.stopping" : failed ? "startup.attention" : stopped ? "startup.stopped" : "startup.startTitle");
    const code = status.error?.code ?? "";
    const phase = status.state === "Starting" ? status.phase : lastPhase;
    let current = acquisitionKind === "node" ? 0 : acquisitionKind === "dsh" ? 1 : phase === "node" || phase === "configuration" ? 0 : phase === "runtime" ? 1 : phase === "profile" ? 2 : 3;
    if (failed) current = code.includes("NODE") ? 0 : code.includes("PROFILE") ? 2 : code.includes("RUNTIME") || code.includes("VERSION") ? 1 : code.startsWith("DSH_") || code.includes("PROCESS") ? 3 : -1;
    if (missing && !active() && !busy) current = !installedNode() ? 0 : 1;
    const ids = ["node", "dsh", "profile", "start"];
    const values = [launch?.nodeVersion || (selectedNode === "system" ? snapshot?.systemNode?.version : snapshot?.nodes?.find(n => n.id === selectedNode)?.version), launch?.runtimeVersion || selectedVersion(), startupProfileName(status, snapshot?.configured?.profile.name), t("startup.ready")];
    const labels = ["startup-node-state", "startup-dsh-state", "startup-profile", "startup-start-state"];
    ids.forEach((id, index) => {
      const row = element(`check-${id}`);
      const done = status.state === "Ready" || index < current;
      const running = index === current && (busy || status.state === "Starting");
      row.dataset.state = done ? "done" : index === current && failed ? "failed" : running ? "active" : "pending";
      row.querySelector(".check-marker")!.textContent = done ? "✓" : index === current && failed ? "!" : String(index + 1);
      element(labels[index]).textContent = done ? index === 1 ? `${values[index] ?? ""} · ${t("startup.localInstalled")}` : values[index] ?? t("startup.available") : running ? t(index === 3 ? "startup.launching" : "startup.checking") : index === current && missing ? t("startup.missingComponent") : index === current && stopped ? t("startup.stopped") : index === current && failed ? t("startup.stepFailed") : t("startup.waiting");
      if (index === 2 && values[index] && !done) element(labels[index]).textContent = `${values[index]} · ${element(labels[index]).textContent}`;
    });
    retry.textContent = t(missing ? "startup.installAndStart" : failed ? "common.retry" : "startup.continue");
    const currentBody = current >= 0 && !connectionFailed ? element(`check-${ids[current]}`).querySelector(".check-body")! : undefined;
    for (const content of [progressPanel, resolution]) {
      if (currentBody) { if (content.parentElement !== currentBody) currentBody.append(content); }
      else document.querySelector(".startup-footer")!.before(content);
    }
    const needsAttention = failed || !!lastError || connectionFailed;
    resolution.classList.toggle("has-error", needsAttention);
    diagnostics.hidden = !needsAttention && !logsRequested;
    const logToggle = element("show-startup-log");
    logToggle.hidden = needsAttention;
    logToggle.setAttribute("aria-expanded", String(!diagnostics.hidden));
    if (connectionFailed) output.textContent = lastError || t("startup.readError");
    progressPanel.hidden = !acquiring;
    if (!acquiring) progressBar.removeAttribute("value");
    element("node-controls").hidden = !(snapshot && !installedNode() && !active() && !busy);
    element("dsh-controls").hidden = !(snapshot && installedNode() && (!installedRuntime()) && !active() && !busy);
    element("status-message").textContent = t(code.includes("PROFILE") && status.error?.detail?.includes("ERR_MODULE_NOT_FOUND") ? "startup.moduleFailure" : code.includes("PROFILE") ? "startup.profileFailure" : code === "DSH_READINESS_TIMEOUT" ? "startup.timeoutFailure" : "startup.failureMessage");
    element("status-message").hidden = !failed || !!missing || !!lastError;
    node.disabled = dsh.disabled = releases.disabled = busy || active() || !snapshot;
    downloadNode.disabled = busy || active() || !snapshot;
    downloadDsh.disabled = busy || active() || !downloadableVersion() || !!installedRuntime();
    const safe = element<HTMLButtonElement>("startup-safe-mode");
    safe.hidden = !failed && !stopped;
    const restoring = snapshot?.restorePoints?.operation?.status === "running";
    restoreButton.hidden = !failed && !stopped && !restoring;
    restoreButton.disabled = !snapshot || busy || (active() && !restoring);
    restoreButton.textContent = t(restoring ? "startup.restoreProgress" : "startup.restore");
    versionPoints.render(snapshot, busy || active());
    safe.disabled = busy || active() || !snapshot?.configured || !installedRuntime() || !installedNode();
    safe.textContent = t(safeModeActive(snapshot) ? "safe.exit" : "safe.enter");
    retry.disabled = busy || active() || !snapshot || !selectedVersion();
    retry.hidden = busy || active() || !!missing;
    errorPanel.hidden = !lastError;
    cancel.hidden = !acquiring && !status.canCancel;
    cancel.disabled = cancelRequested || status.state === "Stopping";
    document.querySelector<HTMLElement>(".startup-checks")!.hidden = connectionFailed;
    if (connectionFailed) {

      element("app-title").textContent = t("startup.attention");
      for (const id of ["startup-node-state", "startup-dsh-state", "startup-profile", "startup-start-state"]) element(id).textContent = "—";
      for (const id of ["node", "dsh", "profile", "start"]) element(`check-${id}`).dataset.state = "pending";
      retry.hidden = false; retry.disabled = busy; retry.textContent = t("common.retry");
      cancel.hidden = true; safe.hidden = true; restoreButton.hidden = true;
    }
  }

  function renderEnvironment() {
    if (!snapshot) return;
    selectedNode ||= snapshot.configured?.node.kind === "managed" ? snapshot.configured.node.installationId ?? "system" : "system";
    if (snapshot.current && active()) {
      selectedRuntime = snapshot.current.runtimeId;
      selectedNode = snapshot.current.node.kind === "managed" ? snapshot.current.node.installationId ?? "system" : "system";
    }
    selectedRuntime ||= snapshot.configured?.runtimeId ?? (snapshot.dshReleases?.[0] ? `dsh-${snapshot.dshReleases[0].version}` : "");
    node.replaceChildren(new Option(`Node.js ${snapshot.systemNode?.version ?? ""} · ${t("startup.systemNode")}`, "system"));
    for (const n of snapshot.nodes ?? []) node.add(new Option(`Node.js ${n.version}`, n.id));
    if (!Array.from(node.options).some(o => o.value === selectedNode)) node.add(new Option(`${selectedNode} · ${t("startup.missingComponent")}`, selectedNode));
    node.value = selectedNode;
    const versions = new Map((snapshot.runtimes ?? []).map(r => [r.id, r.version]));
    for (const release of snapshot.dshReleases ?? []) if (!versions.has(`dsh-${release.version}`)) versions.set(`dsh-${release.version}`, release.version);
    if (selectedRuntime && !versions.has(selectedRuntime)) versions.set(selectedRuntime, selectedRuntime);
    dsh.replaceChildren(...Array.from(versions).map(([id, version]) => {
      const installed = snapshot!.runtimes?.some(r => r.id === id && r.installed);
      const downloadable = snapshot!.dshReleases?.some(r => r.version === version);
      return new Option(`DSH ${version} · ${t(installed ? "startup.localInstalled" : downloadable ? "startup.remoteDownload" : "startup.notInstalled")}`, id);
    }));
    dsh.value = selectedRuntime;
    render();
  }

  async function refreshEnvironment() {
    snapshot = await ManagerService.GetSnapshot();
    applyTheme(snapshot.theme);
    renderEnvironment();
  }

  element("startup-safe-mode").addEventListener("click", () => void action(async () => {
    snapshot = await (safeModeActive(snapshot) ? ManagerService.ExitSafeMode() : ManagerService.EnterSafeMode());
  }));

  function showError(error: unknown) {
    const value = error as {message?: string; summary?: string; detail?: string};
    lastError = [value?.summary, value?.detail, value?.message].filter(Boolean).join("\n") || String(error);
    errorPanel.textContent = t(connectionFailed ? "startup.readError" : cancelRequested ? "runtimes.cancelled" : lastError.includes("PROFILE_") ? "startup.profileFailure" : "startup.failureMessage");
    errorPanel.hidden = false;
    render();
  }

  async function action(work: () => Promise<unknown>, acquisition?: "node" | "dsh") {
    if (busy) return;
    element("diagnostic-copy-status").textContent = "";
    logsRequested = false;
    busy = true; acquiring = !!acquisition; acquisitionKind = acquisition; cancelRequested = false; errorPanel.hidden = true; lastError = "";
    progressMessage.textContent = t("startup.preparing"); progressBar.removeAttribute("value");
    if (acquisition) {

      panels[acquisition].hidden = false;
      panels[acquisition].querySelector("p")!.textContent = t("runtimes.preparing");
      const progress = panels[acquisition].querySelector("progress")!;
      progress.hidden = false; progress.removeAttribute("value");
    }
    render();
    try { await work(); } catch (error) {
      showError(error);
      if (acquisition) panels[acquisition].querySelector("p")!.textContent = t(cancelRequested ? "runtimes.cancelled" : "runtimes.failed");
    }
    finally {
      busy = false; acquiring = false; acquisitionKind = undefined; cancelRequested = false;
      Object.values(panels).forEach(panel => { panel.querySelector("progress")!.hidden = true; });
      if (!connectionFailed) {
        try {
          await refreshEnvironment();
          status = await HostService.GetStatus() as LifecycleStatus;
        } catch {
          connectionFailed = true;
          showError(new Error(t("startup.readError")));
        }
      }
      render();
    }
  }

  node.addEventListener("change", () => { selectedNode = node.value; render(); });
  dsh.addEventListener("change", () => { selectedRuntime = dsh.value; render(); });
  releases.addEventListener("click", () => void action(async () => { snapshot = await ManagerService.RefreshDSHReleases();
    if (!installedRuntime() && !snapshot.dshReleases?.some(r => r.version === selectedVersion())) {
      const release = snapshot.dshReleases?.[0]; if (release) selectedRuntime = `dsh-${release.version}`;
    }
    renderEnvironment(); }));
  downloadNode.addEventListener("click", () => void action(async () => {
    snapshot = await ManagerService.InstallLatestNode();
    const installed = snapshot.nodes?.find(n => n.version === snapshot?.latestNode?.version && n.installed && n.verified);
    if (installed) selectedNode = installed.id;
    renderEnvironment();
    if (installedNode() && installedRuntime()) await prepareAndStart();
  }, "node"));
  downloadDsh.addEventListener("click", () => void action(async () => {
    const version = selectedVersion();
    if (!version) return;
    // Catalog discovery is part of the user's explicit download action.
    panels.dsh.hidden = false;
    panels.dsh.querySelector("p")!.textContent = t("startup.fetchingReleases");
    panels.dsh.querySelector("progress")!.hidden = false;
    panels.dsh.querySelector("progress")!.removeAttribute("value");
    await ManagerService.RefreshDSHReleases();
    if (cancelRequested) return;
    snapshot = await ManagerService.InstallRuntime(version);
    if (!installedNode()) {
      const installed = snapshot.nodes?.find(n => n.version === snapshot?.latestNode?.version && n.installed && n.verified);
      if (installed) selectedNode = installed.id;
    }
    renderEnvironment();
    if (installedNode() && installedRuntime()) await prepareAndStart();
  }, "dsh"));
  async function prepareAndStart() {
    if (cancelRequested || !snapshot) return;
    if (!installedNode()) {
      const available = snapshot.nodes?.find(n => n.installed && n.verified);
      if (available) selectedNode = available.id;
      else {
        snapshot = await ManagerService.InstallLatestNode();
        const installed = snapshot.nodes?.find(n => n.version === snapshot?.latestNode?.version && n.installed && n.verified);
        if (installed) selectedNode = installed.id;
      }
    }
    if (cancelRequested) return;
    if (!installedRuntime()) {
      const version = selectedVersion();
      if (!version) throw new Error("No DSH version selected");
      progressMessage.textContent = t("startup.fetchingReleases");
      await ManagerService.RefreshDSHReleases();
      if (cancelRequested) return;
      snapshot = await ManagerService.InstallRuntime(version);
    }
    if (cancelRequested || !snapshot) return;
    acquiring = false; acquisitionKind = undefined;
    progressMessage.textContent = t("host.titleStarting"); progressBar.removeAttribute("value");
    const target = {profile: snapshot.configured?.profile ?? {dataDirectoryId: "dsh-work", name: "web"}, runtimeId: selectedRuntime, node: selectedNode === "system" ? {kind: NodeSelectionKind.NodeSelectionSystem} : {kind: NodeSelectionKind.NodeSelectionManaged, installationId: selectedNode}};
    starting = ManagerService.SetRunContext(target);
    try { await starting; } finally { starting = undefined; }
  }
  retry.addEventListener("click", () => void action(connectionFailed ? initialize : prepareAndStart, connectionFailed ? undefined : !installedNode() ? "node" : !installedRuntime() ? "dsh" : undefined));
  cancel.addEventListener("click", () => {
    cancelRequested = true; render();
    void (async () => {
      if (starting) starting.cancel();
      else if (acquiring) await ManagerService.CancelRuntime();
      else status = await HostService.Cancel() as LifecycleStatus;
      render();
    })().catch(error => { cancelRequested = false; showError(error); render(); });
  });
  element("open-runtime-settings").addEventListener("click", () => void HostService.OpenRuntimeSettings().catch(showError));
  element("quit").addEventListener("click", () => void (async () => {
    if (acquiring) await ManagerService.CancelRuntime();
    await HostService.Quit();
  })().catch(showError));
  element("show-startup-log").addEventListener("click", () => { logsRequested = !logsRequested; render(); });
  restoreButton.addEventListener("click", () => { versionPoints.closePreview(); restoreDialog.showModal(); });
  element("close-startup-restore").addEventListener("click", () => restoreDialog.close());
  copy.addEventListener("click", () => void (async () => {
    await poll();
    const runtime = snapshot?.runtimes?.find(r => r.id === selectedRuntime);
    const nodeInfo = selectedNode === "system" ? snapshot?.systemNode : snapshot?.nodes?.find(n => n.id === selectedNode);
    const attempt = status.launchSelection;
    const report = ["DSH Work startup diagnostics", new Date().toISOString(),
      `Lifecycle: ${status.state} / ${status.phase}`, `Attempt: ${status.correlationId ?? status.generationId ?? ""}`,
      `DSH: ${attempt?.runtimeVersion ?? runtime?.version ?? selectedVersion()} | ${attempt?.runtimePath ?? runtime?.path ?? ""}`,
      `Node: ${attempt?.nodeVersion ?? nodeInfo?.version ?? selectedNode} | ${attempt?.nodePath ?? nodeInfo?.nodePath ?? ""}`,
      `Profile: ${startupProfileName(status, snapshot?.configured?.profile.name) ?? ""}`,
      `DSH_HOME: ${attempt?.dataDirectoryPath ?? snapshot?.dataDirectories?.find(d => d.id === snapshot?.configured?.profile.dataDirectoryId)?.path ?? ""}`,
      status.error ? JSON.stringify(status.error, null, 2) : "", lastError,
      logs.node.text(), logs.dsh.text(), output.textContent ?? ""].filter(Boolean).join("\n\n");
    await navigator.clipboard.writeText(report);
    element("diagnostic-copy-status").textContent = t("common.copied");
  })().catch(() => { element("diagnostic-copy-status").textContent = t("common.copyFailed"); }));

  const offAcquisition = Events.On("acquisition", event => {
    const update = event.data as OperationStatus;
    if (update.artifact.kind === "plugin") return;
    const kind = update.artifact.kind === "node" ? "node" : "dsh";
    if (busy && update.state === "active") acquisitionKind = kind;
    panels[kind].hidden = false;
    if (update.log) { logs[kind].append(update.operationId, update.log); return; }
    let text = runtimePreparationText(acquisitionPreparation(update));
    if (update.artifact.version) text += ` · ${update.artifact.version}`;
    if (update.route) text += ` · ${t(`value.${update.route}Source`)}`;
    if (update.receivedBytes) text += ` · ${formatRuntimeBytes(update.receivedBytes)}${update.hasTotal ? ` / ${formatRuntimeBytes(update.totalBytes ?? 0)}` : ""}`;
    panels[kind].querySelector("p")!.textContent = text;
    if (busy && update.state === "active") {
      progressMessage.textContent = text;
      if (update.hasTotal && update.totalBytes) { progressBar.max = update.totalBytes; progressBar.value = update.receivedBytes ?? 0; }
      else progressBar.removeAttribute("value");
    }
    const progress = panels[kind].querySelector("progress")!;
    progress.hidden = update.state !== "active";
    if (update.hasTotal && update.totalBytes) { progress.max = update.totalBytes; progress.value = update.receivedBytes ?? 0; }
    else progress.removeAttribute("value");
    const bucket = update.hasTotal && update.totalBytes ? Math.floor((update.receivedBytes ?? 0) / update.totalBytes * 10) : Math.floor((update.receivedBytes ?? 0) / (5 * 1024 * 1024));
    logs[kind].append(update.operationId, text, update.state === "failed", `${update.state}/${update.step}/${update.route}/${bucket}`);
  });
  const offLifecycle = Events.On("lifecycle", event => {
    status = event.data as LifecycleStatus;
    if (status.state === "Starting") lastPhase = status.phase;
    render();
    if (!active()) void refreshEnvironment().catch(showError);
  });
  // Keep observing later retries and recovery attempts; avoid overlapping calls.
  async function poll() {
    if (polling || disposed || connectionFailed || document.hidden || status.state === "Ready") return;
    polling = true;
    try {
      const data = await HostService.GetStartupOutput();
      const text = [!busy && status.error && [status.error.code, status.error.summary, status.error.detail].filter(Boolean).join("\n"), lastError, logs.node.text(), logs.dsh.text(), data.stdout && `[stdout]\n${data.stdout}`, data.stderr && `[stderr]\n${data.stderr}`].filter(Boolean).join("\n\n");
      const follows = output.scrollHeight - output.scrollTop - output.clientHeight < 32;
      if (output.textContent !== text) { output.textContent = text || t("host.noOutput"); if (follows && active()) output.scrollTop = output.scrollHeight; }
      await refreshEnvironment();
    } catch (error) { console.error("Startup output unavailable", error); }
    finally { polling = false; }
  }
  const timer = window.setInterval(() => void poll(), 500);
  const offLocale = subscribeLocale(() => { renderEnvironment(); Object.values(logs).forEach(log => log.labels()); });
  window.addEventListener("pagehide", () => { disposed = true; clearInterval(timer); offAcquisition(); offLifecycle(); offLocale(); }, {once: true});
  window.addEventListener("focus", () => void refreshEnvironment().catch(showError));
  render();
  async function initialize() {
    try {
      applyLocale(await HostService.GetLocale());
      status = await HostService.GetStatus() as LifecycleStatus;
      await refreshEnvironment();
      connectionFailed = false; lastError = ""; render();
      void ManagerService.RefreshDSHReleases().then(next => { snapshot = next; renderEnvironment(); }).catch(showError);
      await poll();
    } catch {
      connectionFailed = true; showError(new Error(t("startup.readError"))); render();
    }
  }
  void initialize();
}
