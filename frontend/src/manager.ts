import {Events} from "@wailsio/runtime";

import {HostService, ManagerService} from "../bindings/github.com/local/dsh-work/internal/app";
import {DataDirectoryOwnership, type DataDirectoryInfo, type PluginInfo, type PluginResult, type ProfileInfo, type ProfileRef, type RunContext, type RuntimeInfo, type Snapshot} from "../bindings/github.com/local/dsh-work/internal/dshmanager";
import {mountNotifications, mountSettings} from "./settings";
import {buildOverviewModel, type OverviewLane} from "./overview";
import {applyTheme} from "./theme";
import {subscribeLocale, t} from "./i18n";
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
const removePlugin = ManagerService.RemovePlugin;
const renameProfile = ManagerService.RenameProfile;
const installRuntime = ManagerService.InstallRuntime;
const removeRuntime = ManagerService.RemoveRuntime;
const registerDataDirectory = ManagerService.RegisterDataDirectory;
const removeDataDirectory = ManagerService.RemoveDataDirectory;

function managerErrorMessage(error: unknown, fallbackKey: string): string {
  let message = "";
  if (error instanceof Error) {
    message = error.message;
  } else if (typeof error === "string") {
    message = error;
  } else if (typeof error === "object" && error !== null && "message" in error && typeof error.message === "string") {
    message = error.message;
  }
  message = message.replace(/[\r\n]+/g, " ").trim();
  return message.length > 0 && message.length <= 240 ? message : t(fallbackKey);
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
    return t("value.managed");
  }
  if (value === "development-fixture") {
    return t("value.development");
  }
  if (value === "system") {
    return t("value.system");
  }
  return t("value.other");
}

type ManagerSection = "overview" | "profiles" | "runtimes" | "data-directories" | "settings" | "notifications";

function sectionName(value: string | null): ManagerSection {
  if (value === "plugins") {
    return "profiles";
  }
  if (value === "overview" || value === "profiles" || value === "runtimes" || value === "data-directories" || value === "settings" || value === "notifications") {
    return value;
  }
  return "overview";
}

export function mountManager() {
  const runtime = document.getElementById("manager-runtime") as HTMLSelectElement;
  const dataDirectory = document.getElementById("manager-data-directory") as HTMLSelectElement;
  const profile = document.getElementById("manager-profile") as HTMLSelectElement;
  const packageInput = document.getElementById("manager-plugin-package") as HTMLInputElement;
  const install = document.getElementById("manager-plugin-install") as HTMLButtonElement;
  const newDataDirectoryPath = document.getElementById("manager-new-data-directory-path") as HTMLInputElement;
  const newDataDirectoryId = document.getElementById("manager-new-data-directory-id") as HTMLInputElement;
  const newDataDirectoryName = document.getElementById("manager-new-data-directory-name") as HTMLInputElement;
  const registerDataDirectoryButton = document.getElementById("manager-register-data-directory") as HTMLButtonElement;
  const runtimeVersion = document.getElementById("manager-runtime-version") as HTMLInputElement;
  const installRuntimeButton = document.getElementById("manager-runtime-install") as HTMLButtonElement;
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
  const profileScopeNote = document.getElementById("manager-profile-scope-note") as HTMLParagraphElement;
  const profileEmpty = document.getElementById("manager-profile-empty") as HTMLElement;
  const profileDetail = document.getElementById("manager-profile-detail") as HTMLElement;
  const profileName = document.getElementById("manager-profile-name") as HTMLInputElement;
  const profileRename = document.getElementById("manager-profile-rename") as HTMLButtonElement;
  const profilePlugins = document.getElementById("manager-profile-plugins") as HTMLDivElement;
  const profileList = document.getElementById("manager-profiles") as HTMLDivElement;
  const runtimeList = document.getElementById("manager-runtimes") as HTMLDivElement;
  const dataDirectoryList = document.getElementById("manager-data-directories") as HTMLDivElement;
  const navItems = Array.from(document.querySelectorAll<HTMLButtonElement>("[data-manager-section]"));
  const panels = Array.from(document.querySelectorAll<HTMLElement>("[data-manager-panel]"));
  const query = new URLSearchParams(window.location.search);
  const initialDataDirectory = query.get("data-directory")?.trim();
  const initialProfile = query.get("profile")?.trim();
  let currentSection = sectionName(query.get("section"));
  let snapshot: ManagerSnapshot | undefined;
  let hostStatus: HostLifecycleStatus | undefined;
  let managedProfile: ManagerProfileRef | undefined = initialDataDirectory && initialProfile
    ? {dataDirectoryId: initialDataDirectory, name: initialProfile}
    : undefined;
  let themeSyncTimer: number | undefined;
  let themeSyncAvailable = false;
  let contextSwitchInFlight = false;

  const sectionCopy: Record<ManagerSection, {title: string}> = {
    overview: {title: "manager.overview"},
    settings: {title: "manager.general"},
    profiles: {title: "manager.profiles"},
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

  function currentRunContext(): ManagerRunContext | undefined {
    return snapshot?.current ?? undefined;
  }

  function canMutateProfile(ref: ManagerProfileRef | undefined): boolean {
    const current = currentRunContext();
    return !contextSwitchInFlight && !!ref && !!current && hostStatus?.state === "Ready" && sameProfileRef(ref, current.profile);
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
    if (hostStatus?.state === "Failed" && hostStatus.error) {
      const failure = hostStatus.error;
      const detail = failure.detail ? ` · ${failure.detail}` : "";
      setFeedback(`${failure.summary} (${failure.code})${detail}`, "error");
    }
  }

  function syncManagedProfile(preferred?: ManagerProfileRef) {
    const profiles = snapshot?.profiles ?? [];
    const candidate = preferred ?? managedProfile;
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
      detail.textContent = plugin.spec || plugin.version || t("value.installed");
      text.append(name, detail);
      if (mutable) {
        const removeButton = document.createElement("button");
        removeButton.className = "button button-secondary";
        removeButton.type = "button";
        removeButton.textContent = t("action.remove");
        removeButton.dataset.pluginRemove = "true";
        removeButton.addEventListener("click", () => void mutatePlugin("remove", plugin.package || plugin.name, removeButton));
        row.append(text, removeButton);
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
      profilePlugins.replaceChildren();
      return;
    }
    selectedProfileLabel.textContent = item.ref.name;
    const profileState = item.renamable
      ? `${dataDirectoryLabel(item.ref.dataDirectoryId)} · ${profileKindLabel(item.kind)}`
      : `${dataDirectoryLabel(item.ref.dataDirectoryId)} · ${profileKindLabel(item.kind)} · ${t("profiles.fixedName")}`;
    const mutable = canMutateProfile(item.ref);
    const current = currentRunContext();
    const isCurrent = !!current && sameProfileRef(item.ref, current.profile);
    const canRename = item.renamable && !isCurrent && !contextSwitchInFlight;
    const accessNote = mutable
      ? t("profiles.currentEditable")
      : item.renamable ? t("profiles.pluginsReadOnly") : t("profiles.readOnly");
    profileScopeNote.textContent = `${profileState} · ${accessNote}`;
    profileName.value = item.ref.name;
    profileName.disabled = !canRename;
    profileRename.disabled = !canRename;
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
    dataDirectory.replaceChildren();
    for (const item of snapshot?.dataDirectories ?? []) {
      const option = document.createElement("option");
      option.value = item.id;
      option.textContent = item.name;
      dataDirectory.append(option);
    }
    if (configured) {
      runtime.value = configured.runtimeId;
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

  function selectedRunContext(): ManagerRunContext | undefined {
    if (!runtime.value || !dataDirectory.value || !profile.value) {
      return undefined;
    }
    return {
      runtimeId: runtime.value,
      profile: {dataDirectoryId: dataDirectory.value, name: profile.value}
    };
  }

  function setContextControlsDisabled(disabled: boolean) {
    runtime.disabled = disabled;
    dataDirectory.disabled = disabled;
    profile.disabled = disabled;
    runtimeVersion.disabled = disabled;
    installRuntimeButton.disabled = disabled;
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
    const runtimeId = snapshot?.configured?.runtimeId || snapshot?.current?.runtimeId;
    if (!runtimeId) {
      setFeedback(t("error.selectRunContext"), "error");
      return;
    }
    await switchRunContext({runtimeId, profile: {...ref}});
  }

  function selectProfile(ref: ManagerProfileRef) {
    managedProfile = {...ref};
    renderProfiles();
    showSection("profiles");
  }

  function renderProfiles() {
    profileList.replaceChildren();
    const profiles = snapshot?.profiles ?? [];
    if (!managedProfileItem()) {
      syncManagedProfile();
    }
    if (profiles.length === 0) {
      const empty = document.createElement("p");
      empty.className = "manager-empty";
      empty.textContent = t("profiles.noProfiles");
      profileList.append(empty);
      renderProfileDetail();
      return;
    }
    for (const item of profiles) {
      const selected = sameProfileRef(item.ref, managedProfile);
      const row = document.createElement("div");
      row.className = `manager-list-item${selected ? " is-selected" : ""}`;
      const text = document.createElement("div");
      const name = document.createElement("strong");
      name.textContent = item.ref.name;
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
  }

  function renderRuntimes() {
    runtimeList.replaceChildren();
    const runtimes = snapshot?.runtimes ?? [];
    if (runtimes.length === 0) {
      const empty = document.createElement("p");
      empty.className = "manager-empty";
      empty.textContent = t("runtimes.noRuntimes");
      runtimeList.append(empty);
      return;
    }
    for (const item of runtimes) {
      const selected = item.id === snapshot?.configured?.runtimeId;
      const row = document.createElement("div");
      row.className = `manager-list-item${selected ? " is-selected" : ""}`;
      const text = document.createElement("div");
      const name = document.createElement("strong");
      name.textContent = `${item.version}${selected ? ` · ${t("action.selected")}` : ""}`;
      const detail = document.createElement("span");
      detail.textContent = `${runtimeSourceLabel(item.source)} · ${t(item.installed ? "value.verified" : "value.unverified")} · ${item.path}`;
      text.append(name, detail);
      row.append(text);
      if (item.removable) {
        const removeButton = document.createElement("button");
        removeButton.className = "button button-secondary";
        removeButton.type = "button";
        removeButton.textContent = t("action.remove");
        removeButton.dataset.contextMutation = "true";
        removeButton.disabled = contextSwitchInFlight;
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
            removeButton.disabled = contextSwitchInFlight;
          }
        })());
        row.append(removeButton);
      }
      runtimeList.append(row);
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
    if (!managedProfile) {
      throw new Error(t("error.selectPluginTarget"));
    }
    if (!canMutateProfile(managedProfile)) {
      throw new Error(t("error.profileReadOnly"));
    }
    return {profile: {...managedProfile}};
  }

  function setPluginControlsDisabled(disabled: boolean) {
    const item = managedProfileItem();
    const current = currentRunContext();
    const isCurrent = !!item && !!current && sameProfileRef(item.ref, current.profile);
    const canRename = !!item?.renamable && !isCurrent && !contextSwitchInFlight;
    install.disabled = disabled || !canMutateProfile(item?.ref);
    packageInput.disabled = disabled || !canMutateProfile(item?.ref);
    profileName.disabled = disabled || !canRename;
    profileRename.disabled = disabled || !canRename;
    for (const button of Array.from(profilePlugins.querySelectorAll<HTMLButtonElement>("[data-plugin-remove]"))) {
      button.disabled = disabled;
    }
  }

  async function mutatePlugin(operation: "install" | "remove", packageOverride?: string, sourceButton?: HTMLButtonElement) {
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
        : await removePlugin({target, package: packageSpec});
      await refresh(target.profile);
      setFeedback(result.restartRequired
        ? t("feedback.pluginChangedRestart")
        : t(operation === "install" ? "feedback.pluginInstalled" : "feedback.pluginRemoved", {profile: result.profile.name}), "success");
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
    if (contextSwitchInFlight) {
      return;
    }
    const version = runtimeVersion.value.trim();
    if (!version) {
      setFeedback(t("error.enterVersion"), "error");
      return;
    }
    installRuntimeButton.disabled = true;
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
      installRuntimeButton.disabled = contextSwitchInFlight;
    }
  })());

  void refresh(managedProfile);
}
