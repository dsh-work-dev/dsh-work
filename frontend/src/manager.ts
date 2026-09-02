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
  const active = document.getElementById("manager-active") as HTMLSpanElement;
  const profileList = document.getElementById("manager-profiles") as HTMLDivElement;
  const runtimeList = document.getElementById("manager-runtimes") as HTMLDivElement;
  const section = new URLSearchParams(window.location.search).get("section");
  let snapshot: ManagerSnapshot | undefined;

  function selectedProfile(): ManagerProfile | undefined {
    if (!snapshot) {
      return undefined;
    }
    return (snapshot.profiles ?? []).find((item) => item.ref.homeId === home.value && item.ref.name === profile.value);
  }

  function fillProfiles(preferred?: ManagerProfileRef) {
    profile.replaceChildren();
    const profiles = (snapshot?.profiles ?? []).filter((item) => item.ref.homeId === home.value);
    for (const item of profiles) {
      const option = document.createElement("option");
      option.value = item.ref.name;
      option.textContent = item.exists ? item.ref.name : `${item.ref.name} (created on first use)`;
      option.disabled = !item.launchable;
      profile.append(option);
    }
    if (preferred && preferred.homeId === home.value && profiles.some((item) => item.ref.name === preferred.name)) {
      profile.value = preferred.name;
    }
    renderProfileSelection();
  }

  function renderProfileSelection() {
    const item = selectedProfile();
    if (!item) {
      return;
    }
    if (section === "plugins") {
      feedback.textContent = `Plugin management is scoped to ${item.ref.homeId}/${item.ref.name}. Choose an explicit profile before changing plugins.`;
    }
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
    fillProfiles(desired?.profile);
    if (!workspace.value) {
      workspace.value = "";
    }
    if (snapshot?.active) {
      active.textContent = `Active · ${snapshot.active.profile.name}`;
      active.className = "manager-state ready";
    } else {
      active.textContent = "Not running";
      active.className = "manager-state";
    }
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
      const row = document.createElement("div");
      row.className = "manager-list-item";
      const text = document.createElement("div");
      const name = document.createElement("strong");
      name.textContent = `${item.ref.homeId}/${item.ref.name}`;
      const detail = document.createElement("span");
      const pluginNames = (item.plugins ?? []).map((plugin) => plugin.name).join(", ");
      detail.textContent = `${item.kind} · ${item.pluginCount} plugin${item.pluginCount === 1 ? "" : "s"}${pluginNames ? ` · ${pluginNames}` : ""}${item.exists ? "" : " · initialized by DSH on first use"}`;
      text.append(name, detail);
      row.append(text);
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
      const row = document.createElement("div");
      row.className = "manager-list-item";
      const text = document.createElement("div");
      const name = document.createElement("strong");
      name.textContent = `${item.version} · ${item.source}`;
      const detail = document.createElement("span");
      detail.textContent = item.installed ? item.path : `${item.path} · verify before launch`;
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
            feedback.textContent = `Removed runtime ${item.version} from the catalog. Its files are retained for now.`;
            renderSelection();
            renderProfiles();
            renderRuntimes();
          } catch (error) {
            feedback.textContent = managerErrorMessage(error, "The runtime could not be removed while it is selected or in use.");
            console.error("Could not remove Work runtime", error);
          } finally {
            removeButton.disabled = false;
          }
        })());
        row.append(removeButton);
      }
      runtimeList.append(row);
    }
  }

  async function refresh() {
    try {
      snapshot = await getSnapshot();
      renderSelection();
      renderProfiles();
      renderRuntimes();
      if (!section || section !== "plugins") {
        feedback.textContent = "Selection changes apply to the next DSH Worker generation.";
      }
    } catch (error) {
      feedback.textContent = managerErrorMessage(error, "The manager could not load its catalog.");
      console.error("Could not read Work manager snapshot", error);
    }
  }

  home.addEventListener("change", () => fillProfiles());
  profile.addEventListener("change", renderProfileSelection);
  save.addEventListener("click", () => void (async () => {
    save.disabled = true;
    try {
      snapshot = await setDesiredSelection({
        runtimeId: runtime.value,
        profile: {homeId: home.value, name: profile.value},
        workspace: workspace.value
      });
      feedback.textContent = snapshot.active ? "Saved. Restart DSH to apply this selection." : "Saved. Work will use this selection on the next start.";
      renderSelection();
      renderProfiles();
      renderRuntimes();
    } catch (error) {
      feedback.textContent = managerErrorMessage(error, "The selection was rejected. Choose an existing runtime and profile.");
      console.error("Could not save Work launch selection", error);
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
      feedback.textContent = "Enter a package name before changing plugins.";
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
      feedback.textContent = result.restartRequired
        ? "Plugin changed in the active profile. Restart DSH to apply it."
        : `Plugin ${operation === "install" ? "installed" : "removed"} in ${result.profile.name}.`;
    } catch (error) {
      feedback.textContent = managerErrorMessage(error, "The profile plugin operation was rejected by DSH.");
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
      feedback.textContent = "Enter a home id, display name and existing DSH home path.";
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
      feedback.textContent = `Registered DSH home ${newHomeId.value.trim()}.`;
      renderSelection();
      renderProfiles();
      renderRuntimes();
    } catch (error) {
      feedback.textContent = managerErrorMessage(error, "The DSH home could not be registered.");
      console.error("Could not register DSH home", error);
    } finally {
      registerHomeButton.disabled = false;
    }
  })());

  createProfile.addEventListener("click", () => void (async () => {
    const name = newProfile.value.trim();
    const packageSpec = newProfilePackage.value.trim();
    if (!runtime.value || !home.value || !name || !packageSpec) {
      feedback.textContent = "Choose a runtime and home, then enter a profile name and initial plugin package.";
      return;
    }
    createProfile.disabled = true;
    try {
      const result = await installPlugin({
        target: {runtimeId: runtime.value, profile: {homeId: home.value, name}},
        package: packageSpec
      });
      await refresh();
      fillProfiles({homeId: home.value, name});
      feedback.textContent = result.restartRequired
        ? "Profile created in the active selection. Restart DSH to apply it."
        : `Profile ${name} created through DSH.`;
    } catch (error) {
      feedback.textContent = managerErrorMessage(error, "DSH could not create the profile with that plugin package.");
      console.error("Could not create DSH profile", error);
    } finally {
      createProfile.disabled = false;
    }
  })());

  installRuntimeButton.addEventListener("click", () => void (async () => {
    const version = runtimeVersion.value.trim();
    if (!version) {
      feedback.textContent = "Enter a DSH version before installing a runtime.";
      return;
    }
    installRuntimeButton.disabled = true;
    try {
      snapshot = await installRuntime(version);
      feedback.textContent = `Installed DSH ${version}. Select it for the next Worker generation.`;
      renderSelection();
      renderProfiles();
      renderRuntimes();
    } catch (error) {
      feedback.textContent = managerErrorMessage(error, "The DSH runtime installation failed or is unavailable on this platform.");
      console.error("Could not install DSH runtime", error);
    } finally {
      installRuntimeButton.disabled = false;
    }
  })());

  void refresh();
}
