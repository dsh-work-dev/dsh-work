(() => {
  const cfg = __MAC_PREVIEW_CONFIG__;
  if (globalThis.__MAC_PREVIEW_SHELL__ === cfg.nonce) return;
  globalThis.__MAC_PREVIEW_SHELL__ = cfg.nonce;

  let slot = "", documentToken = "", pending;
  const frame = () => document.querySelector("iframe");
  const origin = () => globalThis.__DSH_WORK_ORIGINS__?.origin(frame()?.src);
  let sending = Promise.resolve();
  const send = value => {
    const body = JSON.stringify({...value, nonce: cfg.nonce, slot, document: documentToken});
    sending = sending.then(() => fetch("/__mac-preview/report", {
      method: "POST", headers: {"Content-Type": "application/json"}, body
    })).catch(() => {});
  };

  addEventListener("message", event => {
    const expected = origin();
    if (!expected || event.source !== frame()?.contentWindow || event.origin !== expected) return;
    const m = event.data;
    if (m?.version !== 1) return;
    if (m.type === "dsh-work/browser" && m.action === "update" && m.address === cfg.url &&
        typeof m.slot === "string" && typeof m.document === "string" && m.document) {
      slot = m.slot;
      documentToken = m.document;
      if (pending) {
        send(pending);
        pending = undefined;
      }
    }
    if (m.type === "dsh-work/mac-preview-report" && m.nonce === cfg.nonce &&
        ["slot-requested", "hidden-requested", "sidebar-reopened", "guide-tab-selected", "browser-tab-selected", "shell-overlay-opened", "provider-overlay-opened", "shell-overlay-still-blocked", "shell-overlay-closed", "stale-layout-rejected", "prompt-answered", "popup-answered", "failure"].includes(m.stage)) {
      const value = {
        stage: m.stage, session: m.session, step: m.step, browserSidebar: m.browserSidebar,
        workerOrigin: event.origin, shellOrigin: location.origin
      };
      if (m.stage === "slot-requested" && !slot) pending = value;
      else send(value);
    }
  });

  globalThis.__MAC_PREVIEW_COMMAND__ = message => {
    if (message.nonce !== cfg.nonce || !["hide", "show", "switch-to-guide", "switch-to-browser", "open-shell-overlay", "open-provider-overlay", "close-shell-overlay", "close-provider-overlay", "send-stale-layout", "answer-prompt", "deny-popup", "allow-popup", "open-workspace-picker"].includes(message.command)) return;
    const expected = origin();
    if (expected) frame()?.contentWindow?.postMessage({
      version: 1, type: "dsh-work/mac-preview-command", nonce: cfg.nonce, command: message.command, slot
    }, expected);
  };
  send({stage: "shell-ready", shellOrigin: location.origin});
})();
