import {ManagerService} from "../bindings/github.com/local/work/internal/app";
import {HomeOwnership, type HomeInfo, type LaunchSelection, type PluginInfo, type PluginResult, type ProfileInfo, type ProfileRef, type RuntimeInfo, type Snapshot} from "../bindings/github.com/local/work/internal/dshmanager";

export type ManagerProfileRef = ProfileRef;
export type ManagerLaunchSelection = LaunchSelection;
export type ManagerPlugin = PluginInfo;
export type ManagerProfile = ProfileInfo;
export type ManagerRuntime = RuntimeInfo;
export type ManagerHome = HomeInfo;
export type ManagerSnapshot = Snapshot;
export type ManagerPluginResult = PluginResult;

const getSnapshot = ManagerService.GetSnapshot;
const setDesiredSelection = ManagerService.SetDesiredSelection;
const installPlugin = ManagerService.InstallPlugin;
const removePlugin = ManagerService.RemovePlugin;
const installRuntime = ManagerService.InstallRuntime;
const removeRuntime = ManagerService.RemoveRuntime;
const registerHome = ManagerService.RegisterHome;
const removeHome = ManagerService.RemoveHome;

function managerErrorMessage(error: unknown, fallback: string): string {
  let message = "";
  if (error instanceof Error) {
    message = error.message;
  } else if (typeof error === "string") {
    message = error;
  } else if (typeof error === "object" && error !== null && "message" in error && typeof error.message === "string") {
    message = error.message;
  }
  message = message.replace(/[\r\n]+/g, " ").trim();
  return message.length > 0 && message.length <= 240 ? message : fallback;
}

function sectionName(value: string | null): "overview" | "profiles" | "runtimes" | "homes" {
  if (value === "plugins") {
    return "profiles";
  }
  if (value === "profiles" || value === "runtimes" || value === "homes") {
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
  const refreshButton = document.getElementById("manager-refresh") as HTMLButtonElement;
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
  const active = document.getElementById("manager-active") as HTMLSpanElement;
  const selectionRuntime = document.getElementById("manager-selection-runtime") as HTMLElement;
  const selectionHome = document.getElementById("manager-selection-home") as HTMLElement;
  const selectionProfile = document.getElementById("manager-selection-profile") as HTMLElement;
  const selectionWorkspace = document.getElementById("manager-selection-workspace") as HTMLElement;
  const selectionNote = document.getElementById("manager-selection-note") as HTMLParagraphElement;
  const selectedProfileLabel = document.getElementById("manager-selected-profile") as HTMLElement;
  const profileScopeNote = document.getElementById("manager-profile-scope-note") as HTMLParagraphElement;
  const profileList = document.getElementById("manager-profiles") as HTMLDivElement;
  const runtimeList = document.getElementById("manager-runtimes") as HTMLDivElement;
  const homeList = document.getElementById("manager-homes") as HTMLDivElement;
  const navItems = Array.from(document.querySelectorAll<HTMLButtonElement>("[data-manager-section]"));
  const panels = Array.from(document.querySelectorAll<HTMLElement>("[data-manager-panel]"));
  let currentSection = sectionName(new URLSearchParams(window.location.search).get("section"));
  let snapshot: ManagerSnapshot | undefined;

  function setFeedback(message: string, tone: "neutral" | "success" | "error" = "neutral") {
    feedback.textContent = message;
    feedback.className = tone === "neutral" ? "manager-feedback" : `manager-feedback is-${tone}`;
  }

  function showSection(next: "overview" | "profiles" | "runtimes" | "homes") {
    currentSection = next;
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
      ? `${selectedRuntime.version} · ${selectedRuntime.installed ? "verified" : "not verified"}`
      : "No runtime selected";
    selectionHome.textContent = selectedHome ? `${selectedHome.name} · ${selectedHome.ownership}` : "Choose a DSH home";
    selectionProfile.textContent = profile.value ? `${home.value} / ${profile.value}` : "Choose a profile";
    selectionWorkspace.textContent = workspace.value || "Workspace path will appear here.";
  }

  function renderProfileSelection() {
    const item = selectedProfile();
    if (!item) {
      selectedProfileLabel.textContent = "Choose a profile above";
      profileScopeNote.textContent = "Plugin changes are sent to DSH with this profile as the explicit target.";
      renderSelectionSummary();
      return;
    }
    selectedProfileLabel.textContent = `${item.ref.homeId} / ${item.ref.name}`;
    profileScopeNote.textContent = item.exists
      ? "Plugin changes are sent to DSH with this profile as the explicit target."
      : "This built-in profile will be initialized by DSH on first use.";
    renderSelectionSummary();
  }

  function fillProfiles(preferred?: ManagerProfileRef) {
    profile.replaceChildren();
    const profiles = (snapshot?.profiles ?? []).filter((item) => item.ref.homeId === home.value);
    if (profiles.length === 0) {
      const option = document.createElement("option");
      option.value = "";
      option.textContent = "No profiles in this home";
      option.disabled = true;
      option.selected = true;
      profile.append(option);
      renderProfileSelection();
      return;
    }
    for (const item of profiles) {
      const option = document.createElement("option");
      option.value = item.ref.name;
      option.textContent = item.exists ? item.ref.name : `${item.ref.name} (created on first use)`;
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
      option.textContent = `${item.version}${item.installed ? "" : " (not verified)"}`;
      runtime.append(option);
    }
    home.replaceChildren();
    for (const item of snapshot?.homes ?? []) {
      const option = document.createElement("option");
      option.value = item.id;
      option.textContent = `${item.name} · ${item.ownership}`;
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
      active.textContent = `Active · ${snapshot.active.profile.name}`;
      active.className = "manager-state ready";
      selectionNote.textContent = "The active DSH Worker is using its current target. Save changes, then restart DSH to apply a new target.";
    } else {
      active.textContent = "Not running";
      active.className = "manager-state";
      selectionNote.textContent = "Selection changes apply to the next DSH Worker generation.";
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
      empty.textContent = "No profiles found in the configured DSH homes.";
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
      detail.textContent = `${item.kind} · ${item.pluginCount} plugin${item.pluginCount === 1 ? "" : "s"}${pluginNames ? ` · ${pluginNames}` : ""}${item.exists ? "" : " · initialized by DSH on first use"}`;
      text.append(name, detail);
      const choose = document.createElement("button");
      choose.className = "button button-secondary";
      choose.type = "button";
      choose.textContent = selected ? "Selected" : "Use profile";
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
      empty.textContent = "No DSH runtimes are in the catalog.";
      runtimeList.append(empty);
      return;
    }
    for (const item of runtimes) {
      const selected = item.id === snapshot?.desired?.runtimeId;
      const row = document.createElement("div");
      row.className = `manager-list-item${selected ? " is-selected" : ""}`;
      const text = document.createElement("div");
      const name = document.createElement("strong");
      name.textContent = `${item.version}${selected ? " · selected" : ""}`;
      const detail = document.createElement("span");
      detail.textContent = `${item.source} · ${item.installed ? "verified" : "verify before launch"} · ${item.path}`;
      text.append(name, detail);
      row.append(text);
      if (item.removable) {
        const removeButton = document.createElement("button");
        removeButton.className = "button button-secondary";
        removeButton.type = "button";
        removeButton.textContent = "Remove";
        removeButton.addEventListener("click", () => void (async () => {
          removeButton.disabled = true;
          try {
            snapshot = await removeRuntime(item.id);
            setFeedback(`Removed runtime ${item.version} from the catalog. Its files are retained for now.`, "success");
            renderSelection();
            renderProfiles();
            renderRuntimes();
            renderHomes();
          } catch (error) {
            setFeedback(managerErrorMessage(error, "The runtime could not be removed while it is selected or in use."), "error");
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
      empty.textContent = "No DSH homes are registered.";
      homeList.append(empty);
      return;
    }
    for (const item of homes) {
      const selected = item.id === snapshot?.desired?.profile.homeId;
      const row = document.createElement("div");
      row.className = `manager-list-item${selected ? " is-selected" : ""}`;
      const text = document.createElement("div");
      const name = document.createElement("strong");
      name.textContent = `${item.name}${selected ? " · selected" : ""}`;
      const detail = document.createElement("span");
      detail.textContent = `${item.ownership} · ${item.path}`;
      text.append(name, detail);
      row.append(text);
      if (item.ownership === HomeOwnership.HomeOwnershipUser) {
        const removeButton = document.createElement("button");
        removeButton.className = "button button-secondary";
        removeButton.type = "button";
        removeButton.textContent = "Unregister";
        removeButton.addEventListener("click", () => void (async () => {
          removeButton.disabled = true;
          try {
            snapshot = await removeHome(item.id);
            setFeedback(`Unregistered ${item.name}. Home files were not changed.`, "success");
            renderSelection();
            renderProfiles();
            renderRuntimes();
            renderHomes();
          } catch (error) {
            setFeedback(managerErrorMessage(error, "The DSH home could not be unregistered while it is selected or in use."), "error");
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
    refreshButton.disabled = true;
    try {
      snapshot = await getSnapshot();
      renderSelection();
      renderProfiles();
      renderRuntimes();
      renderHomes();
      showSection(currentSection);
      setFeedback("Catalog refreshed.");
    } catch (error) {
      setFeedback(managerErrorMessage(error, "The manager could not load its catalog."), "error");
      console.error("Could not read DSH manager snapshot", error);
    } finally {
      refreshButton.disabled = false;
    }
  }

  for (const item of navItems) {
    item.addEventListener("click", () => showSection(sectionName(item.dataset.managerSection ?? null)));
  }
  showSection(currentSection);

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
  refreshButton.addEventListener("click", () => void refresh());

  save.addEventListener("click", () => void (async () => {
    if (!runtime.value || !home.value || !profile.value) {
      setFeedback("Choose a runtime, DSH home and profile before saving.", "error");
      return;
    }
    save.disabled = true;
    try {
      snapshot = await setDesiredSelection({
        runtimeId: runtime.value,
        profile: {homeId: home.value, name: profile.value},
        workspace: workspace.value
      });
      setFeedback(snapshot.active ? "Saved. Restart DSH to apply this selection." : "Saved. Work will use this selection on the next start.", "success");
      renderSelection();
      renderProfiles();
      renderRuntimes();
      renderHomes();
    } catch (error) {
      setFeedback(managerErrorMessage(error, "The selection was rejected. Choose an existing runtime and profile."), "error");
      console.error("Could not save DSH launch selection", error);
    } finally {
      save.disabled = false;
    }
  })());

  function explicitPluginTarget() {
    if (!runtime.value || !home.value || !profile.value) {
      throw new Error("an explicit runtime, home and profile are required");
    }
    return {runtimeId: runtime.value, profile: {homeId: home.value, name: profile.value}};
  }

  async function mutatePlugin(operation: "install" | "remove") {
    const packageSpec = packageInput.value.trim();
    if (!packageSpec) {
      setFeedback("Enter a package name before changing plugins.", "error");
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
        ? "Plugin changed in the active profile. Restart DSH to apply it."
        : `Plugin ${operation === "install" ? "installed" : "removed"} in ${result.profile.name}.`, "success");
    } catch (error) {
      setFeedback(managerErrorMessage(error, "The profile plugin operation was rejected by DSH."), "error");
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
      setFeedback("Enter a home id, display name and existing DSH home path.", "error");
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
      setFeedback(`Registered DSH home ${newHomeId.value.trim()}.`, "success");
      newHomePath.value = "";
      newHomeId.value = "";
      newHomeName.value = "";
      renderSelection();
      renderProfiles();
      renderRuntimes();
      renderHomes();
    } catch (error) {
      setFeedback(managerErrorMessage(error, "The DSH home could not be registered."), "error");
      console.error("Could not register DSH home", error);
    } finally {
      registerHomeButton.disabled = false;
    }
  })());

  createProfile.addEventListener("click", () => void (async () => {
    const name = newProfile.value.trim();
    const packageSpec = newProfilePackage.value.trim();
    if (!runtime.value || !home.value || !name || !packageSpec) {
      setFeedback("Choose a runtime and home, then enter a profile name and initial plugin package.", "error");
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
        ? "Profile created in the active selection. Restart DSH to apply it."
        : `Profile ${name} created through DSH.`, "success");
      newProfile.value = "";
      newProfilePackage.value = "";
    } catch (error) {
      setFeedback(managerErrorMessage(error, "DSH could not create the profile with that plugin package."), "error");
      console.error("Could not create DSH profile", error);
    } finally {
      createProfile.disabled = false;
    }
  })());

  installRuntimeButton.addEventListener("click", () => void (async () => {
    const version = runtimeVersion.value.trim();
    if (!version) {
      setFeedback("Enter a DSH version before installing a runtime.", "error");
      return;
    }
    installRuntimeButton.disabled = true;
    try {
      snapshot = await installRuntime(version);
      setFeedback(`Installed DSH ${version}. Select it for the next Worker generation.`, "success");
      runtimeVersion.value = "";
      renderSelection();
      renderProfiles();
      renderRuntimes();
      renderHomes();
    } catch (error) {
      setFeedback(managerErrorMessage(error, "The DSH runtime installation failed or is unavailable on this platform."), "error");
      console.error("Could not install DSH runtime", error);
    } finally {
      installRuntimeButton.disabled = false;
    }
  })());

  void refresh();
}
