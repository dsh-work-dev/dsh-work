/** Shared setting row built around a native checkbox. The host owns its value. */
export function mountSettingSwitches(root: ParentNode) {
  for (const row of Array.from(root.querySelectorAll<HTMLElement>("[data-setting-switch]"))) {
    const id = row.dataset.settingSwitch!;
    const labelKey = row.dataset.label!;
    const detailKey = row.dataset.detail;
    const copy = document.createElement("div");
    copy.className = "setting-copy";
    const label = document.createElement("label");
    label.htmlFor = id;
    const title = document.createElement("strong");
    title.id = `${id}-label`;
    title.dataset.i18n = labelKey;
    label.append(title);
    copy.append(label);

    const descriptions: string[] = [];
    if (detailKey) {
      const detail = document.createElement("span");
      detail.className = "setting-detail";
      detail.id = `${id}-detail`;
      detail.dataset.i18n = detailKey;
      copy.append(detail);
      descriptions.push(detail.id);
    }
    if (row.dataset.status) descriptions.push(row.dataset.status);

    const control = document.createElement("div");
    control.className = "setting-control";
    const hitArea = document.createElement("label");
    hitArea.className = "switch";
    hitArea.htmlFor = id;
    const input = document.createElement("input");
    input.type = "checkbox";
    input.id = id;
    input.setAttribute("role", "switch");
    input.setAttribute("aria-labelledby", title.id);
    if (descriptions.length) input.setAttribute("aria-describedby", descriptions.join(" "));
    input.disabled = row.hasAttribute("data-disabled");
    const track = document.createElement("span");
    track.className = "switch-track";
    track.setAttribute("aria-hidden", "true");
    const thumb = document.createElement("span");
    thumb.className = "switch-thumb";
    track.append(thumb);
    hitArea.append(input, track);
    control.append(hitArea);
    row.classList.add("setting-row");
    row.replaceChildren(copy, control);
  }
}
