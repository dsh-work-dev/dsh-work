/*! Microsoft Playwright, Apache-2.0. See browser-extension/LICENSE, NOTICE and PROVENANCE.txt. */

// browser-extension/playwright/manualPromise.ts
var ManualPromise = class extends Promise {
  _resolve;
  _reject;
  _isDone;
  constructor() {
    let resolve;
    let reject;
    super((f, r) => {
      resolve = f;
      reject = r;
    });
    this._isDone = false;
    this._resolve = resolve;
    this._reject = reject;
  }
  isDone() {
    return this._isDone;
  }
  resolve(t) {
    this._isDone = true;
    this._resolve(t);
  }
  reject(e) {
    this._isDone = true;
    this._reject(e);
  }
  static get [Symbol.species]() {
    return Promise;
  }
  get [Symbol.toStringTag]() {
    return "ManualPromise";
  }
};

// browser-extension/playwright/log.ts
function logUnhandledError(e) {
  console.error(e);
}

// browser-extension/playwright/browserModel.ts
var BrowserModel = class {
  _sendToExtension;
  // Set only while a Playwright CDP connection is attached (see
  // `connectOverCDP`). Before that, any attempt to emit to Playwright is a
  // no-op — the model is observation-only during the extension handshake.
  _sendToCDPClient = null;
  // Tabs observed via chrome.tabs.onCreated, whether or not the debugger is attached yet.
  _knownTabs = /* @__PURE__ */ new Map();
  // Subset of _knownTabs we've attached the debugger to and assigned a sessionId.
  _tabSessions = /* @__PURE__ */ new Map();
  _tabAttaching = /* @__PURE__ */ new Map();
  _autoAttach = false;
  _nextSessionId = 1;
  constructor(sendToExtension) {
    this._sendToExtension = sendToExtension;
  }
  // Wires the model's CDP output sink. Called by the handler once the
  // extension handshake is done and a Playwright CDP client is ready to
  // receive events. Before this call the model is observation-only —
  // `Target.attachedToTarget` etc. would be answered into a black hole.
  connectOverCDP(sendToCDPClient) {
    this._sendToCDPClient = sendToCDPClient;
  }
  _emit(message) {
    this._sendToCDPClient?.(message);
  }
  // ─── Extension → model inputs ─────────────────────────────────────────
  onTabCreated(tab) {
    if (tab.id === void 0)
      return;
    this._knownTabs.set(tab.id, tab);
    if (this._autoAttach)
      void this._attachTab(tab.id).catch(logUnhandledError);
  }
  onTabRemoved(tabId) {
    this._knownTabs.delete(tabId);
    this._detachTab(tabId);
  }
  onDebuggerEvent(source, method, params) {
    if (source.tabId === void 0)
      return;
    const tabSession = this._tabSessions.get(source.tabId);
    if (!tabSession)
      return;
    const childSessionId = params?.sessionId;
    if (method === "Target.attachedToTarget" && childSessionId)
      tabSession.childSessions.add(childSessionId);
    else if (method === "Target.detachedFromTarget" && childSessionId)
      tabSession.childSessions.delete(childSessionId);
    const sessionId = source.sessionId || tabSession.sessionId;
    this._emit({ sessionId, method, params });
  }
  onDebuggerDetach(source) {
    if (source.tabId !== void 0)
      this._detachTab(source.tabId);
  }
  // ─── Playwright → model commands ──────────────────────────────────────
  // Turn auto-attach on and attach to every tab we already know about.
  // Called in response to Playwright's `Target.setAutoAttach` on the root session.
  async enableAutoAttach() {
    this._autoAttach = true;
    const tabIds = [...this._knownTabs.keys()];
    await Promise.all(tabIds.map((tabId) => this._attachTab(tabId).catch(logUnhandledError)));
  }
  async createTarget(url) {
    const tab = await this._sendToExtension("chrome.tabs.create", [{ url }]);
    if (tab?.id === void 0)
      throw new Error("Failed to create tab");
    this._knownTabs.set(tab.id, tab);
    const tabSession = await this._attachTab(tab.id);
    return { targetId: tabSession.targetInfo?.targetId };
  }
  async closeTarget(targetId) {
    const tabSession = targetId ? this._findTabSession((s) => s.targetInfo?.targetId === targetId) : void 0;
    if (!tabSession)
      return { success: false };
    await this._sendToExtension("chrome.tabs.remove", [tabSession.tabId]);
    return { success: true };
  }
  getTargetInfo(sessionId) {
    if (!sessionId)
      return void 0;
    return this._findTabSession((s) => s.sessionId === sessionId)?.targetInfo;
  }
  // Forward a browser-level CDP command (Storage.*, Browser.*, etc.) that is
  // not associated with a specific tab. chrome.debugger.sendCommand requires a
  // target, so we route through any attached tab; the command itself is
  // browser-scoped and returns the same result regardless of which tab is used.
  async sendBrowserCommand(method, params) {
    const tabSession = this._tabSessions.values().next().value;
    if (!tabSession)
      throw new Error(`No attached tab to forward browser-level command: ${method}`);
    return await this._sendToExtension("chrome.debugger.sendCommand", [
      { tabId: tabSession.tabId },
      method,
      params
    ]);
  }
  // Forward a CDP command from Playwright to the tab its sessionId resolves to.
  async sendCommand(sessionId, method, params) {
    let tabSession = this._findTabSession((s) => s.sessionId === sessionId);
    let cdpSessionId;
    if (!tabSession) {
      tabSession = this._findTabSession((s) => s.childSessions.has(sessionId));
      cdpSessionId = sessionId;
    }
    if (!tabSession)
      throw new Error(`No tab found for sessionId: ${sessionId}`);
    return await this._sendToExtension("chrome.debugger.sendCommand", [
      { tabId: tabSession.tabId, sessionId: cdpSessionId },
      method,
      params
    ]);
  }
  // ─── Internals ────────────────────────────────────────────────────────
  async _attachTab(tabId) {
    const existing = this._tabSessions.get(tabId);
    if (existing)
      return existing;
    const pending = this._tabAttaching.get(tabId);
    if (pending)
      return pending;
    const attaching = this._attachTabOnce(tabId);
    this._tabAttaching.set(tabId, attaching);
    try {
      return await attaching;
    } finally {
      this._tabAttaching.delete(tabId);
    }
  }
  async _attachTabOnce(tabId) {
    await this._sendToExtension("chrome.debugger.attach", [{ tabId }, "1.3"]);
    const result = await this._sendToExtension("chrome.debugger.sendCommand", [
      { tabId },
      "Target.getTargetInfo"
    ]);
    const targetInfo = result?.targetInfo;
    if (!this._knownTabs.has(tabId))
      throw new Error("Tab closed while attaching");
    const sessionId = `pw-tab-${this._nextSessionId++}`;
    const tabSession = { tabId, sessionId, targetInfo, childSessions: /* @__PURE__ */ new Set() };
    this._tabSessions.set(tabId, tabSession);
    this._emit({
      method: "Target.attachedToTarget",
      params: {
        sessionId,
        targetInfo: { ...targetInfo, attached: true },
        waitingForDebugger: false
      }
    });
    return tabSession;
  }
  _detachTab(tabId) {
    const tabSession = this._tabSessions.get(tabId);
    if (!tabSession)
      return;
    this._tabSessions.delete(tabId);
    this._emit({
      method: "Target.detachedFromTarget",
      params: {
        sessionId: tabSession.sessionId,
        targetId: tabSession.targetInfo?.targetId
      }
    });
  }
  _findTabSession(predicate) {
    for (const session of this._tabSessions.values()) {
      if (predicate(session))
        return session;
    }
    return void 0;
  }
};

// browser-extension/playwright/cdpRelayV2.ts
var ExtensionProtocolV2 = class {
  _model;
  // Resolved by `extension.initialized`. Purely a handshake signal for the
  // relay — the model itself is oblivious to this phase.
  _ready = new ManualPromise();
  constructor(sendCommand) {
    this._model = new BrowserModel(sendCommand);
    void this._ready.catch(logUnhandledError);
  }
  // Resolves once the extension has completed its initial handshake and the
  // relay may start processing CDP commands from Playwright.
  ready() {
    return this._ready;
  }
  connectOverCDP(sendToCDPClient) {
    this._model.connectOverCDP(sendToCDPClient);
  }
  // Called when the extension WebSocket closes. Rejects a pending `ready()`
  // promise so a blocked `establishExtensionConnection` bails out instead of
  // hanging forever.
  onExtensionDisconnect(reason) {
    if (!this._ready.isDone())
      this._ready.reject(new Error(`Extension disconnected before initialization: ${reason}`));
  }
  handleExtensionEvent(method, params) {
    switch (method) {
      case "chrome.debugger.onEvent": {
        const [source, cdpMethod, cdpParams] = params;
        this._model.onDebuggerEvent(source, cdpMethod, cdpParams);
        break;
      }
      case "chrome.debugger.onDetach": {
        const [source] = params;
        this._model.onDebuggerDetach(source);
        break;
      }
      case "chrome.tabs.onCreated": {
        const [tab] = params;
        this._model.onTabCreated(tab);
        break;
      }
      case "chrome.tabs.onRemoved": {
        const [tabId] = params;
        this._model.onTabRemoved(tabId);
        break;
      }
      case "extension.initialized": {
        this._ready.resolve();
        break;
      }
    }
  }
  // Handles a protocol-specific CDP command. Returns { result } if handled,
  // undefined to fall through to forwarding to the extension.
  async handleCDPCommand(method, params, sessionId) {
    switch (method) {
      case "Target.setAutoAttach": {
        if (sessionId)
          return void 0;
        await this._model.enableAutoAttach();
        return { result: {} };
      }
      case "Target.createTarget":
        return { result: await this._model.createTarget(params?.url) };
      case "Target.closeTarget":
        return { result: await this._model.closeTarget(params?.targetId) };
      case "Target.getTargetInfo":
        return { result: this._model.getTargetInfo(sessionId) };
    }
    return void 0;
  }
  async forwardToExtension(method, params, sessionId) {
    if (!sessionId)
      return await this._model.sendBrowserCommand(method, params);
    return await this._model.sendCommand(sessionId, method, params);
  }
};
export {
  ExtensionProtocolV2
};
