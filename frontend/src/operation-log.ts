import {t} from "./i18n";

// One bounded transcript per visible operation. The user may scroll backwards
// while more output arrives; follow the tail only when already near the bottom.
export function mountOperationLog(parent: HTMLElement) {
  const details = document.createElement("details");
  details.className = "operation-log";
  details.hidden = true;
  const summary = document.createElement("summary");
  const toolbar = document.createElement("div");
  toolbar.className = "operation-log-toolbar";
  const copy = document.createElement("button");
  copy.type = "button";
  copy.className = "button button-secondary button-compact";
  const output = document.createElement("pre");
  output.tabIndex = 0;
  output.className = "operation-log-output";
  toolbar.append(copy);
  details.append(summary, toolbar, output);
  parent.append(details);
  let operationID = "";
  let lines: string[] = [];
  let size = 0;
  let frame = 0;
  let lastStep = "";
  const labels = () => {
    summary.textContent = t("logs.title");
    copy.textContent = t("logs.copy");
    output.setAttribute("aria-label", t("logs.title"));
  };
  labels();
  copy.addEventListener("click", async () => {
    try {
      await navigator.clipboard.writeText(lines.join("\n"));
      copy.textContent = t("logs.copied");
    } catch {
      copy.textContent = t("logs.copyFailed");
      output.focus();
    }
  });
  function append(id: string, message: string, failed = false, stepKey?: string) {
    if (id !== operationID) {
      details.open = false;
      operationID = id;
      lines = []; size = 0; lastStep = "";

      output.textContent = "";
    }
    if (stepKey && stepKey === lastStep) return;
    if (stepKey) lastStep = stepKey;
    labels();
    details.hidden = false;
    details.dataset.failed = String(failed);
    if (failed) details.open = true;
    const time = new Date().toLocaleTimeString([], {hour12: false});
    for (const line of message.split(/\r?\n/)) {
      if (!line.trim()) continue;
      const entry = `${time}  ${line}`;
      lines.push(entry); size += entry.length + 1;
    }
    while (lines.length > 300 || size > 64 * 1024) size -= (lines.shift()?.length ?? 0) + 1;
    if (!frame) frame = requestAnimationFrame(() => {
      frame = 0;
      const follow = output.scrollHeight - output.scrollTop - output.clientHeight < 40;
      output.textContent = lines.join("\n");
      if (follow) output.scrollTop = output.scrollHeight;
    });
  }
  function clear() {
    if (frame) cancelAnimationFrame(frame);
    frame = 0;
    operationID = "";
    lines = []; size = 0; lastStep = "";
    output.textContent = "";
    details.hidden = true;
  }
  return {append, clear, labels, text: () => lines.join("\n")};
}
