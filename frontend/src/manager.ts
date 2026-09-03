import {Events} from "@wailsio/runtime";

import {ManagerService} from "../bindings/github.com/local/work/internal/app";
import {HomeOwnership, type HomeInfo, type LaunchSelection, type PluginInfo, type PluginResult, type ProfileInfo, type ProfileRef, type RuntimeInfo, type Snapshot} from "../bindings/github.com/local/work/internal/dshmanager";
import {mountNotifications, mountSettings} from "./settings";
import {buildOverviewModel, type OverviewLane} from "./overview";
import {applyTheme} from "./theme";
import {subscribeLocale, t} from "./i18n";

export type ManagerProfileRef = ProfileRef;
export type ManagerLaunchSelection = LaunchSelection;
export type ManagerPlugin = PluginInfo;
export type ManagerProfile = ProfileInfo;
export type ManagerRuntime = RuntimeInfo;
export type ManagerHome = HomeInfo;
export type ManagerSnapshot = Snapshot;
export type ManagerPluginResult = PluginResult;

const getSnapshot = ManagerService.GetSnapshot;
const getTheme = ManagerService.GetTheme;
const setDesiredSelection = ManagerService.SetDesiredSelection;
const installPlugin = ManagerService.InstallPlugin;
const removePlugin = ManagerService.RemovePlugin;
const renameProfile = ManagerService.RenameProfile;
const installRuntime = ManagerService.InstallRuntime;
const removeRuntime = ManagerService.RemoveRuntime;
const registerHome = ManagerService.RegisterHome;
const removeHome = ManagerService.RemoveHome;

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

function homeOwnershipLabel(value: HomeOwnership): string {
  if (value === HomeOwnership.HomeOwnershipWork) {
    return "Work";
  }
  if (value === HomeOwnership.HomeOwnershipUser) {
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

type ManagerSection = "overview" | "profiles" | "runtimes" | "homes" | "settings" | "notifications";

function sectionName(value: string | null): ManagerSection {
  if (value === "plugins") {
    return "profiles";
  }
  if (value === "overview" || value === "profiles" || value === "runtimes" || value === "homes" || value === "settings" || value === "notifications") {
    return value;
  }
  return "overview";
}

export function mountManager() {
  const runtime = document.getElementById("manager-runtime") as HTMLSelectElement;
  const home = document.getElementById("manager-home") as HTMLSelectElement;
  const profile = document.getElementById("manager-profile") as HTMLSelectElement;
  const workspace = document.getElementById("manager-workspace") as HTMLInputElement;
  const save = document.getElementById("manager-save") as HTMLButtonElement;
  const packageInput = document.getElementById("manager-plugin-package") as HTMLInputElement;
  const install = document.getElementById("manager-plugin-install") as HTMLButtonElement;
  const newHomePath = document.getElementById("manager-new-home-path") as HTMLInputElement;
  const newHomeId = document.getElementById("manager-new-home-id") as HTMLInputElement;
  const newHomeName = document.getElementById("manager-new-home-name") as HTMLInputElement;
  const registerHomeButton = document.getElementById("manager-register-home") as HTMLButtonElement;
  const newProfileHome = document.getElementById("manager-new-profile-home") as HTMLSelectElement;
  const newProfile = document.getElementById("manager-new-profile") as HTMLInputElement;
  const newProfilePackage = document.getElementById("manager-new-profile-package") as HTMLInputElement;
  const createProfile = document.getElementById("manager-create-profile") as HTMLButtonElement;
  const runtimeVersion = document.getElementById("manager-runtime-version") as HTMLInputElement;
  const installRuntimeButton = document.getElementById("manager-runtime-install") as HTMLButtonElement;
  const feedback = document.getElementById("manager-feedback") as HTMLParagraphElement;
  const managerTitle = document.getElementById("manager-title") as HTMLHeadingElement;
  const currentRuntime = document.getElementById("manager-current-runtime") as HTMLElement;
  const currentHome = document.getElementById("manager-current-home") as HTMLElement;
  const currentProfile = document.getElementById("manager-current-profile") as HTMLElement;
  const currentWorkspace = document.getElementById("manager-current-workspace") as HTMLElement;
  const currentState = document.getElementById("manager-current-state") as HTMLElement;
  const nextLaunch = document.getElementById("manager-next-launch") as HTMLElement;
  const nextRuntime = document.getElementById("manager-next-runtime") as HTMLElement;
  const nextHome = document.getElementById("manager-next-home") as HTMLElement;
  const nextProfile = document.getElementById("manager-next-profile") as HTMLElement;
  const nextWorkspace = document.getElementById("manager-next-workspace") as HTMLElement;
  const selectedProfileLabel = document.getElementById("manager-selected-profile") as HTMLElement;
  const profileScopeNote = document.getElementById("manager-profile-scope-note") as HTMLParagraphElement;
  const profileEmpty = document.getElementById("manager-profile-empty") as HTMLElement;
  const profileDetail = document.getElementById("manager-profile-detail") as HTMLElement;
  const profileName = document.getElementById("manager-profile-name") as HTMLInputElement;
  const profileRename = document.getElementById("manager-profile-rename") as HTMLButtonElement;
  const profilePlugins = document.getElementById("manager-profile-plugins") as HTMLDivElement;
  const profileList = document.getElementById("manager-profiles") as HTMLDivElement;
  const runtimeList = document.getElementById("manager-runtimes") as HTMLDivElement;
  const homeList = document.getElementById("manager-homes") as HTMLDivElement;
  const navItems = Array.from(document.querySelectorAll<HTMLButtonElement>("[data-manager-section]"));
  const panels = Array.from(document.querySelectorAll<HTMLElement>("[data-manager-panel]"));
  const query = new URLSearchParams(window.location.search);
  const initialHome = query.get("home")?.trim();
  const initialProfile = query.get("profile")?.trim();
  let currentSection = sectionName(query.get("section"));
  let snapshot: ManagerSnapshot | undefined;
  let managedProfile: ManagerProfileRef | undefined = initialHome && initialProfile
    ? {homeId: initialHome, name: initialProfile}
    : undefined;
  let themeSyncTimer: number | undefined;
  let themeSyncAvailable = false;

  const sectionCopy: Record<ManagerSection, {title: string}> = {
    overview: {title: "manager.overview"},
    settings: {title: "manager.general"},
    profiles: {title: "manager.profiles"},
    runtimes: {title: "manager.runtimes"},
    homes: {title: "manager.homes"},
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
    return !!left && !!right && left.homeId === right.homeId && left.name === right.name;
  }

  function managedProfileItem(): ManagerProfile | undefined {
    if (!managedProfile) {
      return undefined;
    }
    return (snapshot?.profiles ?? []).find((item) => sameProfileRef(item.ref, managedProfile));
  }

  function homeLabel(homeId: string): string {
    return (snapshot?.homes ?? []).find((item) => item.id === homeId)?.name ?? homeId;
  }

  function renderOverviewLane(lane: OverviewLane, fields: {runtime: HTMLElement; home: HTMLElement; profile: HTMLElement; workspace: HTMLElement}) {
    fields.runtime.textContent = lane.runtime
      ? `${lane.runtime.version} · ${t(lane.runtime.installed ? "value.verified" : "value.unverified")}`
      : lane.selection?.runtimeId || t("value.noRuntime");
    fields.home.textContent = lane.home?.name ?? lane.selection?.profile.homeId ?? t("value.noHome");
    fields.profile.textContent = lane.selection?.profile.name ?? t("value.noProfile");
    fields.workspace.textContent = lane.selection?.workspace || t("value.noWorkspace");
  }

  function renderOverview() {
    if (!snapshot) {
      return;
    }
    const model = buildOverviewModel(snapshot);
    renderOverviewLane(model.current, {
      runtime: currentRuntime,
      home: currentHome,
      profile: currentProfile,
      workspace: currentWorkspace
    });
    currentState.textContent = t({
      active: "value.active",
      "restart-required": "value.restartRequired",
      "not-running": "value.notRunning",
      "no-target": "value.noLaunchTarget"
    }[model.state]);
    nextLaunch.hidden = !model.next;
    if (model.next) {
      renderOverviewLane(model.next, {
        runtime: nextRuntime,
        home: nextHome,
        profile: nextProfile,
        workspace: nextWorkspace
      });
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

  function renderPluginList(item: ManagerProfile) {
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
      const removeButton = document.createElement("button");
      removeButton.className = "button button-secondary";
      removeButton.type = "button";
      removeButton.textContent = t("action.remove");
      removeButton.dataset.pluginRemove = "true";
      removeButton.addEventListener("click", () => void mutatePlugin("remove", plugin.package || plugin.name, removeButton));
      row.append(text, removeButton);
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
      ? `${homeLabel(item.ref.homeId)} · ${profileKindLabel(item.kind)}`
      : `${homeLabel(item.ref.homeId)} · ${profileKindLabel(item.kind)} · ${t("profiles.fixedName")}`;
    profileScopeNote.textContent = profileState;
    profileName.value = item.ref.name;
    profileName.disabled = !item.renamable;
    profileRename.disabled = !item.renamable;
    renderPluginList(item);
  }

  function fillProfiles(preferred?: ManagerProfileRef) {
    profile.replaceChildren();
    const profiles = (snapshot?.profiles ?? []).filter((item) => item.ref.homeId === home.value);
    if (profiles.length === 0) {
      const option = document.createElement("option");
      option.value = "";
      option.textContent = t("profiles.noInHome");
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
    if (preferred && preferred.homeId === home.value && profiles.some((item) => item.ref.name === preferred.name)) {
      profile.value = preferred.name;
    } else if (!profiles.some((item) => item.ref.name === profile.value)) {
      profile.value = profiles[0].ref.name;
    }
  }

  function renderSelection() {
    const desired = snapshot?.desired;
    runtime.replaceChildren();
    for (const item of snapshot?.runtimes ?? []) {
      const option = document.createElement("option");
      option.value = item.id;
      option.textContent = `${item.version}${item.installed ? "" : ` · ${t("value.unverified")}`}`;
      runtime.append(option);
    }
    home.replaceChildren();
    for (const item of snapshot?.homes ?? []) {
      const option = document.createElement("option");
      option.value = item.id;
      option.textContent = item.name;
      home.append(option);
    }
    if (desired) {
      runtime.value = desired.runtimeId;
      home.value = desired.profile.homeId;
      workspace.value = desired.workspace;
    }
    if (!runtime.value && runtime.options.length > 0) {
      runtime.selectedIndex = 0;
    }
    if (!home.value && home.options.length > 0) {
      home.selectedIndex = 0;
    }
    fillProfiles(desired?.profile);
    newProfileHome.replaceChildren();
    for (const item of snapshot?.homes ?? []) {
      const option = document.createElement("option");
      option.value = item.id;
      option.textContent = item.name;
      newProfileHome.append(option);
    }
    if (desired?.profile.homeId && (snapshot?.homes ?? []).some((item) => item.id === desired.profile.homeId)) {
      newProfileHome.value = desired.profile.homeId;
    } else if (home.value) {
      newProfileHome.value = home.value;
    } else if (newProfileHome.options.length > 0) {
      newProfileHome.selectedIndex = 0;
    }
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
        home: homeLabel(item.ref.homeId),
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
      const selected = item.id === snapshot?.desired?.runtimeId;
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
        removeButton.addEventListener("click", () => void (async () => {
          removeButton.disabled = true;
          try {
            snapshot = await removeRuntime(item.id);
            setFeedback(t("feedback.removedRuntime", {version: item.version}), "success");
            renderSelection();
            renderProfiles();
            renderRuntimes();
            renderHomes();
          } catch (error) {
            setFeedback(managerErrorMessage(error, "error.removeRuntime"), "error");
            console.error("Could not remove DSH runtime", error);
          } finally {
            removeButton.disabled = false;
          }
        })());
        row.append(removeButton);
      }
      runtimeList.append(row);
    }
  }

  function renderHomes() {
    homeList.replaceChildren();
    const homes = snapshot?.homes ?? [];
    if (homes.length === 0) {
      const empty = document.createElement("p");
      empty.className = "manager-empty";
      empty.textContent = t("homes.noHomes");
      homeList.append(empty);
      return;
    }
    for (const item of homes) {
      const selected = item.id === snapshot?.desired?.profile.homeId;
      const row = document.createElement("div");
      row.className = `manager-list-item${selected ? " is-selected" : ""}`;
      const text = document.createElement("div");
      const name = document.createElement("strong");
      name.textContent = `${item.name}${selected ? ` · ${t("action.selected")}` : ""}`;
      const detail = document.createElement("span");
      detail.textContent = `${homeOwnershipLabel(item.ownership)} · ${item.path}`;
      text.append(name, detail);
      row.append(text);
      if (item.ownership === HomeOwnership.HomeOwnershipUser) {
        const removeButton = document.createElement("button");
        removeButton.className = "button button-secondary";
        removeButton.type = "button";
        removeButton.textContent = t("action.unregister");
        removeButton.addEventListener("click", () => void (async () => {
          removeButton.disabled = true;
          try {
            snapshot = await removeHome(item.id);
            setFeedback(t("feedback.unregisteredHome", {name: item.name}), "success");
            renderSelection();
            renderProfiles();
            renderRuntimes();
            renderHomes();
          } catch (error) {
            setFeedback(managerErrorMessage(error, "error.unregisterHome"), "error");
            console.error("Could not unregister DSH home", error);
          } finally {
            removeButton.disabled = false;
          }
        })());
        row.append(removeButton);
      }
      homeList.append(row);
    }
  }

  async function refresh(preferredProfile?: ManagerProfileRef) {
    setFeedback("");
    try {
      snapshot = await getSnapshot();
      applyTheme(snapshot.theme);
      renderSelection();
      syncManagedProfile(preferredProfile);
      renderOverview();
      renderProfiles();
      renderRuntimes();
      renderHomes();
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
    renderHomes();
  });

  home.addEventListener("change", () => {
    fillProfiles();
  });
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

  save.addEventListener("click", () => void (async () => {
    if (!runtime.value || !home.value || !profile.value) {
      setFeedback(t("error.selectLaunchTarget"), "error");
      return;
    }
    save.disabled = true;
    try {
      snapshot = await setDesiredSelection({
        runtimeId: runtime.value,
        profile: {homeId: home.value, name: profile.value},
        workspace: workspace.value
      });
      applyTheme(snapshot.theme);
      setFeedback(snapshot.active ? t("feedback.savedRestart") : t("feedback.savedNextStart"), "success");
      renderSelection();
      renderOverview();
      renderProfiles();
      renderRuntimes();
      renderHomes();
    } catch (error) {
      setFeedback(managerErrorMessage(error, "error.saveLaunchTarget"), "error");
      console.error("Could not save DSH launch selection", error);
    } finally {
      save.disabled = false;
    }
  })());

  function pluginRuntimeId(): string {
    // Profile/plugin management has its own profile context. Use the
    // persisted launch runtime (or the active one) rather than an unsaved
    // General-form draft so changing launch settings cannot retarget a
    // profile operation by accident.
    const preferred = snapshot?.desired?.runtimeId || snapshot?.active?.runtimeId || "";
    const selected = (snapshot?.runtimes ?? []).find((item) => item.id === preferred && item.installed);
    return selected?.id ??
      (snapshot?.runtimes ?? []).find((item) => item.installed)?.id ??
      preferred;
  }

  function explicitPluginTarget() {
    if (!managedProfile) {
      throw new Error(t("error.selectPluginTarget"));
    }
    return {runtimeId: pluginRuntimeId(), profile: {...managedProfile}};
  }

  function setPluginControlsDisabled(disabled: boolean) {
    install.disabled = disabled;
    profileName.disabled = disabled || !managedProfileItem()?.renamable;
    profileRename.disabled = disabled || !managedProfileItem()?.renamable;
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
      managedProfile = {homeId: item.ref.homeId, name: nextName};
      applyTheme(snapshot.theme);
      renderSelection();
      renderOverview();
      renderProfiles();
      renderRuntimes();
      renderHomes();
      setFeedback(t("feedback.profileRenamed", {profile: nextName}), "success");
    } catch (error) {
      setFeedback(managerErrorMessage(error, "error.renameProfile"), "error");
      console.error("Could not rename DSH profile", error);
    } finally {
      renderProfileDetail();
    }
  })());

  registerHomeButton.addEventListener("click", () => void (async () => {
    if (!newHomeId.value.trim() || !newHomeName.value.trim() || !newHomePath.value.trim()) {
      setFeedback(t("error.completeHome"), "error");
      return;
    }
    registerHomeButton.disabled = true;
    try {
      snapshot = await registerHome({
        id: newHomeId.value.trim(),
        name: newHomeName.value.trim(),
        path: newHomePath.value.trim(),
        ownership: HomeOwnership.HomeOwnershipUser
      });
      setFeedback(t("feedback.registeredHome", {id: newHomeId.value.trim()}), "success");
      newHomePath.value = "";
      newHomeId.value = "";
      newHomeName.value = "";
      renderSelection();
      renderOverview();
      renderProfiles();
      renderRuntimes();
      renderHomes();
    } catch (error) {
      setFeedback(managerErrorMessage(error, "error.registerHome"), "error");
      console.error("Could not register DSH home", error);
    } finally {
      registerHomeButton.disabled = false;
    }
  })());

  createProfile.addEventListener("click", () => void (async () => {
    const name = newProfile.value.trim();
    const packageSpec = newProfilePackage.value.trim();
    const homeId = newProfileHome.value;
    if (!homeId || !name || !packageSpec) {
      setFeedback(t("error.createProfileFields"), "error");
      return;
    }
    createProfile.disabled = true;
    try {
      const result = await installPlugin({
        target: {runtimeId: pluginRuntimeId(), profile: {homeId, name}},
        package: packageSpec
      });
      await refresh({homeId, name});
      selectProfile({homeId, name});
      setFeedback(result.restartRequired
        ? t("feedback.profileCreatedRestart")
        : t("feedback.profileCreated"), "success");
      newProfile.value = "";
      newProfilePackage.value = "";
    } catch (error) {
      setFeedback(managerErrorMessage(error, "error.createProfile"), "error");
      console.error("Could not create DSH profile", error);
    } finally {
      createProfile.disabled = false;
    }
  })());

  installRuntimeButton.addEventListener("click", () => void (async () => {
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
      renderHomes();
    } catch (error) {
      setFeedback(managerErrorMessage(error, "error.installRuntime"), "error");
      console.error("Could not install DSH runtime", error);
    } finally {
      installRuntimeButton.disabled = false;
    }
  })());

  void refresh(managedProfile);
}
