/** Shared setting row built around a native checkbox. The host owns its value. */
export interface SettingSwitchOptions {
  id?: string;
  ariaLabel?: string;
  labelledBy?: string;
  describedBy?: string;
  checked?: boolean;
  disabled?: boolean;
  onChange?: (enabled: boolean) => void;
}

export function createSettingSwitch(options: SettingSwitchOptions): HTMLLabelElement {
  const hitArea = document.createElement("label");
  hitArea.className = "switch";
  if (options.id) hitArea.htmlFor = options.id;

  const input = document.createElement("input");
  input.type = "checkbox";
  input.setAttribute("role", "switch");
  if (options.id) input.id = options.id;
  if (options.ariaLabel) input.setAttribute("aria-label", options.ariaLabel);
  if (options.labelledBy) input.setAttribute("aria-labelledby", options.labelledBy);
  if (options.describedBy) input.setAttribute("aria-describedby", options.describedBy);
  input.checked = options.checked ?? false;
  input.disabled = options.disabled ?? false;
  if (options.onChange) input.addEventListener("change", () => options.onChange!(input.checked));

  const track = document.createElement("span");
  track.className = "switch-track";
  track.setAttribute("aria-hidden", "true");
  const thumb = document.createElement("span");
  thumb.className = "switch-thumb";
  track.append(thumb);
  hitArea.append(input, track);
  return hitArea;
}

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
    const hitArea = createSettingSwitch({
      id,
      labelledBy: title.id,
      describedBy: descriptions.length ? descriptions.join(" ") : undefined,
      disabled: row.hasAttribute("data-disabled"),
    });
    control.append(hitArea);
    row.classList.add("setting-row");
    row.replaceChildren(copy, control);
  }
}
