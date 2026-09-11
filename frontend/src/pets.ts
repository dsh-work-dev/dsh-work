import {PetSettingsService} from "../bindings/github.com/local/dsh-work/internal/desktopclient";
import {subscribeLocale, t} from "./i18n";
import {beginControlUpdate} from "./ui/pending-control";

type PetPanel = Awaited<ReturnType<typeof PetSettingsService.GetPetPanel>>;
type PetPreview = Awaited<ReturnType<typeof PetSettingsService.PreviewPet>>;
type PetItem = NonNullable<PetPanel["snapshot"]["items"]>[number];

const minPetSizePercent = 50;
const maxPetSizePercent = 200;
const defaultPetSizePercent = 100;

// Decode one local video frame for the same static thumbnail UI used by atlases.
async function getPreviewImage(ref:string):Promise<string>{
 const url=await PetSettingsService.GetPetPreview(ref);
 if(!url.startsWith("/__pet_media/"))return url;
 return new Promise((resolve,reject)=>{
  const video=document.createElement("video");video.muted=true;video.preload="auto";
  const cleanup=()=>{clearTimeout(timer);video.onloadeddata=null;video.onerror=null;video.pause();video.removeAttribute("src");video.load();};
  const timer=setTimeout(()=>{cleanup();reject(new Error("Preview timed out"));},8000);
  video.onerror=()=>{cleanup();reject(new Error("Preview unavailable"));};
  video.onloadeddata=()=>{
   if(!video.videoWidth||!video.videoHeight||video.videoWidth>4096||video.videoHeight>4096){cleanup();reject(new Error("Invalid preview"));return;}
   const canvas=document.createElement("canvas"),scale=Math.min(1,384/video.videoWidth);
   canvas.width=video.videoWidth*scale;canvas.height=video.videoHeight*scale;
   canvas.getContext("2d")!.drawImage(video,0,0,canvas.width,canvas.height);
   const data=canvas.toDataURL("image/png");cleanup();resolve(data);
  };
  video.src=url;
 });
}

function errorMessage(_error: unknown, fallback: string): string {
  // Backend errors may contain implementation or environment details. The
  // Settings surface presents only the bounded product copy for each action.
  return fallback;
}

export function sortPetItems(items: readonly PetItem[]): PetItem[] {
  return [...items].sort((left, right) => {
    const name = left.displayName.localeCompare(right.displayName, undefined, {sensitivity: "base"});
    return name !== 0 ? name : left.stableSourceKey.localeCompare(right.stableSourceKey);
  });
}

export function filterPetItems(items: readonly PetItem[], query: string): PetItem[] {
  const needle = query.trim().toLocaleLowerCase();
  if (!needle) {
    return [...items];
  }
  return items.filter((item) => `${item.displayName}\n${item.description ?? ""}`.toLocaleLowerCase().includes(needle));
}

function sourceBadge(item: PetItem): string {
  if (item.sourceBadge === "dsh-native") {
    return t("pets.source.dsh");
  }
  return t("pets.source.codex");
}

function issueMessage(code: string): string {
  if (code.includes("catalog-limit")) {
    return t("pets.invalid.tooMany");
  }
  if (code.includes("manifest")) {
    return t("pets.invalid.manifest");
  }
  if (code.includes("spritesheet") || code.includes("image")) {
    return t("pets.invalid.spritesheetMissing");
  }
  if (code.includes("path") || code.includes("resource") || code.includes("symlink")) {
    return t("pets.invalid.path");
  }
  if (code.includes("large") || code.includes("pixels")) {
    return t("pets.invalid.tooLarge");
  }
  if (code.includes("conflict")) {
    return t("pets.invalid.conflict");
  }
  return t("pets.invalid.manifest");
}

export function mountPets() {
  let visibilityFailure = "";
  let sizeFailure = "";
  const visibility = document.getElementById("pets-visibility") as HTMLInputElement;
  const alwaysOnTop = document.getElementById("pets-always-on-top") as HTMLInputElement;
  const alwaysOnTopStatus = document.getElementById("pets-always-on-top-status")!;
  const visibilityStatus = document.getElementById("pets-visibility-status") as HTMLParagraphElement;
  const size = document.getElementById("pets-size") as HTMLInputElement;
  const sizeValue = document.getElementById("pets-size-value") as HTMLOutputElement;
  const sizeStatus = document.getElementById("pets-size-status") as HTMLParagraphElement;
  const refreshButton = document.getElementById("pets-refresh") as HTMLButtonElement;
  const search = document.getElementById("pets-search") as HTMLInputElement;
  const list = document.getElementById("pets-list") as HTMLDivElement;
  const listCount = document.getElementById("pets-list-count") as HTMLParagraphElement;
  const empty = document.getElementById("pets-empty") as HTMLDivElement;
  const emptyTitle = document.getElementById("pets-empty-title") as HTMLElement;
  const emptyDescription = document.getElementById("pets-empty-description") as HTMLSpanElement;
  const invalidIssues = document.getElementById("pets-invalid-issues") as HTMLDetailsElement;
  const invalidSummary = document.getElementById("pets-invalid-summary") as HTMLSpanElement;
  const invalidIssueList = document.getElementById("pets-invalid-issue-list") as HTMLDivElement;
  const previewPlaceholder = document.getElementById("pets-preview-placeholder") as HTMLDivElement;
  const previewImage = document.getElementById("pets-preview-image") as HTMLImageElement;
  const previewName = document.getElementById("pets-preview-name") as HTMLHeadingElement;
  const previewDescription = document.getElementById("pets-preview-description") as HTMLParagraphElement;
  const previewMessage = document.getElementById("pets-preview-message") as HTMLParagraphElement;
  const useButton = document.getElementById("pets-use") as HTMLButtonElement;
  const retryButton = document.getElementById("pets-retry") as HTMLButtonElement;
  const clearButton = document.getElementById("pets-clear") as HTMLButtonElement;
  let panel: PetPanel | undefined;
  let browseKey: string | undefined;
  let preview: PetPreview | undefined;
  let previewDataURL = "";
  let previewRequest = 0;
  let previewFailure = "";
  let previewLoading = false;
  const thumbnailCache = new Map<string, string | null>();
  const thumbnailRequests = new Map<string, number>();
  const maxThumbnailRequests = 4;
  let thumbnailEpoch = 0;
  let refreshing = false;
  let switching = false;
  let loading = true;
  let sizeTimer: number | undefined;
  let sizeRequest = 0;
  let pendingSize: {request: number; percent: number} | undefined;
  let sizeCommitInFlight = false;

  function selectedKey(): string | undefined {
    return panel?.preference.selectedKey ?? undefined;
  }

  function visibleItems(): PetItem[] {
    const sorted = sortPetItems(panel?.snapshot.items ?? []);
    return filterPetItems(sorted, search.value);
  }

  function renderVisibility() {
    visibility.closest<HTMLElement>(".setting-row")!.hidden = !panel;
    const preference = panel?.preference;
    const runtime = panel?.runtime;
    visibility.checked = preference?.visibilityIntent === "visible";
    alwaysOnTop.checked = preference?.alwaysOnTop ?? false;
    alwaysOnTop.disabled = !preference || switching || refreshing;
    visibility.disabled = !preference?.selectedKey || switching || refreshing;
    if (visibilityFailure) {
      visibilityStatus.textContent = visibilityFailure;
    } else if (!panel) {
      visibilityStatus.textContent = "";
    } else if (!preference?.selectedKey) {
      visibilityStatus.textContent = t("pets.noSelection.status");
    } else if (runtime?.effectiveVisibility === "paused") {
      visibilityStatus.textContent = t("pets.visibility.paused");
    } else {
      visibilityStatus.textContent = "";
    }
  }

  function petSizePercent(value: number | undefined): number {
    if (!Number.isFinite(value)) {
      return defaultPetSizePercent;
    }
    return Math.min(maxPetSizePercent, Math.max(minPetSizePercent, Math.round(value as number)));
  }

  function renderSize() {
    const percent = petSizePercent(panel?.sizePercent);
    size.value = String(percent);
    sizeValue.value = `${percent}%`;
    size.disabled = !selectedKey() || switching || refreshing || loading || panel?.sizeAvailable === false;
    if (sizeFailure) {
      sizeStatus.textContent = sizeFailure;
    } else if (!selectedKey()) {
      sizeStatus.textContent = "";
    } else if (panel?.sizeAvailable === false) {
      sizeStatus.textContent = t("pets.size.unavailable");
    } else {
      sizeStatus.textContent = "";
    }
  }

  function readSizePercent(): number | undefined {
    const value = Number(size.value);
    if (!Number.isFinite(value)) {
      return undefined;
    }
    return petSizePercent(value);
  }

  function cancelSizeCommit() {
    sizeRequest += 1;
    pendingSize = undefined;
    if (sizeTimer !== undefined) {
      window.clearTimeout(sizeTimer);
      sizeTimer = undefined;
    }
  }

  function hasCurrentPendingSize(): boolean {
    return pendingSize !== undefined && pendingSize.request === sizeRequest;
  }

  function updateSizeValue() {
    const percent = readSizePercent();
    if (percent !== undefined) {
      sizeValue.value = `${percent}%`;
    }
  }

  function renderIssues() {
    const issues = panel?.snapshot.issues ?? [];
    const visibleIssues = issues.filter((issue) => issue.severity !== "info");
    invalidIssues.hidden = visibleIssues.length === 0;
    invalidSummary.textContent = t("pets.invalid.summary", {count: visibleIssues.length});
    invalidIssueList.replaceChildren();
    for (const issue of visibleIssues) {
      const line = document.createElement("p");
      line.className = "manager-note";
      line.textContent = issueMessage(issue.code);
      invalidIssueList.append(line);
    }
  }

  function renderPreview() {
    document.querySelector<HTMLElement>(".pets-preview-panel")!.hidden = !panel;
    const item = (panel?.snapshot.items ?? []).find((candidate) => candidate.stableSourceKey === browseKey);
    previewName.textContent = item?.displayName ?? preview?.displayName ?? "";
    previewName.hidden = !previewName.textContent;
    previewDescription.textContent = item?.description ?? preview?.description ?? "";
    previewPlaceholder.hidden = !!previewDataURL;
    previewImage.hidden = !previewDataURL;
    if (previewDataURL) {
      previewImage.src = previewDataURL;
      previewImage.alt = preview?.displayName ?? "";
    } else {
      previewImage.removeAttribute("src");
      previewImage.alt = "";
    }
    previewPlaceholder.textContent = t(previewLoading ? "pets.preview.loading" : previewFailure ? "pets.preview.unavailable" : "pets.preview.choose");
    if (panel?.runtime.selectionStatus === "unavailable" && browseKey === selectedKey()) {
      previewMessage.textContent = t("pets.selectedUnavailable");
    } else if (panel?.snapshot.stale) {
      previewMessage.textContent = t("pets.refresh.staleError");
    } else if (previewFailure) {
      previewMessage.textContent = previewFailure;
    } else {
      previewMessage.textContent = "";
    }
    useButton.disabled = !browseKey || switching || browseKey === selectedKey();
    retryButton.hidden = !browseKey || !previewFailure || switching;
    retryButton.disabled = switching;
    clearButton.disabled = !selectedKey() || switching;
  }

  function loadThumbnail(key: string) {
    if (thumbnailCache.has(key) || thumbnailRequests.has(key) || thumbnailRequests.size >= maxThumbnailRequests) {
      return;
    }
    if (document.visibilityState !== "visible" || !document.hasFocus()) {
      return;
    }
    const epoch = thumbnailEpoch;
    thumbnailRequests.set(key, epoch);
    void PetSettingsService.PreviewPet(key).then(async (next) => {
      const dataURL = await getPreviewImage(next.previewRef);
      if (thumbnailRequests.get(key) === epoch) {
        thumbnailRequests.delete(key);
      }
      if (epoch !== thumbnailEpoch) {
        return;
      }
      thumbnailCache.set(key, dataURL || null);
      renderList();
    }).catch(() => {
      if (thumbnailRequests.get(key) === epoch) {
        thumbnailRequests.delete(key);
      }
      if (epoch !== thumbnailEpoch) {
        return;
      }
      thumbnailCache.set(key, null);
      renderList();
    });
  }

  function renderList() {
    const activeElement = document.activeElement;
    const focusedKey = activeElement instanceof HTMLButtonElement && list.contains(activeElement)
      ? activeElement.dataset.petKey
      : undefined;
    const scanning = loading || (refreshing && panel?.snapshot.scanState === "never-scanned");
    if (scanning) {
      list.replaceChildren();
      listCount.textContent = "—";
      empty.hidden = false;
      emptyTitle.textContent = t("pets.loading");
      emptyDescription.hidden = true;
      invalidIssues.hidden = true;
      return;
    }
    if (!panel && !loading) {
      list.replaceChildren(); listCount.textContent = "—";
      empty.hidden = false; emptyTitle.textContent = t("view.unavailable");
      emptyDescription.hidden = true; invalidIssues.hidden = true;
      return;
    }
    const items = visibleItems();
    const keyboardKey = items.find((item) => item.stableSourceKey === browseKey && item.availability === "ready")?.stableSourceKey
      ?? items.find((item) => item.availability === "ready")?.stableSourceKey;
    list.tabIndex = keyboardKey ? -1 : 0;
    list.replaceChildren();
    listCount.textContent = t("pets.list.count", {count: panel?.snapshot.items?.length ?? 0});
    empty.hidden = items.length > 0;
    emptyTitle.textContent = search.value.trim() ? t("pets.noSearchResults") : t("pets.empty.title");
    if (!search.value.trim()) {
      emptyDescription.hidden = false;
      emptyDescription.textContent = t("pets.empty.description");
    } else {
      emptyDescription.hidden = true;
    }
    for (const item of items) {
      const option = document.createElement("button");
      option.type = "button";
      option.className = `manager-list-item pets-list-item${item.stableSourceKey === browseKey ? " is-selected" : ""}`;
      option.dataset.petKey = item.stableSourceKey;
      option.tabIndex = item.stableSourceKey === keyboardKey ? 0 : -1;
      option.setAttribute("role", "option");
      option.setAttribute("aria-selected", String(item.stableSourceKey === browseKey));
      option.disabled = switching || refreshing || item.availability !== "ready";
      const thumbnail = document.createElement("span");
      thumbnail.className = "pets-thumbnail";
      thumbnail.setAttribute("aria-hidden", "true");
      const thumbnailURL = thumbnailCache.get(item.stableSourceKey);
      if (thumbnailURL) {
        const image = document.createElement("img");
        image.src = thumbnailURL;
        image.alt = "";
        image.decoding = "async";
        thumbnail.append(image);
      } else {
        thumbnail.classList.add("is-placeholder");
      }
      const content = document.createElement("span");
      content.className = "pets-list-copy";
      const name = document.createElement("strong");
      name.textContent = item.displayName;
      const detail = document.createElement("span");
      const status = item.current ? ` · ${t("pets.current.status")}` : "";
      detail.textContent = `${sourceBadge(item)}${status}`;
      content.append(name, detail);
      option.append(thumbnail, content);
      option.addEventListener("click", () => void browse(item.stableSourceKey));
      list.append(option);
      if (item.availability === "ready") {
        loadThumbnail(item.stableSourceKey);
      }
    }
    // Selection and asynchronous thumbnails rebuild rows. Keep keyboard focus
    // on the same pet without moving the outer Settings scroll position.
    if (focusedKey) {
      const replacement = list.querySelector<HTMLButtonElement>(`[data-pet-key="${CSS.escape(focusedKey)}"]`);
      if (replacement && !replacement.disabled) replacement.focus({preventScroll: true});
      else list.focus({preventScroll: true});
    }
    renderIssues();
  }

  function render() {
    renderVisibility();
    renderSize();
    renderList();
    renderPreview();
    search.disabled = !panel || switching || refreshing;
    refreshButton.disabled = refreshing || switching;
    refreshButton.textContent = refreshing ? t("pets.refresh.inProgress") : t("pets.refresh.action");
  }

  async function browse(key: string) {
    browseKey = key;
    preview = undefined;
    previewDataURL = "";
    previewFailure = "";
    const request = ++previewRequest;
    previewLoading = document.visibilityState === "visible" && document.hasFocus();
    render();
    if (!previewLoading) {
      return;
    }
    try {
      const next = await PetSettingsService.PreviewPet(key);
      if (request !== previewRequest || browseKey !== key) {
        return;
      }
      preview = next;
      renderPreview();
      try {
        const dataURL = await getPreviewImage(next.previewRef);
        if (request !== previewRequest || browseKey !== key) {
          return;
        }
        previewDataURL = dataURL;
        previewFailure = dataURL ? "" : t("pets.preview.unavailable");
        if (dataURL) {
          thumbnailCache.set(key, dataURL);
        }
      } catch (error) {
        if (request !== previewRequest || browseKey !== key) {
          return;
        }
        previewFailure = errorMessage(error, t("pets.preview.unavailable"));
      }
      renderPreview();
    } catch (error) {
      if (request !== previewRequest || browseKey !== key) {
        return;
      }
      previewFailure = errorMessage(error, t("pets.preview.unavailable"));
      renderPreview();
    } finally {
      if (request === previewRequest) {
        previewLoading = false;
        renderPreview();
      }
    }
  }

  function applyPanel(next: PetPanel) {
    panel = next;
    const committed = selectedKey();
    if (!browseKey || !(panel.snapshot.items ?? []).some((item) => item.stableSourceKey === browseKey)) {
      browseKey = committed;
    }
    render();
  }

  async function refreshCatalog() {
    if (refreshing) {
      return;
    }
    cancelSizeCommit();
    refreshing = true;
    thumbnailEpoch += 1;
    thumbnailCache.clear();
    thumbnailRequests.clear();
    preview = undefined;
    previewDataURL = "";
    previewFailure = "";
    render();
    try {
      applyPanel(await PetSettingsService.RefreshPetCatalog());
      if (browseKey && document.visibilityState === "visible" && document.hasFocus()) {
        void browse(browseKey);
      }
    } catch (error) {
      const message = errorMessage(error, t("pets.refresh.failed"));
      previewMessage.textContent = panel?.snapshot.stale ? t("pets.refresh.staleError") : message;
      previewFailure = message;
    } finally {
      refreshing = false;
      render();
    }
  }

  async function refresh(): Promise<boolean> {
    cancelSizeCommit();
    loading = true;
    render();
    try {
      const next = await PetSettingsService.GetPetPanel();
      applyPanel(next);
      if (next.snapshot.scanState === "never-scanned") {
        await refreshCatalog();
      } else if (browseKey) {
        void browse(browseKey);
      }
      return true;
    } catch (error) {
      const message = errorMessage(error, t("error.loadSettings"));
      previewMessage.textContent = message;
      previewFailure = message;
      return false;
    } finally {
      loading = false;
      render();
    }
  }

  async function setVisibility() {
    visibilityFailure = "";
    const nextValue = visibility.checked;
    const finishUpdate = beginControlUpdate(visibility);
    try {
      applyPanel(await PetSettingsService.SetPetVisibility(nextValue));
    } catch (error) {
      visibility.checked = !nextValue;
      visibilityFailure = errorMessage(error, t("error.saveSettings"));
    } finally {
      finishUpdate();
      renderVisibility();
    }
  }

  async function commitSize() {
    if (sizeCommitInFlight || !pendingSize) {
      return;
    }
    const request = pendingSize.request;
    const percent = pendingSize.percent;
    pendingSize = undefined;
    if (request !== sizeRequest || size.disabled) {
      return;
    }
    sizeCommitInFlight = true;
    try {
      const next = await PetSettingsService.SetPetSize(percent);
      if (request !== sizeRequest) {
        return;
      }
      applyPanel(next);
    } catch (error) {
      if (request !== sizeRequest) {
        return;
      }
      sizeFailure = errorMessage(error, t("pets.size.failed"));
      renderSize();
    } finally {
      sizeCommitInFlight = false;
      if (hasCurrentPendingSize()) {
        void commitSize();
      }
    }
  }

  function scheduleSizeCommit(immediate = false) {
    sizeFailure = "";
    sizeStatus.textContent = "";
    const percent = readSizePercent();
    cancelSizeCommit();
    updateSizeValue();
    if (percent === undefined || size.disabled) {
      renderSize();
      return;
    }
    const request = sizeRequest;
    pendingSize = {request, percent};
    if (immediate) {
      void commitSize();
      return;
    }
    sizeTimer = window.setTimeout(() => {
      sizeTimer = undefined;
      void commitSize();
    }, 160);
  }

  async function selectPet() {
    previewFailure = "";
    if (!browseKey) {
      previewFailure = t("pets.selectFirst");
      renderPreview();
      return;
    }
    cancelSizeCommit();
    switching = true;
    render();
    try {
      applyPanel(await PetSettingsService.SelectPet(browseKey));
    } catch (error) {
      previewFailure = errorMessage(error, t("pets.switchFailed"));
    } finally {
      switching = false;
      render();
    }
  }

  async function clearSelection() {
    previewFailure = "";
    cancelSizeCommit();
    switching = true;
    render();
    try {
      applyPanel(await PetSettingsService.ClearPetSelection());
      browseKey = undefined;
      preview = undefined;
      previewDataURL = "";
      previewFailure = "";
    } catch (error) {
      previewFailure = errorMessage(error, t("error.saveSettings"));
    } finally {
      switching = false;
      render();
    }
  }

  function moveBrowse(delta: number, boundary?: "start" | "end") {
    const items = visibleItems().filter((item) => item.availability === "ready");
    if (items.length === 0) {
      return;
    }
    const currentIndex = items.findIndex((item) => item.stableSourceKey === browseKey);
    const index = boundary === "start"
      ? 0
      : boundary === "end"
        ? items.length - 1
        : (currentIndex < 0 ? (delta > 0 ? -1 : 0) : currentIndex) + delta;
    const next = items[(index + items.length) % items.length];
    if (next) {
      void browse(next.stableSourceKey);
      const option = list.querySelector<HTMLButtonElement>(`[data-pet-key="${CSS.escape(next.stableSourceKey)}"]`);
      option?.focus();
    }
  }

  refreshButton.addEventListener("click", () => void (panel ? refreshCatalog() : refresh()));
  search.addEventListener("input", () => renderList());
  visibility.addEventListener("change", () => void setVisibility());
  alwaysOnTop.addEventListener("change", async () => {
    const nextValue=alwaysOnTop.checked;
    const finish=beginControlUpdate(alwaysOnTop);
    alwaysOnTopStatus.textContent="";
    try { applyPanel(await PetSettingsService.SetPetAlwaysOnTop(nextValue)); }
    catch(error) { alwaysOnTopStatus.textContent=errorMessage(error,t("error.saveSettings")); }
    finally { finish();renderVisibility(); }
  });
  size.addEventListener("input", () => scheduleSizeCommit());
  size.addEventListener("change", () => scheduleSizeCommit(true));
  useButton.addEventListener("click", () => void selectPet());
  retryButton.addEventListener("click", () => {
    if (browseKey) {
      void browse(browseKey);
    }
  });
  clearButton.addEventListener("click", () => void clearSelection());
  list.addEventListener("keydown", (event) => {
    switch (event.key) {
      case "ArrowDown":
        event.preventDefault();
        moveBrowse(1);
        break;
      case "ArrowUp":
        event.preventDefault();
        moveBrowse(-1);
        break;
      case "Home":
        event.preventDefault();
        moveBrowse(0, "start");
        break;
      case "End":
        event.preventDefault();
        moveBrowse(0, "end");
        break;
      case "Enter":
      case " ":
        if (document.activeElement instanceof HTMLButtonElement && document.activeElement.dataset.petKey) {
          event.preventDefault();
          void browse(document.activeElement.dataset.petKey);
        }
        break;
    }
  });
  function pausePreview() {
    previewRequest += 1;
    previewLoading = false;
    thumbnailEpoch += 1;
    previewDataURL = "";
    renderPreview();
  }

  window.addEventListener("blur", pausePreview);
  document.addEventListener("visibilitychange", () => {
    if (document.visibilityState !== "visible") {
      pausePreview();
    }
  });
  window.addEventListener("focus", () => {
    if (browseKey) {
      void browse(browseKey);
    }
  });
  subscribeLocale(() => {
    render();
  });

  render();
  return {refresh, renderLocale: render};
}
