import {ManagerService} from "../bindings/github.com/local/dsh-work/internal/app";
import type {Snapshot, RestorePoint} from "../bindings/github.com/local/dsh-work/internal/dshmanager";
import {currentLocale, subscribeLocale, t} from "./i18n";

export function mountRestorePoints(container: HTMLElement, updated: (snapshot: Snapshot) => void, startup = false) {
  const heading = document.createElement("h3");
  const summary = document.createElement("p"); summary.className = "manager-note";
  const actions = document.createElement("div"); actions.className = "manager-actions";
  const save = document.createElement("button"); save.type = "button"; save.className = "button button-secondary";
  const cancel = document.createElement("button"); cancel.type = "button"; cancel.className = "button button-secondary"; cancel.hidden = true;
  const list = document.createElement("div"); list.className = "restore-point-list";
  const feedback = document.createElement("p"); feedback.className = "status-detail"; feedback.setAttribute("role", "status");
  const preview = document.createElement("dialog"); preview.className = "restore-point-preview";
  const previewTitle = document.createElement("h3"); const previewBody = document.createElement("pre");
  const confirm = document.createElement("button"); confirm.type = "button"; confirm.className = "button button-primary";
  const close = document.createElement("button"); close.type = "button"; close.className = "button button-secondary";
  const previewActions = document.createElement("div"); previewActions.className = "manager-actions"; previewActions.append(confirm, close);
  preview.append(previewTitle, previewBody, previewActions); close.onclick = () => preview.close();
  const edit = document.createElement("dialog"); edit.className = "restore-point-preview";
  const form = document.createElement("form"); const editTitle = document.createElement("h3");
  const input = document.createElement("input"); input.type = "text"; input.maxLength = 120;
  const editActions = document.createElement("div"); editActions.className = "manager-actions";
  const submit = document.createElement("button"); submit.type = "submit"; submit.className = "button button-primary";
  const dismiss = document.createElement("button"); dismiss.type = "button"; dismiss.className = "button button-secondary";
  dismiss.onclick = () => edit.close(); editActions.append(submit, dismiss); form.append(editTitle, input, editActions); edit.append(form);
  let editAction: ((value: string) => Promise<Snapshot>) | undefined;
  form.onsubmit = event => {event.preventDefault(); const action = editAction; const value = input.value; edit.close(); if (action) void run(() => action(value));};
  container.append(heading, summary, actions, feedback, list, preview, edit); actions.append(save, cancel);
  let snapshot: Snapshot | undefined; let blocked = false; let busy = false; let selected: RestorePoint | undefined;
  let signature = "";

  function editPoint(title: string, value: string, label: string, action: (value: string) => Promise<Snapshot>, deleting = false) {
    if (busy || blocked) return;
    editAction = action; editTitle.textContent = title; input.value = value; input.hidden = deleting;
    input.setAttribute("aria-label", t("points.name")); submit.textContent = t(label); dismiss.textContent = t("common.cancel");
    edit.showModal(); (deleting ? dismiss : input).focus();
  }

  function name(p: RestorePoint) { return p.label || new Date(p.createdAt).toLocaleString(currentLocale()); }
  function controls() {
    const operation = snapshot?.restorePoints?.operation;
    const recovering = operation?.status === "running";
    heading.textContent = t("points.title"); save.textContent = t("points.save"); cancel.textContent = t("common.cancel");
    save.hidden = startup; save.disabled = busy || blocked || recovering || !snapshot?.restorePoints?.canSave;
    cancel.hidden = !busy; confirm.textContent = t("points.restore"); close.textContent = t("common.cancel");
    for (const button of Array.from(list.querySelectorAll<HTMLButtonElement>("button"))) button.disabled = busy || blocked || recovering || button.dataset.unavailable === "true";
    if (operation && operation.status === "running") {
      feedback.textContent = t(`points.stage.${operation.stage}`); cancel.hidden = false;
    }
  }
  function render() {
    const view = snapshot?.restorePoints;
    summary.textContent = view?.saveError ? `${t("points.saveFailed")} ${view.saveError}` : t("points.empty");
    const target = snapshot?.current ?? snapshot?.configured;
    const lastID = target ? view?.lastByProfile?.[`${target.profile.dataDirectoryId}/${target.profile.name}`] : undefined;
    const last = view?.points?.find(p => p.id === lastID);
    if (last && !view?.saveError) summary.textContent = `${t("points.last")} · ${new Date(last.lastVerifiedAt).toLocaleString(currentLocale())} · DSH ${last.dshVersion}`;
    const nextSignature = JSON.stringify([view?.points, lastID, currentLocale(), startup]);
    if (signature !== nextSignature) {
      signature = nextSignature; list.replaceChildren();
      for (const p of view?.points ?? []) {
        const row = document.createElement("div"); row.className = "backup-row";
        const text = document.createElement("div");
        const title = document.createElement("strong"); title.textContent = `${name(p)} · ${t(p.kind === "manual" ? "points.manual" : "points.auto")}`;
        const detail = document.createElement("p"); detail.className = "manager-note";
        detail.textContent = `${p.target.profile.name} · DSH ${p.dshVersion} · ${t("points.plugins", {count: (p.plugins ?? []).length})}${p.unavailable ? ` · ${p.unavailable}` : ""}`;
        const versions = document.createElement("details"); const versionsTitle = document.createElement("summary"); versionsTitle.textContent = t("points.versions");
        const body = document.createElement("pre"); body.textContent = `Node ${p.nodeVersion}\n${p.packageManager}\n${(p.plugins ?? []).map(plugin => `${plugin.name} ${plugin.version}`).join("\n")}`; versions.append(versionsTitle, body);
        text.append(title, detail, versions);
        const buttons = document.createElement("div"); buttons.className = "backup-row-actions";
        const button = (label: string, action: () => void) => {const b = document.createElement("button"); b.type = "button"; b.className = "button button-secondary button-compact"; b.textContent = t(label); b.onclick = action; buttons.append(b); return b;};
        const restore = button("points.restore", () => void showPreview(p)); restore.dataset.unavailable = String(!!p.unavailable);
        restore.setAttribute("aria-label", `${t("points.restore")} ${name(p)}`);
        if (p.kind === "manual" && !startup) {
          button("points.rename", () => editPoint(t("points.name"), name(p), "points.rename", label => ManagerService.RenameRestorePoint(p.id, label)));
          const remove = button("action.delete", () => editPoint(t("points.delete", {name: name(p)}), "", "action.delete", () => ManagerService.DeleteRestorePoint(p.id), true));
          remove.dataset.unavailable = String(p.id === view?.lastRunning || Object.values(view?.lastByProfile ?? {}).includes(p.id) || (view?.operation?.pointId === p.id && view.operation.status !== "completed"));
        }
        row.append(text, buttons); list.append(row);
      }
    }
    controls();
  }
  async function showPreview(point: RestorePoint) {
    if (busy || blocked) return;
    try {
      selected = await ManagerService.PreviewRestorePoint(point.id);
      previewTitle.textContent = t("points.restoreTitle", {name: name(point)});
      const current = snapshot?.current ?? snapshot?.configured;
      const currentRuntime = snapshot?.runtimes?.find(r => r.id === current?.runtimeId);
      const currentPlugins = snapshot?.profiles?.find(p => p.ref.dataDirectoryId === current?.profile.dataDirectoryId && p.ref.name === current.profile.name)?.plugins ?? [];
      const changes = new Map(currentPlugins.map(p => [p.package, `${p.version || "—"} → ${t("points.removed")}`]));
      for (const plugin of point.plugins ?? []) changes.set(plugin.name, `${currentPlugins.find(p => p.package === plugin.name)?.version ?? "—"} → ${plugin.version}`);
      previewBody.textContent = `${t("points.restoreDescription")}\n\nDSH ${currentRuntime?.version ?? "—"} → ${point.dshVersion}\nProfile: ${point.target.profile.name}\n${Array.from(changes, ([name, value]) => `${name}: ${value}`).join("\n")}`;
      preview.showModal(); confirm.focus();
    } catch (error) {feedback.textContent = String(error);}
  }
  async function run(action: () => Promise<Snapshot>, restore = false) {
    if (busy || blocked) return;
    busy = true; feedback.textContent = t("view.loading"); controls();
    let polling = false; let acceptingPolls = true;
    const timer = restore ? window.setInterval(async () => {
      if (polling) return; polling = true;
      try { const next = await ManagerService.GetSnapshot(); if (acceptingPolls) {snapshot = next; render();} } catch {} finally {polling = false;}
    }, 700) : undefined;
    try {snapshot = await action(); acceptingPolls = false; feedback.textContent = t(restore ? "points.restored" : "points.saved"); updated(snapshot);}
    catch (error) {acceptingPolls = false; feedback.textContent = String(error); try {snapshot = await ManagerService.GetSnapshot(); updated(snapshot);} catch {}}
    finally {acceptingPolls = false; if (timer !== undefined) clearInterval(timer); busy = false; render();}
  }
  save.onclick = () => editPoint(t("points.name"), new Date().toLocaleString(currentLocale()), "points.save", label => ManagerService.SaveRestorePoint(label));
  confirm.onclick = () => {const id = selected?.id; preview.close(); if (id) void run(() => ManagerService.RestorePoint(id), true);};
  cancel.onclick = () => void ManagerService.CancelRestore().catch(error => {feedback.textContent = String(error);});
  subscribeLocale(() => render());
  return {render(next: Snapshot | undefined, disabled: boolean) {snapshot = next; blocked = disabled; render();}};
}
