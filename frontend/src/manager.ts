import {ManagerService} from "../bindings/github.com/local/work/internal/app";
import {HomeOwnership, type HomeInfo, type LaunchSelection, type PluginInfo, type PluginResult, type ProfileInfo, type ProfileRef, type RuntimeInfo, type Snapshot} from "../bindings/github.com/local/work/internal/dshmanager";
import {mountSettings} from "./settings";
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

type ManagerSection = "overview" | "profiles" | "runtimes" | "homes" | "settings";

function sectionName(value: string | null): ManagerSection {
  if (value === "plugins") {
    return "profiles";
  }
  if (value === "overview" || value === "profiles" || value === "runtimes" || value === "homes" || value === "settings") {
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
  const remove = document.getElementById("manager-plugin-remove") as HTMLButtonElement;
  const newHomePath = document.getElementById("manager-new-home-path") as HTMLInputElement;
  const newHomeId = document.getElementById("manager-new-home-id") as HTMLInputElement;
  const newHomeName = document.getElementById("manager-new-home-name") as HTMLInputElement;
  const registerHomeButton = document.getElementById("manager-register-home") as HTMLButtonElement;
  const newProfile = document.getElementById("manager-new-profile") as HTMLInputElement;
  const newProfilePackage = document.getElementById("manager-new-profile-package") as HTMLInputElement;
  const createProfile = document.getElementById("manager-create-profile") as HTMLButtonElement;
  const runtimeVersion = document.getElementById("manager-runtime-version") as HTMLInputElement;
  const installRuntimeButton = document.getElementById("manager-runtime-install") as HTMLButtonElement;
  const feedback = document.getElementById("manager-feedback") as HTMLParagraphElement;
  const managerTitle = document.getElementById("manager-title") as HTMLHeadingElement;
  const selectionRuntime = document.getElementById("manager-selection-runtime") as HTMLElement;
  const selectionHome = document.getElementById("manager-selection-home") as HTMLElement;
  const selectionProfile = document.getElementById("manager-selection-profile") as HTMLElement;
  const selectionWorkspace = document.getElementById("manager-selection-workspace") as HTMLElement;
  const selectionState = document.getElementById("manager-selection-state") as HTMLElement;
  const selectedProfileLabel = document.getElementById("manager-selected-profile") as HTMLElement;
  const profileScopeNote = document.getElementById("manager-profile-scope-note") as HTMLParagraphElement;
  const profileList = document.getElementById("manager-profiles") as HTMLDivElement;
  const runtimeList = document.getElementById("manager-runtimes") as HTMLDivElement;
  const homeList = document.getElementById("manager-homes") as HTMLDivElement;
  const navItems = Array.from(document.querySelectorAll<HTMLButtonElement>("[data-manager-section]"));
  const panels = Array.from(document.querySelectorAll<HTMLElement>("[data-manager-panel]"));
  let currentSection = sectionName(new URLSearchParams(window.location.search).get("section"));
  let snapshot: ManagerSnapshot | undefined;
  let themeSyncTimer: number | undefined;
  let themeSyncAvailable = false;

  const sectionCopy: Record<ManagerSection, {title: string}> = {
    overview: {title: "manager.overview"},
    settings: {title: "manager.general"},
    profiles: {title: "manager.profiles"},
    runtimes: {title: "manager.runtimes"},
    homes: {title: "manager.homes"}
  };

  function setFeedback(message: string, tone: "neutral" | "success" | "error" = "neutral") {
    feedback.textContent = message;
    feedback.className = tone === "neutral" ? "manager-feedback" : `manager-feedback is-${tone}`;
    feedback.hidden = message.length === 0;
  }

  const settings = mountSettings(setFeedback);

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

  function selectedProfile(): ManagerProfile | undefined {
    return (snapshot?.profiles ?? []).find((item) => item.ref.homeId === home.value && item.ref.name === profile.value);
  }

  function renderSelectionSummary() {
    const selectedRuntime = (snapshot?.runtimes ?? []).find((item) => item.id === runtime.value);
    const selectedHome = (snapshot?.homes ?? []).find((item) => item.id === home.value);
    selectionRuntime.textContent = selectedRuntime
      ? `${selectedRuntime.version} · ${t(selectedRuntime.installed ? "value.verified" : "value.unverified")}`
      : t("value.noRuntime");
    selectionHome.textContent = selectedHome?.name ?? t("value.noHome");
    selectionProfile.textContent = profile.value ? `${home.value} / ${profile.value}` : t("value.noProfile");
    selectionWorkspace.textContent = workspace.value || t("value.noWorkspace");
  }

  function renderProfileSelection() {
    const item = selectedProfile();
    if (!item) {
      selectedProfileLabel.textContent = t("value.noProfile");
      profileScopeNote.textContent = t("profiles.choose");
      renderSelectionSummary();
      return;
    }
    selectedProfileLabel.textContent = `${item.ref.homeId} / ${item.ref.name}`;
    profileScopeNote.textContent = item.exists
      ? t("value.ready")
      : t("value.notInitialized");
    renderSelectionSummary();
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
      renderProfileSelection();
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
    renderProfileSelection();
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
    if (snapshot?.active) {
      selectionState.textContent = t("value.activeRestart");
    } else {
      selectionState.textContent = t("value.saved");
    }
    renderSelectionSummary();
  }

  function selectProfile(ref: ManagerProfileRef) {
    home.value = ref.homeId;
    fillProfiles(ref);
    profile.value = ref.name;
    renderProfileSelection();
    showSection("profiles");
  }

  function renderProfiles() {
    profileList.replaceChildren();
    const profiles = snapshot?.profiles ?? [];
    if (profiles.length === 0) {
      const empty = document.createElement("p");
      empty.className = "manager-empty";
      empty.textContent = t("profiles.noProfiles");
      profileList.append(empty);
      return;
    }
    for (const item of profiles) {
      const selected = item.ref.homeId === home.value && item.ref.name === profile.value;
      const row = document.createElement("div");
      row.className = `manager-list-item${selected ? " is-selected" : ""}`;
      const text = document.createElement("div");
      const name = document.createElement("strong");
      name.textContent = `${item.ref.homeId} / ${item.ref.name}`;
      const detail = document.createElement("span");
      const pluginNames = (item.plugins ?? []).map((plugin) => plugin.name).join(", ");
      detail.textContent = t("profiles.pluginDetail", {
        kind: profileKindLabel(item.kind),
        count: item.pluginCount,
        plural: item.pluginCount === 1 ? "" : "s",
        plugins: pluginNames ? ` · ${pluginNames}` : "",
        state: item.exists ? "" : ` · ${t("value.new")}`
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

  async function refresh() {
    setFeedback("");
    try {
      snapshot = await getSnapshot();
      applyTheme(snapshot.theme);
      renderSelection();
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
    await settings.refresh();
  }

  for (const item of navItems) {
    item.addEventListener("click", () => showSection(sectionName(item.dataset.managerSection ?? null)));
  }
  showSection(currentSection);
  subscribeLocale(() => {
    showSection(currentSection);
    renderSelection();
    renderProfiles();
    renderRuntimes();
    renderHomes();
  });

  home.addEventListener("change", () => {
    fillProfiles();
    renderProfiles();
  });
  runtime.addEventListener("change", renderSelectionSummary);
  profile.addEventListener("change", () => {
    renderProfileSelection();
    renderProfiles();
  });
  workspace.addEventListener("input", renderSelectionSummary);
  window.addEventListener("focus", () => void syncTheme());
  document.addEventListener("visibilitychange", () => {
    if (document.visibilityState === "visible") {
      void syncTheme();
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

  function explicitPluginTarget() {
    if (!runtime.value || !home.value || !profile.value) {
      throw new Error(t("error.selectPluginTarget"));
    }
    return {runtimeId: runtime.value, profile: {homeId: home.value, name: profile.value}};
  }

  async function mutatePlugin(operation: "install" | "remove") {
    const packageSpec = packageInput.value.trim();
    if (!packageSpec) {
      setFeedback(t("error.enterPackage"), "error");
      packageInput.focus();
      return;
    }
    install.disabled = true;
    remove.disabled = true;
    try {
      const target = explicitPluginTarget();
      const result = operation === "install"
        ? await installPlugin({target, package: packageSpec})
        : await removePlugin({target, package: packageSpec});
      await refresh();
      setFeedback(result.restartRequired
        ? t("feedback.pluginChangedRestart")
        : t(operation === "install" ? "feedback.pluginInstalled" : "feedback.pluginRemoved", {profile: result.profile.name}), "success");
    } catch (error) {
      setFeedback(managerErrorMessage(error, "error.changePlugins"), "error");
      console.error("Could not change profile plugin", error);
    } finally {
      install.disabled = false;
      remove.disabled = false;
    }
  }

  install.addEventListener("click", () => void mutatePlugin("install"));
  remove.addEventListener("click", () => void mutatePlugin("remove"));

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
    if (!runtime.value || !home.value || !name || !packageSpec) {
      setFeedback(t("error.createProfileFields"), "error");
      return;
    }
    createProfile.disabled = true;
    try {
      const result = await installPlugin({
        target: {runtimeId: runtime.value, profile: {homeId: home.value, name}},
        package: packageSpec
      });
      await refresh();
      selectProfile({homeId: home.value, name});
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

  void refresh();
}
