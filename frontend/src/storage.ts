import {StorageService} from "../bindings/github.com/local/dsh-work/internal/desktopclient";
import type {State} from "../bindings/github.com/local/dsh-work/internal/storagepaths/models";
import {mountFolderPicker} from "./ui/folder-picker";
import {subscribeLocale, t} from "./i18n";

export function mountStorage() {
  const useDefault = document.getElementById("storage-default") as HTMLInputElement;
  const save = document.getElementById("storage-save") as HTMLButtonElement;
  const cancel = document.getElementById("storage-cancel") as HTMLButtonElement;
  const status = document.getElementById("storage-status")!;
  let state: State | undefined;
  let root = "";
  let user = "";
  let revision = 0;
  let busy = false;
  let dirty = false;
  const report = (error: unknown) => { status.textContent = error instanceof Error ? error.message : t("view.unavailable"); };
  const picker = (kind: "root" | "user", changed: (path: string) => void) => mountFolderPicker(
    document.getElementById(`storage-${kind}`)!, {
      labelID: `storage-${kind}-label`, choose: StorageService.ChooseDirectory,
      changed, open: () => StorageService.OpenLocation(kind === "user"), report,
    });
  const rootPicker = picker("root", path => { root = path; void sync(); });
  const userPicker = picker("user", path => { user = path; void sync(); });

  function controls() {
    rootPicker.setDisabled(busy || !state);
    userPicker.setDisabled(busy || !state || useDefault.checked);
    useDefault.disabled = busy || !state;
    save.hidden = !dirty;
    save.disabled = busy || !dirty || !root || (!useDefault.checked && !user);
    cancel.hidden = !dirty && !state?.pending;
    cancel.disabled = busy;
    cancel.textContent = t(dirty ? "common.cancel" : "storage.cancel");
  }

  async function sync() {
    const token = ++revision;
    if (!state) return;
    const current = state.current;
    const saved = state.pending ?? current;
    const nextUser = useDefault.checked ? "" : user;
    dirty = root !== saved.root || nextUser !== saved.userData;
    controls();
    status.textContent = state.error ? `${t("storage.failed")} ${state.error}` : state.pending && !dirty ? t("storage.pending") : "";
    rootPicker.setPath(current.root);
    const rootTarget = document.getElementById("storage-root-target")!;
    const userTarget = document.getElementById("storage-user-target")!;
    rootTarget.hidden = root === current.root;
    rootTarget.textContent = t("storage.moveTo", {path: root});
    userTarget.hidden = true;
    try {
      const [currentUser, targetUser] = await Promise.all([
        current.userData || StorageService.DefaultUserDataPath(current.root),
        nextUser || StorageService.DefaultUserDataPath(root),
      ]);
      if (token !== revision) return;
      userPicker.setPath(currentUser);
      userTarget.hidden = currentUser === targetUser;
      userTarget.textContent = t("storage.moveTo", {path: targetUser});
    } catch (error) { if (token === revision) report(error); }
  }

  function render(next: State) {
    state = next;
    const choice = next.pending ?? next.current;
    root = choice.root; user = choice.userData;
    useDefault.checked = !user;
    void sync();
  }
  function restoreActionFocus(source: HTMLElement | null) {
    if (source !== save && source !== cancel) return;
    const target = !source.hidden ? source : !cancel.hidden ? cancel : document.querySelector<HTMLButtonElement>("#storage-root button");
    target?.focus({preventScroll: true});
  }
  async function update(work: () => Promise<State>) {
    if (busy) return;
    const source = document.activeElement as HTMLElement | null;
    busy = true; controls();
    try { render(await work()); }
    catch (error) { report(error); }
    finally { busy = false; controls(); restoreActionFocus(source); }
  }
  useDefault.addEventListener("change", () => void sync());
  save.addEventListener("click", () => void update(() => StorageService.SaveLocations({root, userData: useDefault.checked ? "" : user})));
  cancel.addEventListener("click", () => {
    if (dirty && state) { render(state); restoreActionFocus(cancel); }
    else void update(() => StorageService.CancelMigration());
  });
  const retry = document.createElement("button");
  retry.type = "button"; retry.className = "button button-secondary"; retry.textContent = t("common.retry"); retry.hidden = true; status.after(retry);
  const refresh = async () => {
    retry.disabled = true;
    try { render(await StorageService.GetLocations()); retry.hidden = true; }
    catch { status.textContent = t("view.unavailable"); retry.hidden = false; }
    finally { retry.disabled = false; }
  };
  retry.addEventListener("click", () => void refresh());
  subscribeLocale(() => { retry.textContent = t("common.retry"); void sync(); });
  controls();
  void refresh();
}
