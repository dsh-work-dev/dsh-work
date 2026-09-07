import {Events} from "@wailsio/runtime";

import {HostService, ManagerService} from "../bindings/github.com/local/dsh-work/internal/app";
import type {OperationStatus} from "../bindings/github.com/local/dsh-work/internal/acquisition/models";
import {DataDirectoryOwnership, NodeSelectionKind, type DataDirectoryInfo, type PluginInfo, type PluginResult, type ProfileInfo, type ProfileRef, type RunContext, type RuntimeInfo, type Snapshot} from "../bindings/github.com/local/dsh-work/internal/dshmanager";
import {mountNotifications, mountSettings} from "./settings";
import {buildOverviewModel, type OverviewLane} from "./overview";
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
const registerDataDirectory = ManagerService.RegisterDataDirectory;
const removeDataDirectory = ManagerService.RemoveDataDirectory;
const refreshDSHReleases = ManagerService.RefreshDSHReleases;
const installLatestNode = ManagerService.InstallLatestNode;
const removeNode = ManagerService.RemoveNode;
const retryLastSwitch = ManagerService.RetryLastSwitch;
const restoreKnownGood = ManagerService.RestoreKnownGood;

function managerErrorMessage(error: unknown, fallbackKey: string): string {
  void error;
  return t(fallbackKey);
}

function dataDirectoryOwnershipLabel(value: DataDirectoryOwnership): string {
  if (value === DataDirectoryOwnership.DataDirectoryOwnershipDSHWork) {
    return "dsh-work";
  }
  if (value === DataDirectoryOwnership.DataDirectoryOwnershipUser) {
    return t("value.user");
  }
  return t("value.other");
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

function runtimePreparationText(preparation: RuntimePreparation): string {
  if (preparation.state === "failed" && preparation.error?.summary) {
    return preparation.error.summary;
  }
  switch (preparation.state) {
    case "acquiring-node":
      return t("runtimes.downloadingNode");
    case "acquiring-dsh":
      return t("runtimes.downloadingDsh");
    case "verifying":
      return t("runtimes.verifying");
    case "installed":
      return t(preparation.artifactKind === "node" ? "runtimes.nodePrepared" : "runtimes.prepared");
    case "cancelled":
      return t("runtimes.cancelled");
    case "failed":
      return t("runtimes.failed");
  }
  switch (preparation.operation) {
    case "detect-pnpm":
      return t("runtimes.checkingTools");
    case "detect-npm":
      return t("runtimes.checkingTools");
    case "download-node":
      return t("runtimes.downloadingNode");
    case "install-dsh":
      return t("runtimes.downloadingDsh");
    case "verify":
      return t("runtimes.verifying");
    default:
      return t("runtimes.preparing");
  }
}

function acquisitionPreparation(status: OperationStatus): RuntimePreparation {
  const state = status.state === "succeeded" ? "installed" : status.state === "failed" ? "failed" : status.state === "canceled" ? "cancelled" :
    status.artifact.kind === "node" ? "acquiring-node" : "acquiring-dsh";
  const resultRoute = status.result?.route || undefined;
  return {
    state,
    operation: (status.step || "none") as RuntimePreparation["operation"],
    targetVersion: status.artifact.version,
    source: (status.route || resultRoute || "none") as RuntimePreparation["source"],
    receivedBytes: status.receivedBytes,
    totalBytes: status.totalBytes,
    hasTotal: status.hasTotal,
    canCancel: status.canCancel,
    operationId: status.operationId,
    artifactKind: status.artifact.kind as RuntimePreparation["artifactKind"],
    attempt: status.attempt,
    error: status.result?.failure ? {
      code: status.result.failure.kind,
      summary: status.result.failure.summary,
      retryable: status.result.failure.retryable,
      effectOccurred: false,
      correlationId: status.operationId || ""
    } : undefined,
    result: status.result ? {
      succeeded: status.result.succeeded,
      route: resultRoute as "official" | "mirror" | "local" | undefined,
      failure: status.result.failure ? {
        kind: status.result.failure.kind,
        retryable: status.result.failure.retryable,
      } : undefined,
    } : undefined,
  };
}

function formatRuntimeBytes(value: number): string {
  if (!Number.isFinite(value) || value < 0) {
    return "0 B";
  }
  if (value < 1024) {
    return `${Math.round(value)} B`;
  }
  if (value < 1024 * 1024) {
    return `${(value / 1024).toFixed(1)} KB`;
  }
  if (value < 1024 * 1024 * 1024) {
    return `${(value / (1024 * 1024)).toFixed(1)} MB`;
  }
  return `${(value / (1024 * 1024 * 1024)).toFixed(1)} GB`;
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

type ManagerSection = "overview" | "profiles" | "plugins" | "runtimes" | "data-directories" | "settings" | "notifications";

function sectionName(value: string | null): ManagerSection {
  if (value === "overview" || value === "profiles" || value === "plugins" || value === "runtimes" || value === "data-directories" || value === "settings" || value === "notifications") {
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
  const newDataDirectoryPath = document.getElementById("manager-new-data-directory-path") as HTMLInputElement;
  const newDataDirectoryId = document.getElementById("manager-new-data-directory-id") as HTMLInputElement;
  const newDataDirectoryName = document.getElementById("manager-new-data-directory-name") as HTMLInputElement;
  const registerDataDirectoryButton = document.getElementById("manager-register-data-directory") as HTMLButtonElement;
  const runtimeVersion = document.getElementById("manager-runtime-version") as HTMLSelectElement;
  const installRuntimeButton = document.getElementById("manager-runtime-install") as HTMLButtonElement;
  const refreshDSHButton = document.getElementById("manager-dsh-refresh") as HTMLButtonElement;
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
  const feedback = document.getElementById("manager-feedback") as HTMLParagraphElement;
  const managerTitle = document.getElementById("manager-title") as HTMLHeadingElement;
  const currentRuntime = document.getElementById("manager-current-runtime") as HTMLElement;
  const currentDataDirectory = document.getElementById("manager-current-data-directory") as HTMLElement;
  const currentProfile = document.getElementById("manager-current-profile") as HTMLElement;
  const currentWorkspace = document.getElementById("manager-current-workspace") as HTMLElement;
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
  const currentProfileSummary = document.getElementById("manager-current-profile-summary") as HTMLElement;
  const currentProfileStatus = document.getElementById("manager-current-profile-status") as HTMLParagraphElement;
  const profileScopeNote = document.getElementById("manager-profile-scope-note") as HTMLParagraphElement;
  const profileEmpty = document.getElementById("manager-profile-empty") as HTMLElement;
  const profileDetail = document.getElementById("manager-profile-detail") as HTMLElement;
  const profileName = document.getElementById("manager-profile-name") as HTMLInputElement;
  const profileRename = document.getElementById("manager-profile-rename") as HTMLButtonElement;
  const profileClone = document.getElementById("manager-profile-clone") as HTMLButtonElement;
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
  const dataDirectoryList = document.getElementById("manager-data-directories") as HTMLDivElement;
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
    notifications: {title: "manager.notifications"}
  };

  function setFeedback(message: string, tone: "neutral" | "success" | "error" = "neutral") {
    feedback.textContent = message;
    feedback.className = tone === "neutral" ? "manager-feedback" : `manager-feedback is-${tone}`;
    feedback.hidden = message.length === 0;
  }

  const settings = mountSettings(setFeedback);
  const notifications = mountNotifications(setFeedback);

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

  function showSection(next: ManagerSection) {
    currentSection = next;
    managerTitle.textContent = t(sectionCopy[next].title);
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
  }

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
    return contextSwitchInFlight || runtimeInstallInFlight;
  }

  function canMutateProfile(ref: ManagerProfileRef | undefined): boolean {
    const current = currentRunContext();
    return !mutationBlocked() && !!ref && !!current && hostStatus?.state === "Ready" && sameProfileRef(ref, current.profile);
  }

  function dataDirectoryLabel(dataDirectoryId: string): string {
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
    if (!snapshot) {
      return;
    }
    const model = buildOverviewModel(snapshot);
    renderOverviewLane(model.current, {
      runtime: currentRuntime,
      dataDirectory: currentDataDirectory,
      profile: currentProfile
    });
    const workspace = hostStatus?.workspace;
    if (workspace?.state === "selected" && workspace.path) {
      const workspaceTitle = workspace.title || workspace.path;
      currentWorkspace.textContent = workspaceTitle;
      currentWorkspace.title = workspace.path;
      currentWorkspace.setAttribute("aria-label", `${workspaceTitle}: ${workspace.path}`);
    } else if (workspace?.state === "selection-required") {
      currentWorkspace.textContent = t("value.chooseWorkspaceInDsh");
      currentWorkspace.removeAttribute("title");
      currentWorkspace.setAttribute("aria-label", t("value.chooseWorkspaceInDsh"));
    } else {
      currentWorkspace.textContent = t("value.noWorkspace");
      currentWorkspace.removeAttribute("title");
      currentWorkspace.setAttribute("aria-label", t("value.noWorkspace"));
    }
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
    configuredContext.hidden = !model.configured;
    if (model.configured) {
      renderOverviewLane(model.configured, {
        runtime: configuredRuntime,
        dataDirectory: configuredDataDirectory,
        profile: configuredProfile
      });
    }
    knownGoodContext.hidden = !model.knownGood;
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
      switchFailureDetail.textContent = t("error.switchRunContext");
      switchRetry.hidden = false;
      switchRestore.hidden = !snapshot.knownGood || attempt.rollback === "restored";
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
    const item = managedProfileItem();
    profileEmpty.hidden = !!item;
    profileDetail.hidden = !item;
    if (!item) {
      profileName.value = "";
      profileClone.disabled = true;
      profileBackup.disabled = true;
      profileDelete.disabled = true;
      return;
    }
    selectedProfileLabel.textContent = item.ref.name;
    const profileState = item.renamable
      ? `${dataDirectoryLabel(item.ref.dataDirectoryId)} · ${profileKindLabel(item.kind)}`
      : `${dataDirectoryLabel(item.ref.dataDirectoryId)} · ${profileKindLabel(item.kind)} · ${t("profiles.fixedName")}`;
    const mutable = canMutateProfile(item.ref);
    const current = currentRunContext();
    const isCurrent = !!current && sameProfileRef(item.ref, current.profile);
    const canRename = item.renamable && !isCurrent && !mutationBlocked();
    const accessNote = mutable
      ? t("profiles.currentEditable")
      : t("profiles.readOnly");
    profileScopeNote.textContent = `${profileState} · ${accessNote}`;
    profileName.value = item.ref.name;
    profileName.disabled = !canRename;
    profileRename.disabled = !canRename;
    profileClone.disabled = (!item.exists && !item.autoInitialize) || mutationBlocked();
    profileBackup.disabled = !item.exists || item.kind !== "custom" || mutationBlocked();
    profileDelete.disabled = !item.deletable || mutationBlocked();
  }

  function renderPluginPanel() {
    const item = currentProfileItem();
    pluginEmpty.hidden = !!item;
    pluginDetail.hidden = !item;
    if (!item) {
      profilePlugins.replaceChildren();
      return;
    }
    pluginProfileLabel.textContent = `${item.ref.name} · ${dataDirectoryLabel(item.ref.dataDirectoryId)}`;
    const mutable = canMutateProfile(item.ref);
    const current = currentRunContext();
    const isCurrent = !!current && sameProfileRef(item.ref, current.profile);
    pluginScopeNote.textContent = isCurrent
      ? mutable ? t("profiles.pluginsEditable") : t("profiles.pluginsReadOnly")
      : t("profiles.pluginsReadOnly");
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
      option.textContent = item.exists ? item.ref.name : `${item.ref.name} · ${t("value.new")}`;
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
    if (hostPreparation && hostKind && !preparations[hostKind]) {
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
    const detail = runtimePreparationText(preparation);
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
    view.cancel.disabled = !runtimeInstallInFlight;
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
    runtime.disabled = disabled;
    node.disabled = disabled;
    dataDirectory.disabled = disabled;
    profile.disabled = disabled;
    runtimeVersion.disabled = disabled || runtimeInstallInFlight;
    installRuntimeButton.disabled = disabled || runtimeInstallInFlight;
    refreshDSHButton.disabled = disabled || runtimeInstallInFlight;
    installNodeButton.disabled = disabled || runtimeInstallInFlight;
    newDataDirectoryPath.disabled = disabled;
    newDataDirectoryId.disabled = disabled;
    newDataDirectoryName.disabled = disabled;
    registerDataDirectoryButton.disabled = disabled;
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
    const next = target ?? selectedRunContext();
    if (!next) {
      setFeedback(t("error.selectRunContext"), "error");
      return;
    }
    contextSwitchInFlight = true;
    setContextControlsDisabled(true);
    setFeedback(t("feedback.switchingRunContext"), "neutral");
    try {
      snapshot = await setRunContext(next);
      await refreshCurrentPluginObservation();
      applyTheme(snapshot.theme);
      setFeedback(t("feedback.contextSwitched"), "success");
      renderSelection();
      renderOverview();
      renderProfiles();
      renderRuntimes();
      renderDataDirectories();
    } catch (error) {
      await refresh(next.profile).catch(() => undefined);
      setFeedback(managerErrorMessage(error, "error.switchRunContext"), "error");
      console.error("Could not switch DSH Run context", error);
    } finally {
      contextSwitchInFlight = false;
      setContextControlsDisabled(false);
      renderProfileDetail();
    }
  }

  async function switchToProfile(ref: ManagerProfileRef) {
	const base = snapshot?.configured ?? snapshot?.current;
	const runtimeId = base?.runtimeId;
    if (!runtimeId) {
      setFeedback(t("error.selectRunContext"), "error");
      return;
    }
	await switchRunContext({runtimeId, node: base?.node ?? {kind: NodeSelectionKind.NodeSelectionSystem}, profile: {...ref}});
  }

  function selectProfile(ref: ManagerProfileRef) {
    managedProfile = {...ref};
    renderProfiles();
    showSection("profiles");
  }

  function renderProfiles() {
    profileList.replaceChildren();
    const profiles = snapshot?.profiles ?? [];
    const current = currentRunContext();
    const currentItem = current ? profiles.find((item) => sameProfileRef(item.ref, current.profile)) : undefined;
    if (currentItem) {
      currentProfileSummary.textContent = currentItem.ref.name;
      currentProfileStatus.textContent = `${dataDirectoryLabel(currentItem.ref.dataDirectoryId)} · ${t("profiles.currentHint")}`;
    } else {
      currentProfileSummary.textContent = t("profiles.noCurrent");
      currentProfileStatus.textContent = t("profiles.currentHint");
    }
    if (!managedProfileItem()) {
      syncManagedProfile();
    }
    if (profiles.length === 0) {
      const empty = document.createElement("p");
      empty.className = "manager-empty";
      empty.textContent = t("profiles.noProfiles");
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
      choose.className = "button button-secondary";
      choose.type = "button";
      choose.textContent = t(selected ? "action.selected" : "action.select");
      choose.disabled = selected;
      choose.addEventListener("click", () => selectProfile(item.ref));
      row.append(text, choose);
      if (!canMutateProfile(item.ref) && item.launchable) {
        const switchButton = document.createElement("button");
        switchButton.className = "button button-primary";
        switchButton.type = "button";
        switchButton.textContent = t("action.switchToProfile");
        switchButton.disabled = contextSwitchInFlight;
        switchButton.dataset.runContextSwitch = "true";
        switchButton.addEventListener("click", () => void switchToProfile(item.ref));
        row.append(switchButton);
      }
      profileList.append(row);
    }
    renderProfileDetail();
    renderPluginPanel();
  }

  function renderRuntimes() {
    renderRuntimePreparations();
    const selectedRelease = runtimeVersion.value;
    runtimeVersion.replaceChildren();
    for (const release of snapshot?.dshReleases ?? []) {
      const option = document.createElement("option");
      option.value = release.version;
      option.textContent = `${release.version}${release.tags?.length ? ` · ${release.tags.join(", ")}` : ""}`;
      runtimeVersion.append(option);
    }
    if (selectedRelease) runtimeVersion.value = selectedRelease;
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
      const artifactSource = item.installSource ? runtimeArtifactSourceLabel(item.installSource) : "";
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
        switchButton.addEventListener("click", () => void switchRunContext({...snapshot!.configured!, runtimeId: item.id}));
        row.append(switchButton);
      }
      if (item.removable) {
        const removeButton = document.createElement("button");
        removeButton.className = "button button-secondary";
        removeButton.type = "button";
        removeButton.textContent = t("action.remove");
        removeButton.dataset.contextMutation = "true";
        const protectedRuntime = [snapshot?.configured, snapshot?.current, snapshot?.knownGood].some((target) => target?.runtimeId === item.id);
        removeButton.disabled = contextSwitchInFlight || protectedRuntime;
        removeButton.addEventListener("click", () => void (async () => {
          if (contextSwitchInFlight) {
            return;
          }
          removeButton.disabled = true;
          try {
            snapshot = await removeRuntime(item.id);
            setFeedback(t("feedback.removedRuntime", {version: item.version}), "success");
            renderSelection();
            renderProfiles();
            renderRuntimes();
            renderDataDirectories();
          } catch (error) {
            setFeedback(managerErrorMessage(error, "error.removeRuntime"), "error");
            console.error("Could not remove DSH runtime", error);
          } finally {
            removeButton.disabled = contextSwitchInFlight || protectedRuntime;
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
        useButton.addEventListener("click", () => void switchRunContext({...snapshot!.configured!, node: {kind: NodeSelectionKind.NodeSelectionSystem}}));
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
        switchButton.addEventListener("click", () => void switchRunContext({...snapshot!.configured!, node: {kind: NodeSelectionKind.NodeSelectionManaged, installationId: item.id}}));
        row.append(switchButton);
      }
      if (item.removable) {
        const removeButton = document.createElement("button");
        removeButton.className = "button button-secondary";
        removeButton.type = "button";
        removeButton.textContent = t("action.remove");
        const protectedNode = [snapshot?.configured, snapshot?.current, snapshot?.knownGood].some((target) => target?.node.kind === NodeSelectionKind.NodeSelectionManaged && target.node.installationId === item.id);
        removeButton.disabled = contextSwitchInFlight || protectedNode;
        removeButton.addEventListener("click", () => void (async () => {
          try { snapshot = await removeNode(item.id); renderSelection(); renderRuntimes(); }
          catch (error) { setFeedback(managerErrorMessage(error, "error.removeNode"), "error"); }
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

  function renderDataDirectories() {
    dataDirectoryList.replaceChildren();
    const dataDirectories = snapshot?.dataDirectories ?? [];
    if (dataDirectories.length === 0) {
      const empty = document.createElement("p");
      empty.className = "manager-empty";
      empty.textContent = t("dataDirectories.noDirectories");
      dataDirectoryList.append(empty);
      return;
    }
    for (const item of dataDirectories) {
      const selected = item.id === snapshot?.configured?.profile.dataDirectoryId;
      const row = document.createElement("div");
      row.className = `manager-list-item${selected ? " is-selected" : ""}`;
      const text = document.createElement("div");
      const name = document.createElement("strong");
      name.textContent = `${item.name}${selected ? ` · ${t("action.selected")}` : ""}`;
      const detail = document.createElement("span");
      detail.className = "manager-data-directory-detail";
      detail.textContent = `${dataDirectoryOwnershipLabel(item.ownership)} · ${item.path}`;
      detail.title = item.path;
      detail.setAttribute("aria-label", `${dataDirectoryOwnershipLabel(item.ownership)}: ${item.path}`);
      text.append(name, detail);
      row.append(text);
      if (item.ownership === DataDirectoryOwnership.DataDirectoryOwnershipUser) {
        const removeButton = document.createElement("button");
        removeButton.className = "button button-secondary";
        removeButton.type = "button";
        removeButton.textContent = t("action.unregister");
        removeButton.dataset.contextMutation = "true";
        removeButton.disabled = contextSwitchInFlight;
        removeButton.addEventListener("click", () => void (async () => {
          if (contextSwitchInFlight) {
            return;
          }
          removeButton.disabled = true;
          try {
            snapshot = await removeDataDirectory(item.id);
            setFeedback(t("feedback.unregisteredDataDirectory", {name: item.name}), "success");
            renderSelection();
            renderProfiles();
            renderRuntimes();
            renderDataDirectories();
          } catch (error) {
            setFeedback(managerErrorMessage(error, "error.unregisterDataDirectory"), "error");
            console.error("Could not unregister DSH data directory", error);
          } finally {
            removeButton.disabled = contextSwitchInFlight;
          }
        })());
        row.append(removeButton);
      }
      dataDirectoryList.append(row);
    }
  }

  async function refresh(preferredProfile?: ManagerProfileRef) {
    if (!contextSwitchInFlight) {
      setFeedback("");
    }
    try {
      [snapshot, hostStatus] = await Promise.all([getSnapshot(), getHostStatus()]);
      applyTheme(snapshot.theme);
      renderSelection();
      syncManagedProfile(preferredProfile);
      renderOverview();
      renderProfiles();
      renderRuntimes();
      renderDataDirectories();
      showSection(currentSection);
      themeSyncAvailable = true;
      startThemeSync();
    } catch (error) {
      setFeedback(managerErrorMessage(error, "error.loadDshData"), "error");
      console.error("Could not read DSH manager snapshot", error);
    }
    await Promise.all([settings.refresh(), notifications.refresh()]);
  }

  for (const item of navItems) {
    item.addEventListener("click", () => showSection(sectionName(item.dataset.managerSection ?? null)));
  }
  Events.On("lifecycle", () => void refresh());
  Events.On("acquisition", (event) => {
    const preparation = acquisitionPreparation(event.data as OperationStatus);
    runtimePreparations[preparationKind(preparation)] = preparation;
    renderRuntimePreparations();
  });
  showSection(currentSection);
  subscribeLocale(() => {
    showSection(currentSection);
    renderSelection();
    renderOverview();
    renderProfiles();
    renderRuntimes();
    renderDataDirectories();
  });

  dataDirectory.addEventListener("change", () => {
    fillProfiles();
    void switchRunContext();
  });
  runtime.addEventListener("change", () => void switchRunContext());
  node.addEventListener("change", () => void switchRunContext());
  profile.addEventListener("change", () => void switchRunContext());
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
    profileBackup.disabled = disabled || !item?.exists || item?.kind !== "custom" || mutationBlocked();
    profileDelete.disabled = disabled || !item?.deletable || mutationBlocked();
    for (const button of Array.from(profilePlugins.querySelectorAll<HTMLButtonElement>("[data-plugin-remove]"))) {
      button.disabled = disabled;
    }
  }

  async function mutatePlugin(operation: "install" | "upgrade" | "remove", packageOverride?: string, sourceButton?: HTMLButtonElement) {
    const packageSpec = (packageOverride ?? packageInput.value).trim();
    if (!packageSpec) {
      setFeedback(t("error.enterPackage"), "error");
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
      setFeedback(result.restartRequired
        ? t("feedback.pluginChangedRestart")
        : t(operation === "install" ? "feedback.pluginInstalled" : operation === "upgrade" ? "feedback.pluginUpgraded" : "feedback.pluginRemoved", {profile: result.profile.name}), "success");
    } catch (error) {
      setFeedback(managerErrorMessage(error, "error.changePlugins"), "error");
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
      setFeedback(t("error.profileNameRequired"), "error");
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
      renderDataDirectories();
      setFeedback(t("feedback.profileRenamed", {profile: nextName}), "success");
    } catch (error) {
      setFeedback(managerErrorMessage(error, "error.renameProfile"), "error");
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
      renderSelection();
      renderOverview();
      renderProfiles();
      renderRuntimes();
      renderDataDirectories();
      showSection("profiles");
      setFeedback(t("feedback.profileCloned", {profile: result.profile.name}), "success");
    } catch (error) {
      setFeedback(managerErrorMessage(error, "error.cloneProfile"), "error");
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
      renderDataDirectories();
      showSection("profiles");
      setFeedback(t("feedback.profileDeleted", {profile: item.ref.name}), "success");
    } catch (error) {
      setFeedback(managerErrorMessage(error, "error.deleteProfile"), "error");
      console.error("Could not delete DSH profile", error);
    } finally {
      renderProfileDetail();
    }
  })());

  profileBackup.addEventListener("click", () => void (async () => {
    const item = managedProfileItem();
    if (!item?.exists || contextSwitchInFlight) {
      return;
    }
    profileBackup.disabled = true;
    try {
      const result = await backupProfile({profile: item.ref});
      setFeedback(t("feedback.profileBackedUp", {file: result.fileName}), "success");
    } catch (error) {
      setFeedback(managerErrorMessage(error, "error.backupProfile"), "error");
      console.error("Could not back up DSH profile", error);
    } finally {
      renderProfileDetail();
    }
  })());

  registerDataDirectoryButton.addEventListener("click", () => void (async () => {
    if (contextSwitchInFlight) {
      return;
    }
    if (!newDataDirectoryId.value.trim() || !newDataDirectoryName.value.trim() || !newDataDirectoryPath.value.trim()) {
      setFeedback(t("error.completeDataDirectory"), "error");
      return;
    }
    registerDataDirectoryButton.disabled = true;
    try {
      snapshot = await registerDataDirectory({
        id: newDataDirectoryId.value.trim(),
        name: newDataDirectoryName.value.trim(),
        path: newDataDirectoryPath.value.trim(),
        ownership: DataDirectoryOwnership.DataDirectoryOwnershipUser
      });
      setFeedback(t("feedback.registeredDataDirectory", {id: newDataDirectoryId.value.trim()}), "success");
      newDataDirectoryPath.value = "";
      newDataDirectoryId.value = "";
      newDataDirectoryName.value = "";
      renderSelection();
      renderOverview();
      renderProfiles();
      renderRuntimes();
      renderDataDirectories();
    } catch (error) {
      setFeedback(managerErrorMessage(error, "error.registerDataDirectory"), "error");
      console.error("Could not register DSH data directory", error);
    } finally {
      registerDataDirectoryButton.disabled = contextSwitchInFlight;
    }
  })());

  installRuntimeButton.addEventListener("click", () => void (async () => {
    if (contextSwitchInFlight || runtimeInstallInFlight) {
      return;
    }
    const version = runtimeVersion.value.trim();
    if (!version) {
      setFeedback(t("error.noDshRelease"), "error");
      return;
    }
    runtimeInstallInFlight = true;
    setContextControlsDisabled(true);
    renderRuntimePreparations();
    try {
      snapshot = await installRuntime(version);
      setFeedback(t("feedback.installedRuntime", {version}), "success");
      runtimeVersion.value = "";
      renderSelection();
      renderOverview();
      renderProfiles();
      renderRuntimes();
      renderDataDirectories();
    } catch (error) {
      setFeedback(managerErrorMessage(error, "error.installRuntime"), "error");
      console.error("Could not install DSH runtime", error);
    } finally {
      runtimeInstallInFlight = false;
      setContextControlsDisabled(contextSwitchInFlight);
      renderRuntimePreparations();
    }
  })());

  refreshDSHButton.addEventListener("click", () => void (async () => {
	refreshDSHButton.disabled = true;
	try { snapshot = await refreshDSHReleases(); renderRuntimes(); setFeedback(t("feedback.dshReleasesRefreshed"), "success"); }
	catch (error) { setFeedback(managerErrorMessage(error, "error.refreshDshReleases"), "error"); }
	finally { refreshDSHButton.disabled = false; }
  })());

  installNodeButton.addEventListener("click", () => void (async () => {
	if (runtimeInstallInFlight) return;
	runtimeInstallInFlight = true;
	setContextControlsDisabled(true);
	try { snapshot = await installLatestNode(); renderSelection(); renderRuntimes(); setFeedback(t("feedback.installedNode"), "success"); }
	catch (error) { setFeedback(managerErrorMessage(error, "error.installNode"), "error"); }
	finally { runtimeInstallInFlight = false; setContextControlsDisabled(contextSwitchInFlight); renderRuntimePreparations(); }
  })());

  switchRetry.addEventListener("click", () => void (async () => {
	try { snapshot = await retryLastSwitch(); await refreshCurrentPluginObservation(); renderSelection(); renderOverview(); renderProfiles(); renderRuntimes(); }
	catch (error) { setFeedback(managerErrorMessage(error, "error.switchRunContext"), "error"); }
  })());

  switchRestore.addEventListener("click", () => void (async () => {
	try { snapshot = await restoreKnownGood(); await refreshCurrentPluginObservation(); renderSelection(); renderOverview(); renderProfiles(); renderRuntimes(); }
	catch (error) { setFeedback(managerErrorMessage(error, "error.switchRunContext"), "error"); }
  })());

  for (const cancelRuntimeButton of cancelRuntimeButtons) {
    cancelRuntimeButton.addEventListener("click", () => void (async () => {
      if (!runtimeInstallInFlight) {
        return;
      }
      cancelRuntimeButton.disabled = true;
      try {
        await cancelRuntime();
      } catch (error) {
        setFeedback(managerErrorMessage(error, "error.cancelRuntime"), "error");
        console.error("Could not cancel runtime preparation", error);
      }
    })());
  }

  void refresh(managedProfile);
}
