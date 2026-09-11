import {ManagerService} from "../bindings/github.com/local/dsh-work/internal/desktopclient";
import type {ProfileBackupResult, ProfileCloneResult, ProfileRef, Snapshot} from "../bindings/github.com/local/dsh-work/internal/dshmanager";
import {currentLocale, t} from "./i18n";

export function safeModeActive(snapshot?: Snapshot): boolean {
  const selected = snapshot?.current ?? snapshot?.configured;
  return !!snapshot?.safeMode && selected?.profile.dataDirectoryId === snapshot.safeMode.target.profile.dataDirectoryId;
}

export function mountRecovery(updated: (snapshot: Snapshot, profile?: ProfileRef) => void) {
  const list = document.getElementById("profile-backups")!;
  const importFeedback = document.getElementById("backup-import-feedback")!;
  const more = document.getElementById("backup-more") as HTMLButtonElement;
  let showAll = false;
  const history = document.getElementById("backup-history-dialog") as HTMLDialogElement;
  const historyOpen = document.getElementById("backup-history-open") as HTMLButtonElement;
  const summary = document.getElementById("backup-summary")!;
  historyOpen.addEventListener("click", () => history.showModal());
  document.getElementById("backup-history-close")!.addEventListener("click", () => history.close());
  const feedback = document.getElementById("backup-feedback")!;
  const importButton = document.getElementById("backup-import") as HTMLButtonElement;
  const openButton = document.getElementById("backup-open") as HTMLButtonElement;
  const refreshButton = document.getElementById("backup-refresh") as HTMLButtonElement;
  const safeButton = document.getElementById("manager-safe-mode") as HTMLButtonElement;
  const safeFeedback = document.getElementById("safe-feedback")!;
  const backupResult = document.getElementById("profile-backup-result")!;
  const backupMessage = document.getElementById("profile-backup-message")!;
  const backupOpen = document.getElementById("profile-backup-open") as HTMLButtonElement;
  let createdBackup: ProfileBackupResult | undefined;
  let selectedProfile: ProfileRef | undefined;
  let snapshot: Snapshot | undefined;
  let busy = false;
  let blocked = false;
  let request = 0;

  function controls() {
    historyOpen.disabled = !selectedProfile;
    importButton.disabled = busy || blocked || !(snapshot?.current ?? snapshot?.configured);
    for (const button of [openButton, refreshButton, ...Array.from(list.querySelectorAll<HTMLButtonElement>("button"))]) button.disabled = busy || blocked || !selectedProfile;
    safeButton.disabled = busy || blocked || !snapshot?.configured;
    backupOpen.disabled = busy || !createdBackup;
    safeButton.classList.toggle("button-primary", !!snapshot?.lastSwitchAttempt);
    safeButton.textContent = t(safeModeActive(snapshot) ? "safe.exit" : "safe.enter");
  }

  async function refresh() {
    const owner = selectedProfile && {...selectedProfile};
    const id = owner?.dataDirectoryId;
    const token = ++request;
    const focused = (document.activeElement as HTMLElement)?.dataset.backup;
    list.textContent = t("view.loading");
    summary.textContent = t("view.loading");
    more.hidden = true; document.getElementById("backup-count")!.textContent = "";
    feedback.textContent = "";
    if (!id || !owner) return;
    try {
      const backups = await ManagerService.ListProfileBackups(owner) ?? [];
      if (token !== request || owner.name !== selectedProfile?.name || id !== selectedProfile?.dataDirectoryId) return;
      list.replaceChildren();
      document.getElementById("backup-count")!.textContent = String(backups.length);
      const latest = [...backups].sort((a,b) => b.createdAt.localeCompare(a.createdAt))[0];
      summary.textContent = latest ? t("backup.summary", {count: backups.length, time: new Intl.DateTimeFormat(currentLocale(), {dateStyle: "medium", timeStyle: "short"}).format(new Date(latest.createdAt))}) : t("backup.empty");
      more.hidden = showAll || backups.length <= 5;
      if (!backups.length) {
        const empty = document.createElement("p");
        empty.className = "manager-note"; empty.textContent = t("backup.empty"); list.append(empty);
      }
      for (const backup of (showAll ? backups : backups.slice(0, 5))) {
        const row = document.createElement("div"); row.className = "backup-row";
        const label = document.createElement("span"); label.textContent = `${new Date(backup.createdAt).toLocaleString(currentLocale(), {year:"numeric", month:"2-digit", day:"2-digit", hour:"2-digit", minute:"2-digit", hour12:false})} · ${Math.ceil(backup.size / 1024)} KB`; label.title = backup.fileName;
        const restore = document.createElement("button"); restore.type = "button"; restore.className = "button button-secondary button-compact";
        restore.textContent = t("backup.restore");
        restore.dataset.backup = backup.fileName;
        restore.setAttribute("aria-label", `${t("backup.restore")} · ${backup.fileName}`);
        restore.addEventListener("click", () => void run(async () => restored(await ManagerService.RestoreProfileBackup({dataDirectoryId: id, profileName: owner.name, fileName: backup.fileName}))));
        const remove = document.createElement("button");
        remove.type = "button"; remove.className = "button button-danger button-compact";
        remove.textContent = t("action.delete");
        remove.setAttribute("aria-label", `${t("action.delete")} · ${backup.fileName}`);
        remove.addEventListener("click", () => {
          if (busy || blocked || !window.confirm(t("backup.confirmDelete", {file: backup.fileName}))) return;
          void run(async () => {
            await ManagerService.DeleteProfileBackup(owner, backup.fileName);
            if (createdBackup?.fileName === backup.fileName && createdBackup.profile.name === owner.name && createdBackup.profile.dataDirectoryId === id) {
              createdBackup = undefined; backupResult.hidden = true;
            }
            await refresh();
          }, false, "backup.deleteError").then(() => refreshButton.focus({preventScroll: true}));
        });
        const actions = document.createElement("div"); actions.className = "backup-row-actions";
        actions.append(restore, remove);
        row.append(label, actions); list.append(row);
      }
      if (focused) list.querySelector<HTMLButtonElement>(`[data-backup="${CSS.escape(focused)}"]`)?.focus({preventScroll: true});
    } catch { if (token === request) { list.textContent = ""; feedback.textContent = t("backup.listError"); summary.textContent = t("backup.listError"); } }
    controls();
  }

  function restored(result: ProfileCloneResult) {
    updated(result.snapshot, result.profile);
    document.getElementById("profile-result")!.textContent = t("backup.restored", {profile: result.profile.name});
  }

  async function run(work: () => Promise<void>, safe = false, errorKey = "backup.restoreError", importing = false) {
    if (busy || blocked) return;
    const message = safe ? safeFeedback : importing ? importFeedback : feedback;
    busy = true; message.textContent = ""; controls();
    try { await work(); }
    catch (error) { message.textContent = t(safe ? "safe.error" : errorKey); console.error(error); }
    finally {
      busy = false;
      try { updated(await ManagerService.GetSnapshot()); } catch { /* Retain last result. */ }
      controls();
    }
  }
  more.addEventListener("click", () => { showAll = true; void refresh(); });
  refreshButton.addEventListener("click", () => void refresh());
  importButton.addEventListener("click", () => void run(async () => {
    const result = await ManagerService.ImportProfileBackup();
    if (result) restored(result);
  }, false, "backup.restoreError", true));
  openButton.addEventListener("click", () => void run(async () => { if (selectedProfile) await ManagerService.OpenProfileBackups(selectedProfile); }, false, "backup.openError"));
  backupOpen.addEventListener("click", () => void (async () => {
    if (!createdBackup || backupOpen.disabled) return;
    backupOpen.disabled = true;
    try { await ManagerService.OpenProfileBackups(createdBackup.profile); }
    catch { backupMessage.textContent = t("backup.openError"); }
    finally { controls(); }
  })());
  safeButton.addEventListener("click", () => void run(async () => {
    updated(await (safeModeActive(snapshot) ? ManagerService.ExitSafeMode() : ManagerService.EnterSafeMode()));
  }, true));
  controls();
  return {
    refresh,
    selectProfile(profile?: ProfileRef) {
      const changed = selectedProfile?.name !== profile?.name || selectedProfile?.dataDirectoryId !== profile?.dataDirectoryId;
      selectedProfile = profile && {...profile};
      if (changed) { if (history.open) history.close(); feedback.textContent = ""; showAll = false; void refresh(); }
      controls();
      backupResult.hidden = !createdBackup || createdBackup.profile.name !== profile?.name || createdBackup.profile.dataDirectoryId !== profile?.dataDirectoryId;
    },
    backupCreated(result: ProfileBackupResult) {
      createdBackup = result;
      backupMessage.textContent = t("feedback.profileBackedUp", {file: result.fileName});
      backupResult.hidden = result.profile.name !== selectedProfile?.name || result.profile.dataDirectoryId !== selectedProfile?.dataDirectoryId;

      void refresh();
      controls();
    },
    render(next: Snapshot | undefined, disabled: boolean) {
      snapshot = next; blocked = disabled;
      controls();
    }
  };
}
