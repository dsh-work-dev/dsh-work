import {transientStatus} from "./ui/transient-status";
import {mountRestorePoints} from "./restore-points";
import {runtimePreparationText, acquisitionPreparation, formatRuntimeBytes} from "./acquisition-view";
import {Events} from "@wailsio/runtime";

import {HostService, ManagerService} from "../bindings/github.com/local/dsh-work/internal/app";
import type {OperationStatus} from "../bindings/github.com/local/dsh-work/internal/acquisition/models";
import {NodeSelectionKind, type DataDirectoryInfo, type PluginInfo, type PluginResult, type ProfileInfo, type ProfileRef, type RunContext, type RuntimeInfo, type Snapshot} from "../bindings/github.com/local/dsh-work/internal/dshmanager";
import {mountOperationLog} from "./operation-log";
import {mountStorage} from "./storage";
import {mountRecovery} from "./recovery";
import {mountNotifications, mountSettings} from "./settings";
import {mountPets} from "./pets";
import {buildOverviewModel, sameRunContext, type OverviewLane} from "./overview";
import {applyTheme} from "./theme";
import {subscribeLocale, t} from "./i18n";
import type {RuntimePreparation} from "./lifecycle";
import type {Status as HostLifecycleStatus} from "../bindings/github.com/local/dsh-work/internal/lifecycle/models";

export type ManagerProfileRef = ProfileRef;
export type ManagerRunContext = RunContext;
export type ManagerPlugin = PluginInfo;
export type ManagerProfile = ProfileInfo;
export type ManagerRuntime = RuntimeInfo;
export type ManagerDataDirectory = DataDirectoryInfo;
export type ManagerSnapshot = Snapshot;
export type ManagerPluginResult = PluginResult;

const getSnapshot = ManagerService.GetSnapshot;
const getTheme = ManagerService.GetTheme;
const getHostStatus = HostService.GetWorkspaceStatus;
const setRunContext = ManagerService.SetRunContext;
const installPlugin = ManagerService.InstallPlugin;
const listPlugins = ManagerService.ListPlugins;
const removePlugin = ManagerService.RemovePlugin;
const upgradePlugin = ManagerService.UpgradePlugin;
const renameProfile = ManagerService.RenameProfile;
const cloneProfile = ManagerService.CloneProfile;
const deleteProfile = ManagerService.DeleteProfile;
const backupProfile = ManagerService.BackupProfile;
const installRuntime = ManagerService.InstallRuntime;
const cancelRuntime = ManagerService.CancelRuntime;
const removeRuntime = ManagerService.RemoveRuntime;
const refreshDSHReleases = ManagerService.RefreshDSHReleases;
const installLatestNode = ManagerService.InstallLatestNode;
const removeNode = ManagerService.RemoveNode;
const restoreKnownGood = ManagerService.RestoreKnownGood;

function managerErrorMessage(error: unknown, fallbackKey: string): string {
  void error;
  return t(fallbackKey);
}

function switchReason(code: string): string {
  if (code === "RUNTIME_PROFILE_INCOMPATIBLE") return t("switch.incompatible");
  if (code === "DSH_EARLY_EXIT") return t("switch.earlyExit");
  if (code === "PROFILE_PREPARATION_FAILED") return t("switch.preparationFailed");
  return t("error.switchRunContext");
}

function profileKindLabel(value: string): string {
  if (value === "built-in") {
    return t("value.builtIn");
  }
  if (value === "custom") {
    return t("value.custom");
  }
  return t("value.other");
}

function runtimeSourceLabel(value: string): string {
  if (value === "managed") {
    return t("value.installed");
  }
  if (value === "development-fixture") {
    return t("value.development");
  }
  if (value === "system") {
    return t("value.system");
  }
  return t("value.other");
}

function runtimeArtifactSourceLabel(value: string): string {
  if (value === "official") {
    return t("value.officialSource");
  }
  if (value === "mirror") {
    return t("value.mirrorSource");
  }
  if (value === "local") {
    return t("value.localSource");
  }
  return t("value.unknownSource");
}

function pluginSourceLabel(plugin: PluginInfo): string {
  if (plugin.sourceKind === "public-registry") {
    return runtimeArtifactSourceLabel(plugin.successfulRoute ?? "");
  }
  if (plugin.sourceKind === "private-registry") return t("value.customRegistry");
  if (plugin.sourceKind === "git") return t("value.gitSource");
  if (plugin.sourceKind === "url") return t("value.urlSource");
  if (plugin.sourceKind === "local") return t("value.localSource");
  return t("value.unknownSource");
}

export function runtimePreparationArtifactKind(preparation: Pick<RuntimePreparation, "artifactKind" | "state" | "operation">): "node" | "dsh" {
  if (preparation.artifactKind === "node" || preparation.state === "acquiring-node" || preparation.operation === "download-node") {
    return "node";
  }
  return "dsh";
}

export function runtimePreparationProgressPercent(preparation: Pick<RuntimePreparation, "hasTotal" | "receivedBytes" | "totalBytes" | "state">): number | undefined {
  if (preparation.hasTotal && (preparation.totalBytes ?? 0) > 0) {
    return Math.min(100, Math.round((Math.max(0, preparation.receivedBytes ?? 0) / (preparation.totalBytes ?? 1)) * 100));
  }
  return preparation.state === "installed" ? 100 : undefined;
}

type ManagerSection = "overview" | "profiles" | "plugins" | "runtimes" | "data-directories" | "settings" | "notifications" | "pets" | "about";

function sectionName(value: string | null): ManagerSection {
  if (value === "backups") return "profiles";
  if (value === "overview" || value === "profiles" || value === "plugins" || value === "runtimes" || value === "data-directories" || value === "settings" || value === "notifications" || value === "pets" || value === "about") {
    return value;
  }
  return "overview";
}

export function mountManager() {
  const runtime = document.getElementById("manager-runtime") as HTMLSelectElement;
  const node = document.getElementById("manager-node") as HTMLSelectElement;
  const dataDirectory = document.getElementById("manager-data-directory") as HTMLSelectElement;
  const profile = document.getElementById("manager-profile") as HTMLSelectElement;
  const packageInput = document.getElementById("manager-plugin-package") as HTMLInputElement;
  const install = document.getElementById("manager-plugin-install") as HTMLButtonElement;
  const runtimeVersion = document.getElementById("manager-runtime-version") as HTMLSelectElement;
  const installRuntimeButton = document.getElementById("manager-runtime-install") as HTMLButtonElement;
  const refreshDSHButton = document.getElementById("manager-dsh-refresh") as HTMLButtonElement;
  let releasesLoading = false;
  const installNodeButton = document.getElementById("manager-node-install") as HTMLButtonElement;
  const dshPreparationPanel = document.getElementById("manager-dsh-preparation") as HTMLElement;
  const dshPreparationDetail = document.getElementById("manager-dsh-preparation-detail") as HTMLParagraphElement;
  const dshPreparationProgress = document.getElementById("manager-dsh-preparation-progress") as HTMLDivElement;
  const dshPreparationProgressFill = document.getElementById("manager-dsh-preparation-progress-fill") as HTMLSpanElement;
  const dshPreparationMeta = document.getElementById("manager-dsh-preparation-meta") as HTMLParagraphElement;
  const nodePreparationPanel = document.getElementById("manager-node-preparation") as HTMLElement;
  const nodePreparationDetail = document.getElementById("manager-node-preparation-detail") as HTMLParagraphElement;
  const nodePreparationProgress = document.getElementById("manager-node-preparation-progress") as HTMLDivElement;
  const nodePreparationProgressFill = document.getElementById("manager-node-preparation-progress-fill") as HTMLSpanElement;
  const nodePreparationMeta = document.getElementById("manager-node-preparation-meta") as HTMLParagraphElement;
  const cancelRuntimeButtons = [
    document.getElementById("manager-dsh-cancel") as HTMLButtonElement,
    document.getElementById("manager-node-cancel") as HTMLButtonElement
  ];
  const managerTitle = document.getElementById("manager-title") as HTMLHeadingElement;
  const currentRuntime = document.getElementById("manager-current-runtime") as HTMLElement;
  const currentDataDirectory = document.getElementById("manager-current-data-directory") as HTMLElement;
  const currentProfile = document.getElementById("manager-current-profile") as HTMLElement;
  const currentNode = document.getElementById("manager-current-node") as HTMLElement;
  const currentState = document.getElementById("manager-current-state") as HTMLElement;
  const configuredContext = document.getElementById("manager-configured-context") as HTMLElement;
  const configuredRuntime = document.getElementById("manager-configured-runtime") as HTMLElement;
  const configuredDataDirectory = document.getElementById("manager-configured-data-directory") as HTMLElement;
  const configuredProfile = document.getElementById("manager-configured-profile") as HTMLElement;
  const knownGoodContext = document.getElementById("manager-known-good-context") as HTMLElement;
  const knownGoodRuntime = document.getElementById("manager-known-good-runtime") as HTMLElement;
  const knownGoodDataDirectory = document.getElementById("manager-known-good-data-directory") as HTMLElement;
  const knownGoodProfile = document.getElementById("manager-known-good-profile") as HTMLElement;
  const selectedProfileLabel = document.getElementById("manager-selected-profile") as HTMLElement;
  const profileScopeNote = document.getElementById("manager-profile-scope-note") as HTMLParagraphElement;
  const profileEmpty = document.getElementById("manager-profile-empty") as HTMLElement;
  const profileDetail = document.getElementById("manager-profile-detail") as HTMLElement;
  const profileName = document.getElementById("manager-profile-name") as HTMLInputElement;
  const profileRename = document.getElementById("manager-profile-rename") as HTMLButtonElement;
  const profileClone = document.getElementById("manager-profile-clone") as HTMLButtonElement;
  let exportInFlight = false;
  const profileExport = document.getElementById("manager-profile-export") as HTMLButtonElement;
  const renameControls = document.getElementById("profile-rename-controls") as HTMLElement;
  const profileBackup = document.getElementById("manager-profile-backup") as HTMLButtonElement;
  const profileDelete = document.getElementById("manager-profile-delete") as HTMLButtonElement;
  const pluginEmpty = document.getElementById("manager-plugin-empty") as HTMLElement;
  const pluginDetail = document.getElementById("manager-plugin-detail") as HTMLElement;
  const pluginProfileLabel = document.getElementById("manager-plugin-profile") as HTMLElement;
  const pluginScopeNote = document.getElementById("manager-plugin-scope-note") as HTMLParagraphElement;
  const profilePlugins = document.getElementById("manager-profile-plugins") as HTMLDivElement;
  const profileList = document.getElementById("manager-profiles") as HTMLDivElement;
  const runtimeList = document.getElementById("manager-runtimes") as HTMLDivElement;
  const nodeList = document.getElementById("manager-nodes") as HTMLDivElement;
  const switchFailure = document.getElementById("manager-switch-failure") as HTMLElement;
  const switchFailureDetail = document.getElementById("manager-switch-failure-detail") as HTMLParagraphElement;
  const switchRetry = document.getElementById("manager-switch-retry") as HTMLButtonElement;
  const switchRestore = document.getElementById("manager-switch-restore") as HTMLButtonElement;
  const sectionPicker = document.getElementById("manager-section-select") as HTMLSelectElement;
  sectionPicker.addEventListener("change", () => showSection(sectionName(sectionPicker.value)));
  const navItems = Array.from(document.querySelectorAll<HTMLButtonElement>("[data-manager-section]"));
  const panels = Array.from(document.querySelectorAll<HTMLElement>("[data-manager-panel]"));
  const query = new URLSearchParams(window.location.search);
  const initialDataDirectory = query.get("data-directory")?.trim();
  const initialProfile = query.get("profile")?.trim();
  const initialRuntimeVersion = query.get("version")?.trim();
  let currentSection = sectionName(query.get("section"));
  let snapshot: ManagerSnapshot | undefined;
  let hostStatus: HostLifecycleStatus | undefined;
  let managedProfile: ManagerProfileRef | undefined = initialDataDirectory && initialProfile
    ? {dataDirectoryId: initialDataDirectory, name: initialProfile}
    : undefined;
  let themeSyncTimer: number | undefined;
  let themeSyncAvailable = false;
  let contextSwitchInFlight = false;
  let runtimeInstallInFlight = false;
  let runtimeRemovalInFlight = false;
  let runtimeCancelPending = false;
  let activeRuntimeKind: "node" | "dsh" = "dsh";
  let runtimePreparations: Partial<Record<"node" | "dsh", RuntimePreparation>> = {};

  if (initialRuntimeVersion) {
    runtimeVersion.value = initialRuntimeVersion;
  }

  const sectionCopy: Record<ManagerSection, {title: string}> = {
    overview: {title: "manager.overview"},
    settings: {title: "manager.general"},
    profiles: {title: "manager.profiles"},
    plugins: {title: "manager.plugins"},
    runtimes: {title: "manager.runtimes"},
    "data-directories": {title: "manager.dataDirectories"},
    notifications: {title: "manager.notifications"},
    pets: {title: "manager.pets"},
    about: {title: "manager.about"}
  };

  // Feedback belongs to the initiating panel, including asynchronous failures.
  function feedbackFor(section: ManagerSection, scope?: HTMLElement) {
    const panel = scope ?? panels.find(item => item.dataset.managerPanel === section)!;
    const result = document.createElement("p");
    result.className = "panel-result";
    result.setAttribute("role", "status");
    result.hidden = true;
    (section === "profiles" ? document.getElementById("manager-profile-detail")! : panel).append(result);

    let anchor: Element | null = null;
    const clear = () => { result.hidden = true; result.textContent = ""; };
    panel.addEventListener("change", event => {
      clear(); anchor = (event.target as Element).closest(".setting-row, .runtime-install-card, .plugin-install-row") ?? event.target as Element;
    }, true);
    panel.addEventListener("click", event => {
      const button = (event.target as Element).closest("button");
      if (button) {
        clear();
        anchor = button.closest(".manager-list-item, .profile-actions, .profile-rename-row, .plugin-install-row, .runtime-install-card, .manager-actions, .setting-row") ?? button;
      }
    }, true);
    return (message: string, tone: "neutral" | "success" | "error" = "neutral", persistent = false) => {
      // Controls already show successful changes. Only failures and actionable
      // results (backup filename or required restart) need accompanying text.
      if (!message) { clear(); return; }
      if (tone !== "error" && !persistent) { clear(); return; }
      if (anchor?.isConnected && panel.contains(anchor)) anchor.after(result);
      else panel.append(result);
      result.textContent = message;
      result.hidden = false;
    };
  }
  const setFeedback = feedbackFor("overview");
  const settingsFeedback = feedbackFor("settings");
  const profilesFeedback = feedbackFor("profiles");
  const versionPoints = mountRestorePoints(document.getElementById("version-restore-points")!, next => {snapshot=next;renderSelection();renderOverview();renderProfiles();renderRuntimes();});
  const recovery = mountRecovery((next, restored) => {
    snapshot = next;
    if (restored) { managedProfile = {...restored}; renameControls.hidden = true; }
    renderSelection(); renderOverview(); renderProfiles(); renderRuntimes();
    if (restored) { selectedProfileLabel.tabIndex = -1; selectedProfileLabel.focus(); }
  });
  const pluginsFeedback = feedbackFor("plugins");
  const runtimesFeedback = feedbackFor("runtimes", document.getElementById("manager-dsh-group")!);
  const nodeFeedback = feedbackFor("runtimes", document.getElementById("manager-node-group")!);
  const settings = mountSettings(settingsFeedback);
  mountStorage();
  const notifications = mountNotifications(feedbackFor("notifications"));
  const pets = mountPets();

  async function syncTheme() {
    if (!themeSyncAvailable) {
      return;
    }
    try {
      applyTheme(await getTheme());
    } catch (error) {
      console.error("Could not read DSH theme preference", error);
    }
  }

  function startThemeSync() {
    if (themeSyncTimer !== undefined) {
      return;
    }
    themeSyncTimer = window.setInterval(() => void syncTheme(), 1000);
  }

  const sectionPositions = new Map<ManagerSection, number>();
  const sectionFocus = new Map<ManagerSection, {element: HTMLElement; selector?: string}>();
  const contentScroller = document.getElementById("manager-content")!;
  contentScroller.addEventListener("focusin", event => {
    const target = event.target as HTMLElement;
    const panel = target.closest<HTMLElement>("[data-manager-panel]");
    if (panel) {
      let selector = target.id ? `#${CSS.escape(target.id)}` : undefined;
      for (const key of ["data-pet-key", "data-profile-key"]) {
        const value = target.closest<HTMLElement>(`[${key}]`)?.getAttribute(key);
        if (!selector && value) selector = `[${key}="${CSS.escape(value)}"]`;
      }
      sectionFocus.set(sectionName(panel.dataset.managerPanel ?? null), {element: target, selector});
    }
  });
  function showSection(next: ManagerSection) {
    const changed = next !== currentSection;
    if (changed) sectionPositions.set(currentSection, contentScroller.scrollTop);
    currentSection = next;
    const sharedState = document.querySelector<HTMLElement>(".manager-load-state");
    if (sharedState) sharedState.style.display = ["overview", "profiles", "plugins", "runtimes"].includes(next) ? "" : "none";
    managerTitle.textContent = t(sectionCopy[next].title);
    sectionPicker.value = next;
    for (const panel of panels) {
      panel.hidden = panel.dataset.managerPanel !== next;
    }
    for (const item of navItems) {
      const selected = item.dataset.managerSection === next;
      item.classList.toggle("is-active", selected);
      if (selected) {
        item.setAttribute("aria-current", "page");
      } else {
        item.removeAttribute("aria-current");
      }
    }
    if (changed) {
      contentScroller.scrollTop = sectionPositions.get(next) ?? 0;
      const saved = sectionFocus.get(next);
      const previous = saved?.element.isConnected ? saved.element : saved?.selector ? document.querySelector<HTMLElement>(saved.selector) : null;
      if (previous?.isConnected && previous.getClientRects().length && !(previous as HTMLButtonElement).disabled) previous.focus({preventScroll: true});
    }
  }

  const stickyPanels = Array.from(document.querySelectorAll<HTMLElement>(".profile-editor, .pets-preview-panel"));
  const positionProfileEditor = () => {
    for (const panel of stickyPanels) panel.style.top = `${Math.min(0, window.innerHeight - 48 - panel.offsetHeight)}px`;
  };
  const profileResize = new ResizeObserver(positionProfileEditor);
  stickyPanels.forEach(panel => profileResize.observe(panel));
  window.addEventListener("resize", positionProfileEditor);
  window.addEventListener("pagehide", () => { profileResize.disconnect(); window.removeEventListener("resize", positionProfileEditor); }, {once: true});
  const environmentEditor = document.getElementById("environment-editor")!;
  const environmentApply = document.getElementById("environment-apply") as HTMLButtonElement;
  let editingEnvironment = false;
  function editEnvironment(target?: ManagerRunContext) {
    if (!snapshot || mutationBlocked()) return;
    editingEnvironment = false;
    renderSelection();
    if (target) {
      runtime.value = target.runtimeId;
      node.value = target.node.kind === "system" ? "system" : target.node.installationId ?? "";
      dataDirectory.value = target.profile.dataDirectoryId;
      fillProfiles(target.profile);
    }
    editingEnvironment = true;
    environmentEditor.hidden = false;
    showSection("overview");
    runtime.focus();
  }
  document.getElementById("environment-edit")!.addEventListener("click", () => editEnvironment());
  document.getElementById("environment-cancel")!.addEventListener("click", () => {
    editingEnvironment = false; environmentEditor.hidden = true; renderSelection();
    document.getElementById("environment-edit")!.focus();
  });
  environmentApply.addEventListener("click", () => void switchRunContext());
  document.getElementById("known-good-return")!.addEventListener("click", () => { if (snapshot?.knownGood) editEnvironment(snapshot.knownGood); });
  document.getElementById("plugins-restart")!.addEventListener("click", () => { if (snapshot?.current) editEnvironment(snapshot.current); });
  const renameStart = document.getElementById("profile-rename-start") as HTMLButtonElement;
  const finishRename = () => { renameControls.hidden = true; renameStart.focus(); };
  renameStart.addEventListener("click", () => { renameControls.hidden = false; profileName.focus(); profileName.select(); });
  document.getElementById("profile-rename-cancel")!.addEventListener("click", finishRename);
  profileName.addEventListener("keydown", event => {
    if (event.key === "Escape") finishRename();
    if (event.key === "Enter" && !profileRename.disabled) profileRename.click();
  });
  document.getElementById("profile-back-to-list")!.addEventListener("click", () => {
    const selected = profileList.querySelector<HTMLButtonElement>("[aria-pressed=true]");
    selected?.focus();
  });
  document.getElementById("profile-switch")!.addEventListener("click", () => { if (managedProfile) void switchToProfile(managedProfile); });
  document.getElementById("profile-plugins-link")!.addEventListener("click", () => showSection("plugins"));
  document.getElementById("plugins-profile-link")!.addEventListener("click", () => {
    if (snapshot?.current) selectProfile(snapshot.current.profile); else showSection("profiles");
  });
  const loadState = document.createElement("div");
  loadState.className = "manager-load-state";
  loadState.setAttribute("role", "status");
  const loadMessage = document.createElement("span");
  const loadRetry = document.createElement("button");
  loadRetry.className = "button button-secondary"; loadRetry.type = "button";
  loadRetry.textContent = t("common.retry"); loadRetry.hidden = true;
  loadMessage.textContent = t("view.loading");
  loadState.append(loadMessage, loadRetry);
  document.querySelector(".manager-topbar")!.after(loadState);
  loadRetry.addEventListener("click", () => void refresh());
  document.getElementById("about-version")!.textContent = `dsh-work ${__APP_VERSION__}`;
  document.getElementById("about-copy")!.addEventListener("click", () => void (async () => {
    const result = document.getElementById("about-result")!;
    try {
      await navigator.clipboard.writeText(JSON.stringify({version: __APP_VERSION__, state: hostStatus?.state, current: snapshot?.current, failure: snapshot?.lastSwitchAttempt}, null, 2));
      transientStatus(result, t("logs.copied"));
    } catch { result.textContent = t("logs.copyFailed"); }
  })());

  function sameProfileRef(left: ManagerProfileRef | undefined, right: ManagerProfileRef | undefined): boolean {
    return !!left && !!right && left.dataDirectoryId === right.dataDirectoryId && left.name === right.name;
  }

  function managedProfileItem(): ManagerProfile | undefined {
    if (!managedProfile) {
      return undefined;
    }
    return (snapshot?.profiles ?? []).find((item) => sameProfileRef(item.ref, managedProfile));
  }

  function currentProfileItem(): ManagerProfile | undefined {
    const current = currentRunContext();
    if (!current) {
      return undefined;
    }
    return (snapshot?.profiles ?? []).find((item) => sameProfileRef(item.ref, current.profile));
  }

  function currentRunContext(): ManagerRunContext | undefined {
    return snapshot?.current ?? undefined;
  }

  function mutationBlocked(): boolean {
    return contextSwitchInFlight || runtimeInstallInFlight || runtimeRemovalInFlight;
  }

  function canMutateProfile(ref: ManagerProfileRef | undefined): boolean {
    const current = currentRunContext();
    return !mutationBlocked() && !!ref && !!current && hostStatus?.state === "Ready" && sameProfileRef(ref, current.profile);
  }

  function dataDirectoryLabel(dataDirectoryId: string): string {
	if (dataDirectoryId === "dsh-work") return "DSH Work";
    return (snapshot?.dataDirectories ?? []).find((item) => item.id === dataDirectoryId)?.name ?? dataDirectoryId;
  }

  function renderOverviewLane(lane: OverviewLane, fields: {runtime: HTMLElement; dataDirectory: HTMLElement; profile: HTMLElement}) {
    fields.runtime.textContent = lane.runtime
      ? `${lane.runtime.version} · ${t(lane.runtime.installed ? "value.verified" : "value.unverified")}`
      : lane.target?.runtimeId || t("value.noRuntime");
    fields.dataDirectory.textContent = lane.dataDirectory?.name ?? lane.target?.profile.dataDirectoryId ?? t("value.noDataDirectory");
    fields.profile.textContent = lane.target?.profile.name ?? t("value.noProfile");
  }

  function renderOverview() {
    recovery.render(snapshot, mutationBlocked());
    versionPoints.render(snapshot, mutationBlocked());
    if (!snapshot) {
      return;
    }
    const model = buildOverviewModel(snapshot);
    renderOverviewLane(model.current, {
      runtime: currentRuntime,
      dataDirectory: currentDataDirectory,
      profile: currentProfile
    });
    const target = model.current.target;
    const selection = hostStatus?.launchSelection;
    const matchingLaunch = target && selection?.runtimeId === target.runtimeId && selection.profileName === target.profile.name
      && selection.nodeId === (target.node.kind === "system" ? "system" : target.node.installationId);
    const nodeVersion = target ? matchingLaunch && selection?.nodeVersion || (target.node.kind === "system"
      ? snapshot.systemNode?.version
      : snapshot.nodes?.find(node => node.id === target.node.installationId)?.version) : undefined;
    currentNode.textContent = nodeVersion ? `${nodeVersion} · ${t(target?.node.kind === "system" ? "value.system" : "value.installed")}` : "—";
    const stateKey = hostStatus?.state === "Starting" || hostStatus?.state === "Stopping"
      ? "value.switching"
      : hostStatus?.state === "Failed"
        ? "value.failed"
        : {
          active: "value.active",
          "not-running": "value.notRunning",
          "no-context": "value.noRunContext"
        }[model.state];
    currentState.textContent = t(stateKey);
    configuredContext.hidden = !model.configured || sameRunContext(model.current.target, model.configured.target);
    if (model.configured) {
      renderOverviewLane(model.configured, {
        runtime: configuredRuntime,
        dataDirectory: configuredDataDirectory,
        profile: configuredProfile
      });
    }
    knownGoodContext.hidden = !model.knownGood || sameRunContext(model.current.target, model.knownGood.target);
    const knownReturn = document.getElementById("known-good-return") as HTMLButtonElement;
    knownReturn.hidden = !model.knownGood || sameRunContext(model.current.target, model.knownGood.target);
    knownReturn.disabled = mutationBlocked();
    if (model.knownGood) {
      renderOverviewLane(model.knownGood, {
        runtime: knownGoodRuntime,
        dataDirectory: knownGoodDataDirectory,
        profile: knownGoodProfile
      });
    }
    const attempt = snapshot.lastSwitchAttempt;
    switchFailure.hidden = !attempt;
    if (attempt) {
      const targetProfile = snapshot.profiles?.find(item => sameProfileRef(item.ref, attempt.target.profile));
      const reason = targetProfile?.launchable === false ? t("switch.incompatible") : switchReason(attempt.failure.code);
      switchFailureDetail.textContent = `${t("switch.failedTarget", {profile: attempt.target.profile.name})} ${reason} ${snapshot.current ? t("switch.stillRunning", {profile: snapshot.current.profile.name}) : t("switch.stopped")}`;
      const diagnostic = document.getElementById("manager-switch-diagnostic")!;
      const report = JSON.stringify(attempt, null, 2);
      if (diagnostic.textContent !== report) diagnostic.closest("details")!.open = true;
      diagnostic.textContent = report;
      switchRetry.hidden = !attempt.failure.retryable || targetProfile?.launchable === false;
      switchRestore.hidden = !!snapshot.current || !snapshot.knownGood || attempt.rollback === "restored";
    }
    if (hostStatus?.state === "Failed" && hostStatus.error) {
      setFeedback(t("status.failureFallback"), "error");
    }
  }

  function syncManagedProfile(preferred?: ManagerProfileRef) {
    const profiles = snapshot?.profiles ?? [];
    const candidate = preferred ?? managedProfile ?? snapshot?.current?.profile ?? snapshot?.configured?.profile;
    const match = candidate && profiles.find((item) => sameProfileRef(item.ref, candidate));
    if (match) {
      managedProfile = {...match.ref};
      return;
    }
    managedProfile = undefined;
  }

  function renderPluginList(item: ManagerProfile, mutable: boolean) {
    profilePlugins.replaceChildren();
    const plugins = item.plugins ?? [];
    if (plugins.length === 0) {
      const empty = document.createElement("p");
      empty.className = "manager-empty";
      empty.textContent = item.exists ? t("profiles.noPlugins") : t("value.notInitialized");
      profilePlugins.append(empty);
      return;
    }
    for (const plugin of plugins) {
      const row = document.createElement("div");
      row.className = "plugin-list-item";
      const text = document.createElement("div");
      const name = document.createElement("strong");
      name.textContent = plugin.name;
      const detail = document.createElement("span");
      const currentVersion = plugin.currentVersion || plugin.version;
      detail.textContent = [
        pluginSourceLabel(plugin),
        currentVersion ? t("value.currentVersion", {version: currentVersion}) : t("value.installed"),
        plugin.availableVersion ? t("value.availableVersion", {version: plugin.availableVersion}) : "",
      ].filter(Boolean).join(" · ");
      if (!item.launchable) detail.textContent = t("profiles.noDesktop");
      text.append(name, detail);
      if (mutable) {
        if (plugin.updateCheck === "available") {
          const upgradeButton = document.createElement("button");
          upgradeButton.className = "button button-primary";
          upgradeButton.type = "button";
          upgradeButton.textContent = t("action.upgrade");
          upgradeButton.dataset.pluginRemove = "true";
          upgradeButton.addEventListener("click", () => void mutatePlugin("upgrade", plugin.package || plugin.name, upgradeButton));
          row.append(text, upgradeButton);
        }
        const removeButton = document.createElement("button");
        removeButton.className = "button button-secondary";
        removeButton.type = "button";
        removeButton.textContent = t("action.remove");
        removeButton.dataset.pluginRemove = "true";
        removeButton.addEventListener("click", () => void mutatePlugin("remove", plugin.package || plugin.name, removeButton));
        if (!row.contains(text)) {
          row.append(text);
        }
        row.append(removeButton);
      } else {
        row.append(text);
      }
      profilePlugins.append(row);
    }
  }

  function renderProfileDetail() {
    recovery.selectProfile(managedProfile);
    const item = managedProfileItem();
    profileEmpty.hidden = !!item;
    profileDetail.hidden = !item;
    if (!item) {
      profileName.value = "";
      profileClone.disabled = true;
      profileBackup.disabled = true;
      profileExport.disabled = true;
      profileDelete.disabled = true;
      return;
    }
    selectedProfileLabel.textContent = item.ref.name;
    const current = currentRunContext();
    const isCurrent = !!current && sameProfileRef(item.ref, current.profile);
    const canRename = item.renamable && !isCurrent && !mutationBlocked();
    profileScopeNote.textContent = [profileKindLabel(item.kind), isCurrent ? t("profiles.current") : ""].filter(Boolean).join(" · ");
    renameStart.hidden = !item.renamable;
    renameStart.disabled = !canRename;
    const switchProfile = document.getElementById("profile-switch") as HTMLButtonElement;
    switchProfile.hidden = isCurrent;
    switchProfile.disabled = !item.launchable || mutationBlocked();
    document.getElementById("profile-plugins-link")!.hidden = !isCurrent;
    if (renameControls.hidden) profileName.value = item.ref.name;
    profileName.disabled = !canRename;
    profileRename.disabled = !canRename;
    profileClone.disabled = (!item.exists && !item.autoInitialize) || mutationBlocked();
    profileBackup.disabled = !item.exists || mutationBlocked();
    profileExport.disabled = !item.exists || mutationBlocked() || exportInFlight;
    profileDelete.disabled = !item.deletable || mutationBlocked();
  }

  function renderPluginPanel() {
    const item = currentProfileItem();
    pluginEmpty.hidden = !!item;
    pluginDetail.hidden = !item;
    if (!item) {
      pluginProfileLabel.textContent = "—";
      profilePlugins.replaceChildren();
      return;
    }
    pluginProfileLabel.textContent = item.ref.name;
    const mutable = canMutateProfile(item.ref);
    pluginScopeNote.hidden = mutable || mutationBlocked();
    pluginScopeNote.textContent = t("plugins.startToEdit");
    renderPluginList(item, mutable);
    install.disabled = !mutable;
    packageInput.disabled = !mutable;
  }

  function fillProfiles(preferred?: ManagerProfileRef) {
    profile.replaceChildren();
    const profiles = (snapshot?.profiles ?? []).filter((item) => item.ref.dataDirectoryId === dataDirectory.value);
    if (profiles.length === 0) {
      const option = document.createElement("option");
      option.value = "";
      option.textContent = t("profiles.noInDataDirectory");
      option.disabled = true;
      option.selected = true;
      profile.append(option);
      return;
    }
    for (const item of profiles) {
      const option = document.createElement("option");
      option.value = item.ref.name;
      option.textContent = !item.launchable ? `${item.ref.name} · ${t("profiles.noDesktop")}` : item.exists ? item.ref.name : `${item.ref.name} · ${t("value.new")}`;
      option.disabled = !item.launchable;
      profile.append(option);
    }
    if (preferred && preferred.dataDirectoryId === dataDirectory.value && profiles.some((item) => item.ref.name === preferred.name)) {
      profile.value = preferred.name;
    } else if (!profiles.some((item) => item.ref.name === profile.value)) {
      profile.value = profiles[0].ref.name;
    }
  }

  function renderSelection() {
    if (editingEnvironment) return;
    const configured = snapshot?.configured;
    runtime.replaceChildren();
    for (const item of snapshot?.runtimes ?? []) {
      const option = document.createElement("option");
      option.value = item.id;
      option.textContent = `${item.version}${item.installed ? "" : ` · ${t("value.unverified")}`}`;
      runtime.append(option);
    }
    node.replaceChildren();
    const systemNode = document.createElement("option");
    systemNode.value = "system";
    systemNode.textContent = snapshot?.systemNode ? `${t("runtimes.systemNode")} ${snapshot.systemNode.version}` : t("runtimes.systemNodeUnavailable");
    systemNode.disabled = !snapshot?.systemNode;
    node.append(systemNode);
    for (const item of snapshot?.nodes ?? []) {
      if (item.ownership !== "managed" || !item.installed) continue;
      const option = document.createElement("option");
      option.value = item.id;
      option.textContent = `${item.version} · ${runtimeArtifactSourceLabel(item.installSource)}`;
      node.append(option);
    }
    dataDirectory.replaceChildren();
    for (const item of snapshot?.dataDirectories ?? []) {
      const option = document.createElement("option");
      option.value = item.id;
      option.textContent = item.name;
      dataDirectory.append(option);
    }
    if (configured) {
      runtime.value = configured.runtimeId;
      node.value = configured.node.kind === NodeSelectionKind.NodeSelectionManaged ? configured.node.installationId || "system" : "system";
      dataDirectory.value = configured.profile.dataDirectoryId;
    }
    if (!runtime.value && runtime.options.length > 0) {
      runtime.selectedIndex = 0;
    }
    if (!dataDirectory.value && dataDirectory.options.length > 0) {
      dataDirectory.selectedIndex = 0;
    }
    fillProfiles(configured?.profile);
  }

  function renderRuntimePreparations() {
    const hostPreparation = (hostStatus as (HostLifecycleStatus & {runtimePreparation?: RuntimePreparation}) | undefined)?.runtimePreparation;
    const hostKind = hostPreparation ? preparationKind(hostPreparation) : undefined;
    const preparations: Partial<Record<"node" | "dsh", RuntimePreparation>> = {...runtimePreparations};
    if (hostPreparation && hostKind && !Object.hasOwn(preparations, hostKind)) {
      preparations[hostKind] = hostPreparation;
    }
    renderRuntimePreparationCard("dsh", preparations.dsh, {
      panel: dshPreparationPanel,
      detail: dshPreparationDetail,
      progress: dshPreparationProgress,
      fill: dshPreparationProgressFill,
      meta: dshPreparationMeta,
      cancel: cancelRuntimeButtons[0]
    });
    renderRuntimePreparationCard("node", preparations.node, {
      panel: nodePreparationPanel,
      detail: nodePreparationDetail,
      progress: nodePreparationProgress,
      fill: nodePreparationProgressFill,
      meta: nodePreparationMeta,
      cancel: cancelRuntimeButtons[1]
    });
  }

  function preparationKind(preparation: RuntimePreparation): "node" | "dsh" {
    return runtimePreparationArtifactKind(preparation);
  }

  function renderRuntimePreparationCard(kind: "node" | "dsh", preparation: RuntimePreparation | undefined, view: {
    panel: HTMLElement;
    detail: HTMLParagraphElement;
    progress: HTMLDivElement;
    fill: HTMLSpanElement;
    meta: HTMLParagraphElement;
    cancel: HTMLButtonElement;
  }) {
    if (!preparation) {
      view.panel.hidden = true;
      return;
    }
    view.panel.hidden = false;
    const cancelling = runtimeCancelPending && runtimeInstallInFlight && kind === activeRuntimeKind;
    const detail = cancelling ? t("runtimes.cancelling") : runtimePreparationText(preparation);
    view.detail.textContent = detail;
    const hasProgress = preparation.hasTotal && (preparation.totalBytes ?? 0) > 0;
    const active = preparation.state !== "installed" && preparation.state !== "failed" && preparation.state !== "cancelled";
    view.progress.className = `progress-track${preparation.state === "installed" ? " complete" : preparation.state === "failed" ? " failed" : active && !hasProgress ? " indeterminate" : ""}`;
    const percentage = runtimePreparationProgressPercent(preparation);
    if (hasProgress && percentage !== undefined) {
      view.fill.style.setProperty("--progress", `${percentage}%`);
      view.progress.setAttribute("aria-valuenow", String(percentage));
      view.progress.setAttribute("aria-valuetext", `${detail} ${percentage}%`);
    } else if (preparation.state === "installed") {
      view.fill.style.setProperty("--progress", "100%");
      view.progress.setAttribute("aria-valuenow", "100");
      view.progress.setAttribute("aria-valuetext", detail);
    } else {
      view.fill.style.removeProperty("--progress");
      view.progress.removeAttribute("aria-valuenow");
      view.progress.setAttribute("aria-valuetext", detail);
    }
    const meta = [
      kind === "node" ? "Node" : "DSH",
      preparation.targetVersion ? `${t("field.version")}: ${preparation.targetVersion}` : "",
      preparation.source !== "none" ? runtimeArtifactSourceLabel(preparation.source) : ""
    ].filter(Boolean);
    if (percentage !== undefined) {
      meta.push(`${percentage}%`);
    }
    if (hasProgress) {
      meta.push(`${formatRuntimeBytes(preparation.receivedBytes ?? 0)} / ${formatRuntimeBytes(preparation.totalBytes ?? 0)}`);
    }
    if (preparation.result) {
      if (preparation.result.succeeded) {
        meta.push(t("runtimes.resultSucceeded"));
      } else if (preparation.state === "cancelled") {
        meta.push(t("runtimes.resultCancelled"));
      } else if (preparation.result.failure?.retryable) {
        meta.push(t("runtimes.resultFailedRetryable"));
      } else {
        meta.push(t("runtimes.resultFailed"));
      }
    }
    view.meta.textContent = meta.join(" · ");
    view.cancel.hidden = !preparation.canCancel || !runtimeInstallInFlight;
    view.cancel.disabled = cancelling;
    view.cancel.textContent = t(cancelling ? "runtimes.cancelling" : "common.cancel");
  }

  function selectedRunContext(): ManagerRunContext | undefined {
    if (!runtime.value || !dataDirectory.value || !profile.value) {
      return undefined;
    }
    if (node.value === "system" && !snapshot?.systemNode) {
      return undefined;
    }
    return {
      runtimeId: runtime.value,
      node: node.value === "system" ? {kind: NodeSelectionKind.NodeSelectionSystem} : {kind: NodeSelectionKind.NodeSelectionManaged, installationId: node.value},
      profile: {dataDirectoryId: dataDirectory.value, name: profile.value}
    };
  }

  function setContextControlsDisabled(disabled: boolean) {
    disabled = disabled || !snapshot || runtimeInstallInFlight || runtimeRemovalInFlight;
    environmentApply.disabled = disabled;
    (document.getElementById("environment-edit") as HTMLButtonElement).disabled = disabled;
    runtime.disabled = disabled;
    node.disabled = disabled;
    dataDirectory.disabled = disabled;
    profile.disabled = disabled;
    runtimeVersion.disabled = disabled || runtimeInstallInFlight || releasesLoading || !snapshot?.dshReleases?.length;
    installRuntimeButton.disabled = disabled || runtimeInstallInFlight || releasesLoading || !runtimeVersion.value || !!snapshot?.runtimes?.some(item => item.version === runtimeVersion.value && item.installed);
    refreshDSHButton.disabled = disabled || runtimeInstallInFlight || releasesLoading;
    installNodeButton.disabled = disabled || runtimeInstallInFlight;
    for (const button of Array.from(profileList.querySelectorAll<HTMLButtonElement>("[data-run-context-switch]"))) {
      button.disabled = disabled;
    }
    for (const button of Array.from(document.querySelectorAll<HTMLButtonElement>("[data-context-mutation]"))) {
      button.disabled = disabled;
    }
    if (!disabled) {
      renderProfileDetail();
      renderPluginPanel();
    }
  }

  async function switchRunContext(target?: ManagerRunContext) {
    if (contextSwitchInFlight) {
      return;
    }
    const operationFeedback = currentSection === "profiles" ? profilesFeedback : currentSection === "runtimes" ? runtimesFeedback : setFeedback;
    const next = target ?? selectedRunContext();
    if (!next) {
      operationFeedback(t("error.selectRunContext"), "error");
      return;
    }
    contextSwitchInFlight = true;
    setContextControlsDisabled(true);
    operationFeedback(t("feedback.switchingRunContext"), "neutral", true);
    try {
      snapshot = await setRunContext(next);
      editingEnvironment = false; environmentEditor.hidden = true;
      await refreshCurrentPluginObservation();
      applyTheme(snapshot.theme);
      operationFeedback("");
      renderSelection();
      renderOverview();
      renderProfiles();
      renderRuntimes();
    } catch (error) {
      await refresh(next.profile).catch(() => undefined);
      const failure = snapshot?.lastSwitchAttempt;
      operationFeedback(failure ? `${t("switch.failedTarget", {profile: failure.target.profile.name})} ${switchReason(failure.failure.code)}` : managerErrorMessage(error, "error.switchRunContext"), "error");
      console.error("Could not switch DSH Run context", error);
    } finally {
      contextSwitchInFlight = false;
      setContextControlsDisabled(false);
      renderOverview();
      renderProfileDetail();
      if (environmentEditor.hidden) document.getElementById("environment-edit")!.focus();
    }
  }

  async function switchToProfile(ref: ManagerProfileRef) {
	const base = snapshot?.configured ?? snapshot?.current;
	const runtimeId = base?.runtimeId;
    if (!runtimeId) {
      settingsFeedback(t("error.selectRunContext"), "error");
      return;
    }
	editEnvironment({runtimeId, node: base?.node ?? {kind: NodeSelectionKind.NodeSelectionSystem}, profile: {...ref}});
  }

  function selectProfile(ref: ManagerProfileRef) {
    managedProfile = {...ref};
    renameControls.hidden = true;
    document.getElementById("profile-result")!.textContent = "";
    renderProfiles();
    showSection("profiles");
    if (window.matchMedia("(max-width: 760px)").matches) { selectedProfileLabel.tabIndex = -1; selectedProfileLabel.focus({preventScroll: true}); profileDetail.scrollIntoView({block: "start"}); }
  }

  function renderProfiles() {
    const focusedKey = (document.activeElement as HTMLElement)?.dataset.profileKey;
    profileList.replaceChildren();
    const profiles = snapshot?.profiles ?? [];
    const current = currentRunContext();
    if (!managedProfileItem()) {
      syncManagedProfile();
    }
    if (profiles.length === 0) {
      const empty = document.createElement("p");
      empty.className = "manager-empty";
      empty.textContent = t(snapshot ? "profiles.noProfiles" : "view.loading");
      profileList.append(empty);
      renderProfileDetail();
      renderPluginPanel();
      return;
    }
    for (const item of profiles) {
      const selected = sameProfileRef(item.ref, managedProfile);
      const row = document.createElement("div");
      row.className = `manager-list-item${selected ? " is-selected" : ""}`;
      const text = document.createElement("div");
      const name = document.createElement("strong");
      name.textContent = item.ref.name;
      if (current && sameProfileRef(item.ref, current.profile)) {
        name.textContent += ` · ${t("profiles.current")}`;
      }
      const detail = document.createElement("span");
      detail.textContent = t("profiles.pluginDetail", {
        dataDirectory: dataDirectoryLabel(item.ref.dataDirectoryId),
        kind: profileKindLabel(item.kind),
        count: item.pluginCount,
        plural: item.pluginCount === 1 ? "" : "s"
      });
      text.append(name, detail);
      const choose = document.createElement("button");
      choose.className = "profile-select";
      choose.type = "button";
      choose.dataset.profileKey = `${item.ref.dataDirectoryId}/${item.ref.name}`;
      choose.setAttribute("aria-pressed", String(selected));
      choose.setAttribute("aria-label", t("profile.inspect", {profile: item.ref.name}));
      choose.append(text);
      choose.addEventListener("click", () => selectProfile(item.ref));
      row.append(choose);
      profileList.append(row);
    }
    renderProfileDetail();
    renderPluginPanel();
    if (focusedKey) profileList.querySelector<HTMLButtonElement>(`[data-profile-key="${CSS.escape(focusedKey)}"]`)?.focus({preventScroll: true});
  }

  function renderRuntimes() {
    renderRuntimePreparations();
    const selectedRelease = runtimeVersion.value;
    runtimeVersion.replaceChildren();
    if (!snapshot?.dshReleases?.length) runtimeVersion.add(new Option(t(releasesLoading ? "startup.fetchingReleases" : "runtimes.fetchFirst"), ""));
    for (const release of snapshot?.dshReleases ?? []) {
      const option = document.createElement("option");
      option.value = release.version;
      option.textContent = `${release.version} · ${t(snapshot?.runtimes?.some(r => r.version === release.version && r.installed) ? "startup.localInstalled" : "startup.remoteDownload")}${release.tags?.length ? ` · ${release.tags.join(", ")}` : ""}`;
      runtimeVersion.append(option);
    }
    if (selectedRelease && snapshot?.dshReleases?.some(item => item.version === selectedRelease)) runtimeVersion.value = selectedRelease;
    setContextControlsDisabled(contextSwitchInFlight);
    runtimeList.replaceChildren();
    const runtimes = snapshot?.runtimes ?? [];
    if (runtimes.length === 0) {
      const empty = document.createElement("p");
      empty.className = "manager-empty";
      empty.textContent = t("runtimes.noRuntimes");
      runtimeList.append(empty);
    }
    for (const item of runtimes) {
      const selected = item.id === snapshot?.configured?.runtimeId;
      const row = document.createElement("div");
      row.className = `manager-list-item${selected ? " is-selected" : ""}`;
      const text = document.createElement("div");
      const name = document.createElement("strong");
      name.textContent = `${item.version}${selected ? ` · ${t("action.selected")}` : ""}`;
      const detail = document.createElement("span");
      const artifactSource = item.installSource && item.installSource !== "none" ? runtimeArtifactSourceLabel(item.installSource) : "";
      detail.textContent = [runtimeSourceLabel(item.source), t(item.installed ? "value.verified" : "value.unverified"), artifactSource]
        .filter(Boolean)
        .join(" · ");
      text.append(name, detail);
      row.append(text);
      if (!selected && item.installed && snapshot?.configured) {
        const switchButton = document.createElement("button");
        switchButton.className = "button button-primary";
        switchButton.type = "button";
        switchButton.textContent = t("action.switch");
        switchButton.disabled = contextSwitchInFlight || runtimeInstallInFlight || runtimeRemovalInFlight;
        switchButton.addEventListener("click", () => editEnvironment({...snapshot!.configured!, runtimeId: item.id}));
        row.append(switchButton);
      }
      if (item.removable) {
        const removeButton = document.createElement("button");
        removeButton.className = "button button-secondary";
        removeButton.type = "button";
        removeButton.textContent = t("action.remove");
        removeButton.dataset.contextMutation = "true";
        const protectedRuntime = [snapshot?.configured, snapshot?.current, snapshot?.knownGood, snapshot?.safeMode?.returnTo].some((target) => target?.runtimeId === item.id);
        removeButton.disabled = contextSwitchInFlight || runtimeInstallInFlight || runtimeRemovalInFlight || protectedRuntime;
        removeButton.addEventListener("click", () => void (async () => {
          if (contextSwitchInFlight || runtimeInstallInFlight || runtimeRemovalInFlight) {
            return;
          }
          runtimeRemovalInFlight = true;
          runtimesFeedback("");
          renderRuntimes();
          try {
            snapshot = await removeRuntime(item.id);
            runtimesFeedback(t("feedback.removedRuntime", {version: item.version}), "success");
            runtimePreparations.dsh = undefined;
            operationLogs.dsh.clear();
            renderSelection();
            renderProfiles();
            renderRuntimes();
          } catch (error) {
            runtimesFeedback(managerErrorMessage(error, "error.removeRuntime"), "error");
            console.error("Could not remove DSH runtime", error);
          } finally {
            runtimeRemovalInFlight = false;
            renderRuntimes();
          }
        })());
        row.append(removeButton);
      }
      runtimeList.append(row);
    }
    nodeList.replaceChildren();
    const nodes = snapshot?.nodes ?? [];
    const systemNode = snapshot?.systemNode;
    if (systemNode) {
      const selectedSystemNode = snapshot?.configured?.node.kind === NodeSelectionKind.NodeSelectionSystem;
      const row = document.createElement("div");
      row.className = `manager-list-item${selectedSystemNode ? " is-selected" : ""}`;
      const text = document.createElement("div");
      const name = document.createElement("strong");
      name.textContent = `${t("runtimes.systemNode")} ${systemNode.version}${selectedSystemNode ? ` · ${t("action.selected")}` : ""}`;
      const detail = document.createElement("span");
      detail.textContent = `${t("value.system")} · ${t("value.verified")}`;
      text.append(name, detail);
      row.append(text);
      if (!selectedSystemNode && snapshot?.configured) {
        const useButton = document.createElement("button");
        useButton.className = "button button-primary";
        useButton.type = "button";
        useButton.textContent = t("action.switch");
        useButton.disabled = contextSwitchInFlight || runtimeInstallInFlight || runtimeRemovalInFlight;
        useButton.addEventListener("click", () => editEnvironment({...snapshot!.configured!, node: {kind: NodeSelectionKind.NodeSelectionSystem}}));
        row.append(useButton);
      }
      nodeList.append(row);
    }
    if (nodes.length === 0) {
      const empty = document.createElement("p");
      empty.className = "manager-empty";
      empty.textContent = t("runtimes.noNodes");
      nodeList.append(empty);
    }
    for (const item of nodes) {
      const selectedNode = snapshot?.configured?.node.kind === NodeSelectionKind.NodeSelectionManaged && snapshot.configured.node.installationId === item.id;
      const row = document.createElement("div");
      row.className = `manager-list-item${selectedNode ? " is-selected" : ""}`;
      const text = document.createElement("div");
      const name = document.createElement("strong");
      name.textContent = `${item.version}${selectedNode ? ` · ${t("action.selected")}` : ""}`;
      const detail = document.createElement("span");
      detail.textContent = [t("value.installed"), runtimeArtifactSourceLabel(item.installSource), t(item.verified ? "value.verified" : "value.unverified")].join(" · ");
      text.append(name, detail);
      row.append(text);
      if (!selectedNode && item.installed && snapshot?.configured) {
        const switchButton = document.createElement("button");
        switchButton.className = "button button-primary";
        switchButton.type = "button";
        switchButton.textContent = t("action.switch");
        switchButton.disabled = contextSwitchInFlight || runtimeInstallInFlight || runtimeRemovalInFlight;
        switchButton.addEventListener("click", () => editEnvironment({...snapshot!.configured!, node: {kind: NodeSelectionKind.NodeSelectionManaged, installationId: item.id}}));
        row.append(switchButton);
      }
      if (item.removable) {
        const removeButton = document.createElement("button");
        removeButton.className = "button button-secondary";
        removeButton.type = "button";
        removeButton.textContent = t("action.remove");
        const protectedNode = [snapshot?.configured, snapshot?.current, snapshot?.knownGood, snapshot?.safeMode?.returnTo].some((target) => target?.node.kind === NodeSelectionKind.NodeSelectionManaged && target.node.installationId === item.id);
        removeButton.disabled = contextSwitchInFlight || runtimeInstallInFlight || runtimeRemovalInFlight || protectedNode;
        removeButton.addEventListener("click", () => void (async () => {
          if (contextSwitchInFlight || runtimeInstallInFlight || runtimeRemovalInFlight) return;
          runtimeRemovalInFlight = true;
          nodeFeedback("");
          renderRuntimes();
          try {
            snapshot = await removeNode(item.id);
            nodeFeedback("");
            runtimePreparations.node = undefined;
            operationLogs.node.clear();
            renderSelection();
          } catch (error) {
            nodeFeedback(managerErrorMessage(error, "error.removeNode"), "error");
          } finally {
            runtimeRemovalInFlight = false;
            renderRuntimes();
          }
        })());
        row.append(removeButton);
      }
      nodeList.append(row);
    }
  }

  async function refreshCurrentPluginObservation() {
	const current = snapshot?.current;
	if (!snapshot || !current || hostStatus?.state !== "Ready") return;
	try {
		const plugins = await listPlugins({target: {profile: current.profile}}) ?? [];
		const item = snapshot.profiles?.find((profileItem) => sameProfileRef(profileItem.ref, current.profile));
		if (item) { item.plugins = plugins; item.pluginCount = plugins.length; }
	} catch (error) {
		console.error("Could not refresh current plugin observation", error);
	}
  }

  async function refresh(preferredProfile?: ManagerProfileRef) {
    loadRetry.disabled = true;
    try {
      [snapshot, hostStatus] = await Promise.all([getSnapshot(), getHostStatus()]);
      loadState.hidden = true;
      setContextControlsDisabled(mutationBlocked());
      applyTheme(snapshot.theme);
      renderSelection();
      syncManagedProfile(preferredProfile);
      renderOverview();
      renderProfiles();
      renderRuntimes();
      showSection(currentSection);
      themeSyncAvailable = true;
      startThemeSync();
    } catch (error) {
      loadState.hidden = false; loadRetry.hidden = false;
      loadMessage.textContent = t("view.unavailable");
      if (!snapshot) {
        for (const field of [currentState, currentRuntime, currentProfile, currentNode]) field.textContent = "—";
        profileList.textContent = "";
        profileEmpty.hidden = true; pluginEmpty.hidden = true;
        setContextControlsDisabled(true);
      }
      console.error("Could not read DSH manager snapshot", error);
    }
    loadRetry.disabled = false;
    await Promise.all([settings.refresh(), notifications.refresh(), pets.refresh()]);
  }

  for (const item of navItems) {
    item.addEventListener("click", () => showSection(sectionName(item.dataset.managerSection ?? null)));
  }
  Events.On("lifecycle", () => void refresh());
  const operationLogs = {
    node: mountOperationLog(nodePreparationPanel),
    dsh: mountOperationLog(dshPreparationPanel),
    plugin: mountOperationLog(document.querySelector("#manager-plugin-detail") as HTMLElement),
  };
  function beginRuntimeInstall(kind: "node" | "dsh", version?: string) {
    activeRuntimeKind = kind;
    runtimeInstallInFlight = true;
    runtimeCancelPending = false;
    (kind === "node" ? nodeFeedback : runtimesFeedback)("");
    operationLogs[kind].clear();
    runtimePreparations[kind] = {artifactKind: kind, state: "resolving-toolchain", operation: "none", targetVersion: version, source: "none", hasTotal: false, canCancel: false};
    renderRuntimes();
  }
  Events.On("acquisition", (event) => {
    const status = event.data as OperationStatus;
    const kind = status.artifact.kind === "plugin" ? "plugin" : status.artifact.kind === "node" ? "node" : "dsh";
    const log = operationLogs[kind];
    if (status.log) {
      log.append(status.operationId, status.log);
      return;
    }
    const preparation = acquisitionPreparation(status);
    let message = kind === "plugin" && status.state === "active" ? t("logs.pluginApplying") : runtimePreparationText(preparation);
    if (status.state === "active" && status.step.startsWith("detect-")) message = t("runtimes.checkingTools");
    if (status.state === "succeeded") message = t("runtimes.resultSucceeded");
    if (kind === "plugin") {
      const result = document.getElementById("plugin-operation-status")!;
      if (status.state === "succeeded") transientStatus(result, message);
      else result.textContent = message;
    }
    if (status.route) message += ` · ${runtimeArtifactSourceLabel(status.route)}`;
    if (status.artifact.version) message += ` · ${status.artifact.version}`;
    if (status.receivedBytes) message += ` · ${formatRuntimeBytes(status.receivedBytes)}${status.hasTotal ? ` / ${formatRuntimeBytes(status.totalBytes ?? 0)}` : ""}`;
    const bucket = status.hasTotal && status.totalBytes ? Math.floor((status.receivedBytes ?? 0) / status.totalBytes * 10) : Math.floor((status.receivedBytes ?? 0) / (5 * 1024 * 1024));
    log.append(status.operationId, message, status.state === "failed", `${status.state}/${status.step}/${status.route}/${bucket}`);
    if (kind !== "plugin") {
      runtimePreparations[kind] = preparation;
      if (status.state !== "active") (kind === "node" ? nodeFeedback : runtimesFeedback)("");
      renderRuntimePreparations();
    }
  });
  setContextControlsDisabled(true);
  for (const field of [currentState, currentRuntime, currentProfile, currentNode]) field.textContent = "—";
  profileEmpty.hidden = true; pluginEmpty.hidden = true;
  showSection(currentSection);
  subscribeLocale(() => {
    Object.values(operationLogs).forEach(log => log.labels());
    showSection(currentSection);
    renderSelection();
    renderOverview();
    renderProfiles();
    renderRuntimes();
    pets.renderLocale();
  });

  dataDirectory.addEventListener("change", () => fillProfiles());
  window.addEventListener("focus", () => {
    void syncTheme();
    void refresh();
  });
  document.addEventListener("visibilitychange", () => {
    if (document.visibilityState === "visible") {
      void syncTheme();
      void refresh();
    }
  });

  function explicitPluginTarget() {
    const current = currentRunContext();
    if (!current) {
      throw new Error(t("error.selectPluginTarget"));
    }
    if (!canMutateProfile(current.profile)) {
      throw new Error(t("error.profileReadOnly"));
    }
    return {profile: {...current.profile}};
  }

  function setPluginControlsDisabled(disabled: boolean) {
    const item = managedProfileItem();
    const currentItem = currentProfileItem();
    const current = currentRunContext();
    const isCurrent = !!item && !!current && sameProfileRef(item.ref, current.profile);
    const canRename = !!item?.renamable && !isCurrent && !mutationBlocked();
    install.disabled = disabled || !canMutateProfile(currentItem?.ref);
    packageInput.disabled = disabled || !canMutateProfile(currentItem?.ref);
    profileName.disabled = disabled || !canRename;
    profileRename.disabled = disabled || !canRename;
    profileClone.disabled = disabled || (!item?.exists && !item?.autoInitialize) || mutationBlocked();
    profileBackup.disabled = disabled || !item?.exists || mutationBlocked();
    profileDelete.disabled = disabled || !item?.deletable || mutationBlocked();
    for (const button of Array.from(profilePlugins.querySelectorAll<HTMLButtonElement>("[data-plugin-remove]"))) {
      button.disabled = disabled;
    }
  }

  async function mutatePlugin(operation: "install" | "upgrade" | "remove", packageOverride?: string, sourceButton?: HTMLButtonElement) {
    const packageSpec = (packageOverride ?? packageInput.value).trim();
    if (!packageSpec) {
      pluginsFeedback(t("error.enterPackage"), "error");
      if (operation === "install") {
        packageInput.focus();
      }
      return;
    }
    try {
      const target = explicitPluginTarget();
      setPluginControlsDisabled(true);
      if (sourceButton) {
        sourceButton.disabled = true;
      }
      const result = operation === "install"
        ? await installPlugin({target, package: packageSpec})
        : operation === "upgrade"
          ? await upgradePlugin({target, package: packageSpec})
          : await removePlugin({target, package: packageSpec});
      await refresh(target.profile);
      document.getElementById("plugins-restart")!.hidden = !result.restartRequired;
      pluginsFeedback(result.restartRequired
        ? t("feedback.pluginChangedRestart")
        : t(operation === "install" ? "feedback.pluginInstalled" : operation === "upgrade" ? "feedback.pluginUpgraded" : "feedback.pluginRemoved", {profile: result.profile.name}), "success", result.restartRequired);
    } catch (error) {
      // A failed candidate may have restored the previous environment.
      await Promise.allSettled([refresh()]);
      pluginsFeedback(managerErrorMessage(error, "error.changePlugins"), "error");
      console.error("Could not change profile plugin", error);
    } finally {
      setPluginControlsDisabled(false);
    }
  }

  install.addEventListener("click", () => void mutatePlugin("install"));

  profileRename.addEventListener("click", () => void (async () => {
    const item = managedProfileItem();
    if (!item || !item.renamable) {
      return;
    }
    const nextName = profileName.value.trim();
    if (!nextName) {
      profilesFeedback(t("error.profileNameRequired"), "error");
      profileName.focus();
      return;
    }
    profileRename.disabled = true;
    profileName.disabled = true;
    try {
      snapshot = await renameProfile({profile: item.ref, newName: nextName});
      managedProfile = {dataDirectoryId: item.ref.dataDirectoryId, name: nextName};
      applyTheme(snapshot.theme);
      renderSelection();
      renderOverview();
      renderProfiles();
      renderRuntimes();
      renameControls.hidden = true;
      renameStart.focus();
      profilesFeedback(t("feedback.profileRenamed", {profile: nextName}), "success");
    } catch (error) {
      profilesFeedback(managerErrorMessage(error, "error.renameProfile"), "error");
      console.error("Could not rename DSH profile", error);
    } finally {
      renderProfileDetail();
    }
  })());

  profileClone.addEventListener("click", () => void (async () => {
    const item = managedProfileItem();
    if ((!item?.exists && !item?.autoInitialize) || contextSwitchInFlight) {
      return;
    }
    profileClone.disabled = true;
    try {
      const result = await cloneProfile({profile: item.ref});
      snapshot = result.snapshot;
      managedProfile = {...result.profile};
      renameControls.hidden = true;
      renderSelection();
      renderOverview();
      renderProfiles();
      renderRuntimes();
      showSection("profiles");
      profilesFeedback(t("feedback.profileCloned", {profile: result.profile.name}), "success");
    } catch (error) {
      profilesFeedback(managerErrorMessage(error, "error.cloneProfile"), "error");
      console.error("Could not clone DSH profile", error);
    } finally {
      renderProfileDetail();
    }
  })());

  profileDelete.addEventListener("click", () => void (async () => {
    const item = managedProfileItem();
    if (!item?.deletable || contextSwitchInFlight) {
      return;
    }
    if (!window.confirm(t("confirm.deleteProfile", {profile: item.ref.name}))) {
      return;
    }
    profileDelete.disabled = true;
    try {
      snapshot = await deleteProfile({profile: item.ref});
      managedProfile = undefined;
      renderSelection();
      renderOverview();
      renderProfiles();
      renderRuntimes();
      showSection("profiles");
      profilesFeedback(t("feedback.profileDeleted", {profile: item.ref.name}), "success");
    } catch (error) {
      profilesFeedback(managerErrorMessage(error, "error.deleteProfile"), "error");
      console.error("Could not delete DSH profile", error);
    } finally {
      renderProfileDetail();
    }
  })());

  profileExport.addEventListener("click", () => void (async () => {
    const item = managedProfileItem();
    if (!item?.exists || mutationBlocked() || exportInFlight) return;
    exportInFlight = true;
    profileExport.disabled = true;
    try {
      const file = await ManagerService.ExportProfile(item.ref);
      if (file) profilesFeedback(t("feedback.profileExported", {file}), "success", true);
    } catch (error) { profilesFeedback(managerErrorMessage(error, "error.exportProfile"), "error"); }
    finally { exportInFlight = false; renderProfileDetail(); }
  })());

  profileBackup.addEventListener("click", () => void (async () => {
    const item = managedProfileItem();
    if (!item?.exists || contextSwitchInFlight) {
      return;
    }
    profileBackup.disabled = true;
    try {
      const result = await backupProfile({profile: item.ref});
      recovery.backupCreated(result);
    } catch (error) {
      profilesFeedback(managerErrorMessage(error, "error.backupProfile"), "error");
      console.error("Could not back up DSH profile", error);
    } finally {
      renderProfileDetail();
    }
  })());

  installRuntimeButton.addEventListener("click", () => void (async () => {
    if (contextSwitchInFlight || runtimeInstallInFlight || runtimeRemovalInFlight) {
      return;
    }
    const version = runtimeVersion.value.trim();
    if (!version) {
      runtimesFeedback(t("error.noDshRelease"), "error");
      return;
    }
    beginRuntimeInstall("dsh", version);
    try {
      snapshot = await installRuntime(version);
      runtimesFeedback(t("feedback.installedRuntime", {version}), "success");
      runtimeVersion.value = "";
      renderSelection();
      renderOverview();
      renderProfiles();
      renderRuntimes();
    } catch (error) {
      if (!runtimePreparations.dsh?.result && runtimePreparations.dsh?.state !== "cancelled") {
        runtimePreparations.dsh = undefined;
        runtimesFeedback(managerErrorMessage(error, "error.installRuntime"), "error");
      }
      console.error("Could not install DSH runtime", error);
    } finally {
      runtimeInstallInFlight = false;
      runtimeCancelPending = false;
      renderRuntimes();
    }
  })());

  runtimeVersion.addEventListener("change", () => setContextControlsDisabled(contextSwitchInFlight));
  refreshDSHButton.addEventListener("click", () => void (async () => {
	releasesLoading = true;
	renderRuntimes();
	refreshDSHButton.textContent = t("startup.fetchingReleases");
	try { snapshot = await refreshDSHReleases(); renderRuntimes(); runtimesFeedback(t("feedback.dshReleasesRefreshed"), "success"); }
	catch (error) { runtimesFeedback(managerErrorMessage(error, "error.refreshDshReleases"), "error"); }
	finally { releasesLoading = false; refreshDSHButton.textContent = t("action.refresh"); renderRuntimes(); }
  })());

  installNodeButton.addEventListener("click", () => void (async () => {
	if (contextSwitchInFlight || runtimeInstallInFlight || runtimeRemovalInFlight) return;
	beginRuntimeInstall("node");
	try { snapshot = await installLatestNode(); renderSelection(); nodeFeedback(""); }
	catch (error) {
	    if (!runtimePreparations.node?.result && runtimePreparations.node?.state !== "cancelled") {
        runtimePreparations.node = undefined;
        nodeFeedback(managerErrorMessage(error, "error.installNode"), "error");
      }
    }
	finally { runtimeInstallInFlight = false; runtimeCancelPending = false; renderRuntimes(); }
  })());

  document.getElementById("manager-switch-copy")!.addEventListener("click", () => void (async () => {
    try {
      await navigator.clipboard.writeText(document.getElementById("manager-switch-diagnostic")!.textContent ?? "");
      settingsFeedback(t("logs.copied"), "success");
    } catch { settingsFeedback(t("logs.copyFailed"), "error"); }
  })());
  switchRetry.addEventListener("click", () => {
    if (snapshot?.lastSwitchAttempt) void switchRunContext(snapshot.lastSwitchAttempt.target);
  });

  switchRestore.addEventListener("click", () => void (async () => {
	try { snapshot = await restoreKnownGood(); await refreshCurrentPluginObservation(); renderSelection(); renderOverview(); renderProfiles(); renderRuntimes(); }
	catch (error) { await refresh().catch(() => undefined); settingsFeedback(managerErrorMessage(error, "error.switchRunContext"), "error"); }
  })());

  for (const cancelRuntimeButton of cancelRuntimeButtons) {
    cancelRuntimeButton.addEventListener("click", () => void (async () => {
      if (!runtimeInstallInFlight || runtimeCancelPending) {
        return;
      }
      runtimeCancelPending = true;
      renderRuntimePreparations();
      try {
        await cancelRuntime();
      } catch (error) {
        runtimeCancelPending = false;
        (activeRuntimeKind === "node" ? nodeFeedback : runtimesFeedback)(managerErrorMessage(error, "error.cancelRuntime"), "error");
        renderRuntimePreparations();
        console.error("Could not cancel runtime preparation", error);
      }
    })());
  }

  void refresh(managedProfile);
}
