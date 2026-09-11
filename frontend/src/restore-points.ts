import {transientStatus} from "./ui/transient-status";
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
  const history = document.createElement("dialog"); history.className = "version-history-dialog";
  const historyHeader = document.createElement("header");
  const historyTitle = document.createElement("h2"); historyTitle.id = `${container.id}-history-title`;
  history.setAttribute("aria-labelledby", historyTitle.id);
  const historyClose = document.createElement("button"); historyClose.type = "button"; historyClose.className = "button button-secondary";
  const browse = document.createElement("button"); browse.type = "button"; browse.className = "button button-secondary";
  const inspector = document.createElement("section"); inspector.className = "version-inspector"; inspector.hidden = true;
  historyHeader.append(historyTitle, historyClose); history.append(historyHeader);
  const preview = document.createElement("section"); preview.className = "restore-point-preview"; preview.hidden = true;
  const previewTitle = document.createElement("h3"); const previewBody = document.createElement("pre");
  const confirm = document.createElement("button"); confirm.type = "button"; confirm.className = "button button-primary";
  const close = document.createElement("button"); close.type = "button"; close.className = "button button-secondary";
  const previewActions = document.createElement("div"); previewActions.className = "manager-actions"; previewActions.append(confirm, close);
  let previewRequest = 0;
  function closePreview() {
    previewRequest++;
    preview.hidden = true;
    if (startup) list.hidden = summary.hidden = actions.hidden = false;
    else { inspector.hidden = !selected; list.hidden = !!selected; }
  }
  preview.append(previewTitle, previewBody, previewActions);
  close.onclick = () => {
    closePreview();
    if (!startup) inspector.querySelector<HTMLButtonElement>("[data-restore]")?.focus();
    if (startup) Array.from(list.querySelectorAll<HTMLButtonElement>("button[data-point-id]")).find(button => button.dataset.pointId === selected?.id)?.focus();
  };
  const edit = document.createElement("dialog"); edit.className = "restore-point-preview";
  const form = document.createElement("form"); const editTitle = document.createElement("h3");
  editTitle.id = `${container.id}-edit-title`; edit.setAttribute("aria-labelledby", editTitle.id);
  const input = document.createElement("input"); input.type = "text"; input.maxLength = 120;
  const editActions = document.createElement("div"); editActions.className = "manager-actions";
  const submit = document.createElement("button"); submit.type = "submit"; submit.className = "button button-primary";
  const dismiss = document.createElement("button"); dismiss.type = "button"; dismiss.className = "button button-secondary";
  dismiss.onclick = () => edit.close(); editActions.append(submit, dismiss); form.append(editTitle, input, editActions); edit.append(form);
  let editAction: ((value: string) => Promise<Snapshot>) | undefined;
  form.onsubmit = event => {event.preventDefault(); const action = editAction; const value = input.value; edit.close(); if (action) void run(() => action(value));};
  if (startup) container.append(heading, summary, actions, feedback, list, preview, edit);
  else {
    container.classList.add("version-overview");
    const header = document.createElement("div"); header.className = "version-overview-header"; header.append(heading, actions);
    container.append(header, summary, feedback, history, edit);
    history.append(list, inspector, preview);
    actions.append(browse);
  }
  actions.append(save, cancel);
  browse.onclick = () => {selected = undefined; inspector.hidden = preview.hidden = true; list.hidden = false; history.append(cancel, feedback); history.showModal();};
  historyClose.onclick = () => history.close();
  history.addEventListener("close", () => {previewRequest++; container.append(feedback); actions.append(cancel);});
  let reopenHistory = false;
  edit.addEventListener("close", () => {if (reopenHistory) {reopenHistory = false; history.append(cancel, feedback); history.showModal();}});
  let snapshot: Snapshot | undefined; let blocked = false; let busy = false; let selected: RestorePoint | undefined;
  let signature = "";
  let terminalOperation = "";

  function editPoint(title: string, value: string, label: string, action: (value: string) => Promise<Snapshot>, deleting = false) {
    if (busy || blocked) return;
    editAction = action; editTitle.textContent = title; input.value = value; input.hidden = deleting;
    input.setAttribute("aria-label", t("points.name")); submit.classList.toggle("button-danger", deleting); submit.textContent = t(label); dismiss.textContent = t("common.cancel");
    if (!startup && history.open) { reopenHistory = true; history.close(); }
    edit.showModal(); (deleting ? dismiss : input).focus();
  }

  function date(value: string) { return new Intl.DateTimeFormat(currentLocale(), {dateStyle: "medium", timeStyle: "short"}).format(new Date(value)); }
  function name(p: RestorePoint) { return p.label || date(p.createdAt); }
  function controls() {
    const operation = snapshot?.restorePoints?.operation;
    const recovering = operation?.status === "running";
    heading.hidden = startup;
    historyTitle.textContent = t("points.title"); historyClose.textContent = t("startup.close");
    browse.textContent = t("points.browse", {count: snapshot?.restorePoints?.points?.length ?? 0});
    browse.disabled = !snapshot?.restorePoints?.points?.length;
    for (const button of Array.from(inspector.querySelectorAll<HTMLButtonElement>("button[data-mutation]"))) button.disabled = busy || blocked || recovering || button.dataset.unavailable === "true";
    heading.textContent = t("points.title"); save.textContent = t("points.save"); cancel.textContent = t("common.cancel");
    save.hidden = startup; save.disabled = busy || blocked || recovering || !snapshot?.restorePoints?.canSave;
    confirm.disabled = busy || blocked || recovering || !!selected?.unavailable;
    cancel.hidden = !busy; confirm.textContent = t("points.restore"); close.textContent = t("common.cancel");
    for (const button of Array.from(list.querySelectorAll<HTMLButtonElement>("button"))) button.disabled = busy || blocked || recovering || button.dataset.unavailable === "true";
    if (operation && operation.status === "running") {
      feedback.textContent = t(`points.stage.${operation.stage}`); cancel.hidden = false;
    } else {
      const terminal = JSON.stringify([operation, currentLocale()]);
      if (terminal !== terminalOperation) {
        terminalOperation = terminal;
        if (operation?.status === "failed") feedback.textContent = operation.error || t("points.saveFailed");
        else if (operation?.status === "completed") feedback.textContent = busy ? t("points.restored") : "";
      }
    }
  }
  function render() {
    const view = snapshot?.restorePoints;
    summary.textContent = view?.saveError ? `${t("points.saveFailed")} ${view.saveError}` : t("points.empty");
    const target = snapshot?.current ?? snapshot?.configured;
    const lastID = target ? view?.lastByProfile?.[`${target.profile.dataDirectoryId}/${target.profile.name}`] : undefined;
    const last = view?.points?.find(p => p.id === lastID);
    if (last && !view?.saveError) summary.textContent = startup
      ? `${t("points.last")} · ${date(last.lastVerifiedAt)} · DSH ${last.dshVersion}`
      : `DSH ${last.dshVersion} · ${t("points.verifiedAt", {time: date(last.lastVerifiedAt)})}`;
    const nextSignature = JSON.stringify([view?.points, lastID, currentLocale(), startup]);
    if (signature !== nextSignature) {
      signature = nextSignature; list.replaceChildren();
      for (const p of [...(view?.points ?? [])].sort((a, b) => b.createdAt.localeCompare(a.createdAt))) {
        const row = document.createElement("div"); row.className = "backup-row";
        const text = document.createElement("div");
        const title = document.createElement("strong"); title.textContent = startup ? name(p) : p.label || `DSH ${p.dshVersion}`;
        const badge = document.createElement("span"); badge.className = "version-kind"; badge.textContent = t(p.kind === "manual" ? "points.manual" : "points.auto");
        title.append(" ", badge);
        const detail = document.createElement("p"); detail.className = "manager-note";
        detail.textContent = startup ? `${p.target.profile.name} · DSH ${p.dshVersion} · ${t("points.plugins", {count: (p.plugins ?? []).length})}${p.unavailable ? ` · ${p.unavailable}` : ""}`
          : `${date(p.createdAt)} · ${p.target.profile.name}${p.label ? ` · DSH ${p.dshVersion}` : ""}`;
        text.append(title, detail);
        const buttons = document.createElement("div"); buttons.className = "backup-row-actions";
        const button = document.createElement("button"); button.type = "button"; button.className = "button button-secondary button-compact";
        button.textContent = t(startup ? "points.restore" : "points.details");
        button.dataset.pointId = p.id;
        button.dataset.unavailable = String(startup && !!p.unavailable);
        button.setAttribute("aria-label", `${t(startup ? "points.restore" : "points.details")} ${name(p)}`);
        button.onclick = () => startup ? void showPreview(p) : showDetails(p);
        buttons.append(button);
        row.append(text, buttons); list.append(row);
      }
    }
    controls();
  }
  function showDetails(point: RestorePoint) {
    previewRequest++; selected = point; inspector.replaceChildren(); list.hidden = true; preview.hidden = true; inspector.hidden = false;
    const back = document.createElement("button"); back.type = "button"; back.className = "button button-secondary button-compact"; back.textContent = t("points.back");
    back.onclick = () => { inspector.hidden = true; list.hidden = false; Array.from(list.querySelectorAll<HTMLButtonElement>("button")).find(b => b.dataset.pointId === point.id)?.focus(); selected = undefined; };
    const title = document.createElement("h3"); title.textContent = name(point);
    const metadata = document.createElement("dl"); metadata.className = "version-metadata";
    for (const [label, value] of [["DSH", point.dshVersion], [t("points.profile"), point.target.profile.name], ["Node.js", point.nodeVersion], [t("points.packageManager"), point.packageManager], [t("points.lockfile"), point.lockfile || "—"], [t("points.created"), date(point.createdAt)]]) {
      const dt = document.createElement("dt"); dt.textContent = label; const dd = document.createElement("dd"); dd.textContent = value; metadata.append(dt, dd);
    }
    const plugins = document.createElement("div"); plugins.className = "version-plugin-list";
    const pluginHeading = document.createElement("h4"); pluginHeading.textContent = t("points.plugins", {count: (point.plugins ?? []).length}); plugins.append(pluginHeading);
    for (const plugin of point.plugins ?? []) { const row = document.createElement("div"); const label = document.createElement("span"); label.textContent = plugin.name; const version = document.createElement("code"); version.textContent = plugin.version; row.append(label, version); plugins.append(row); }
    const tools = document.createElement("div"); tools.className = "version-detail-actions";
    const add = (key: string, action: () => void, unavailable = false) => { const b = document.createElement("button"); b.type = "button"; b.className = key === "action.delete" ? "button button-secondary button-danger" : "button button-secondary"; b.textContent = t(key); b.dataset.mutation = "true"; b.dataset.unavailable = String(unavailable); b.onclick = action; tools.append(b); return b; };
    const restore = add("points.restore", () => void showPreview(point), !!point.unavailable); restore.dataset.restore = "true"; restore.className = "button button-primary";
    if (point.kind === "manual") {
      add("points.rename", () => editPoint(t("points.name"), name(point), "points.rename", label => ManagerService.RenameRestorePoint(point.id, label)));
      const view = snapshot?.restorePoints;
      add("action.delete", () => editPoint(t("points.delete", {name: name(point)}), "", "action.delete", () => ManagerService.DeleteRestorePoint(point.id), true), point.id === view?.lastRunning || Object.values(view?.lastByProfile ?? {}).includes(point.id) || (view?.operation?.pointId === point.id && view.operation.status !== "completed"));
    }
    inspector.append(back, title, metadata, plugins);
    if (point.unavailable) {const reason = document.createElement("p"); reason.className = "status-detail"; reason.textContent = point.unavailable; inspector.append(reason);}
    inspector.append(tools); controls(); back.focus();
  }
  async function showPreview(point: RestorePoint) {
    if (busy || blocked) return;
    const request = ++previewRequest;
    try {
      const result = await ManagerService.PreviewRestorePoint(point.id);
      if (request !== previewRequest || !container.getClientRects().length || (!startup && !history.open)) return;
      selected = result;
      previewTitle.textContent = t("points.restoreTitle", {name: name(point)});
      const current = snapshot?.current ?? snapshot?.configured;
      const currentRuntime = snapshot?.runtimes?.find(r => r.id === current?.runtimeId);
      const currentPlugins = snapshot?.profiles?.find(p => p.ref.dataDirectoryId === current?.profile.dataDirectoryId && p.ref.name === current.profile.name)?.plugins ?? [];
      const changes = new Map(currentPlugins.map(p => [p.package, `${p.version || "—"} → ${t("points.removed")}`]));
      for (const plugin of point.plugins ?? []) changes.set(plugin.name, `${currentPlugins.find(p => p.package === plugin.name)?.version ?? "—"} → ${plugin.version}`);
      previewBody.textContent = `${t("points.restoreDescription")}\n\nDSH ${currentRuntime?.version ?? "—"} → ${point.dshVersion}\nProfile: ${point.target.profile.name}\n${Array.from(changes, ([name, value]) => `${name}: ${value}`).join("\n")}`;
      if (startup) { list.hidden = summary.hidden = actions.hidden = true; preview.hidden = false; }
      else { list.hidden = inspector.hidden = true; preview.hidden = false; }
      confirm.focus();
    } catch (error) {if (request === previewRequest) feedback.textContent = String(error);}
  }
  async function run(action: () => Promise<Snapshot>, restore = false) {
    if (busy || blocked) return;
    busy = true; feedback.textContent = t("view.loading"); controls();
    let polling = false; let acceptingPolls = true;
    const timer = restore ? window.setInterval(async () => {
      if (polling) return; polling = true;
      try { const next = await ManagerService.GetSnapshot(); if (acceptingPolls) {snapshot = next; render();} } catch {} finally {polling = false;}
    }, 700) : undefined;
    try {snapshot = await action(); acceptingPolls = false; transientStatus(feedback, t(restore ? "points.restored" : "points.saved")); updated(snapshot);}
    catch (error) {acceptingPolls = false; feedback.textContent = String(error); try {snapshot = await ManagerService.GetSnapshot(); updated(snapshot);} catch {}}
    finally {
      acceptingPolls = false; if (timer !== undefined) clearInterval(timer); busy = false; render();
      if (!startup && selected) { const current = snapshot?.restorePoints?.points?.find(p => p.id === selected?.id); if (current) showDetails(current); else {selected = undefined; inspector.hidden = true; list.hidden = false;} }
    }
  }
  save.onclick = () => editPoint(t("points.name"), new Date().toLocaleString(currentLocale()), "points.save", label => ManagerService.SaveRestorePoint(label));
  confirm.onclick = () => {const id = selected?.id; closePreview(); if (id) void run(() => ManagerService.RestorePoint(id), true);};
  cancel.onclick = () => void ManagerService.CancelRestore().catch(error => {feedback.textContent = String(error);});
  subscribeLocale(() => render());
  return {closePreview, render(next: Snapshot | undefined, disabled: boolean) {snapshot = next; blocked = disabled; render();}};
}
