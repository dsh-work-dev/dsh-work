/*! Derived from Microsoft Playwright. Copyright (c) Microsoft Corporation. Apache-2.0; see LICENSE, NOTICE and PROVENANCE.txt. */

// browser-extension/playwright/relayConnection.ts
function debugLog(...args) {
  const enabled = false;
  if (enabled) {
    console.log("[Extension]", ...args);
  }
}
var ALLOWED_CHROME_COMMANDS = /* @__PURE__ */ new Set([
  "chrome.debugger.attach",
  "chrome.debugger.detach",
  "chrome.debugger.sendCommand",
  "chrome.tabs.create",
  "chrome.tabs.remove"
]);
var CHROME_EVENT_METHODS = [
  "chrome.debugger.onEvent",
  "chrome.debugger.onDetach",
  "chrome.tabs.onCreated",
  "chrome.tabs.onRemoved"
];
var REATTACH_DELAY_MS = 150;
var REATTACH_VERIFY_MS = 2500;
var REATTACH_COOLDOWN_MS = 3e3;
var RelayConnection = class {
  constructor(ws, _beforeAttach) {
    this._beforeAttach = _beforeAttach;
    this._ws = ws;
    this._installEventForwarders();
    this._ws.onmessage = this._onMessage.bind(this);
    this._ws.onclose = () => this._onClose();
  }
  _beforeAttach;
  _ws;
  _ownedTabs = /* @__PURE__ */ new Set();
  // Tabs whose debugger we have explicitly attached for this connection.
  _attachedTabs = /* @__PURE__ */ new Set();
  // Once we've attached at least one tab, detaching the last one closes the connection.
  _hasEverAttached = false;
  _eventListeners = [];
  _closed = false;
  _pendingReattach = /* @__PURE__ */ new Set();
  _recentReattach = /* @__PURE__ */ new Set();
  onclose;
  ontabattached;
  ontabdetached;
  get attachedTabs() {
    return this._attachedTabs;
  }
  // Signals the end of the initial-tab handshake — call after the initial
  // round of `attachTab` invocations. The relay holds CDP traffic from
  // Playwright until it sees this event, so that `Target.setAutoAttach` is
  // answered from a populated tab model.
  didInitialize() {
    this._sendMessage({ method: "extension.initialized", params: [] });
  }
  close(message) {
    this._ws.close(1e3, message);
    this._onClose();
  }
  // Called when the UI adds a tab to the Playwright group, whether as the
  // initial pick from the connect page or from a later drag-in. Simulates a
  // "new tab opened" event; the relay responds by calling
  // chrome.debugger.attach, which flows through _handleCommand and fires
  // ontabattached.
  attachTab(tab) {
    if (this._closed || this._attachedTabs.has(tab.id))
      return;
    this._ownedTabs.add(tab.id);
    this._sendMessage({ method: "chrome.tabs.onCreated", params: [tab] });
  }
  // Called when the UI removes a tab from the Playwright group. We detach the
  // debugger and update bookkeeping. chrome.debugger.detach does not fire
  // onDetach for the caller, so we synthesize one so the relay notices the
  // tab is gone.
  detachTab(tabId) {
    if (this._closed || !this._attachedTabs.has(tabId))
      return;
    chrome.debugger.detach({ tabId }).catch((error) => {
      debugLog("Error detaching tab:", error);
    });
    this._notifyTabDetached(tabId);
    this._sendMessage({
      method: "chrome.debugger.onDetach",
      params: [{ tabId }, "target_closed"]
    });
    this._checkLastTabDetached();
  }
  _notifyTabAttached(tabId) {
    this._attachedTabs.add(tabId);
    this._hasEverAttached = true;
    this._pendingReattach.delete(tabId);
    this.ontabattached?.(tabId);
  }
  _notifyTabDetached(tabId) {
    this._attachedTabs.delete(tabId);
    this._ownedTabs.delete(tabId);
    this.ontabdetached?.(tabId);
  }
  _installEventForwarders() {
    for (const fullMethod of CHROME_EVENT_METHODS) {
      const target = resolveChromeMember(fullMethod);
      const listener = (...args) => this._onChromeEvent(fullMethod, args);
      target.obj[target.name].addListener(listener);
      this._eventListeners.push({
        remove: () => target.obj[target.name].removeListener(listener)
      });
    }
  }
  _onClose() {
    if (this._closed)
      return;
    this._closed = true;
    this._pendingReattach.clear();
    this._recentReattach.clear();
    for (const l of this._eventListeners)
      l.remove();
    this._eventListeners = [];
    for (const tabId of [...this._attachedTabs]) {
      try {
        chrome.debugger.detach({ tabId }).catch(() => {
        });
      } catch {
      }
      try {
        this._notifyTabDetached(tabId);
      } catch (error) {
        debugLog(error);
      }
    }
    this.onclose?.();
  }
  _checkLastTabDetached() {
    if (this._hasEverAttached && this._attachedTabs.size === 0 && this._pendingReattach.size === 0)
      this.close("All controlled tabs detached");
  }
  // Forwards chrome.* events concerning attached tabs to the relay, then runs
  // shared detach bookkeeping.
  _onChromeEvent(fullMethod, args) {
    const tabId = this._tabIdForEventArgs(fullMethod, args);
    if (tabId === void 0 || !this._attachedTabs.has(tabId))
      return;
    if (fullMethod === "chrome.tabs.onCreated") this._ownedTabs.add(args[0].id);
    this._sendMessage({ method: fullMethod, params: args });
    if (fullMethod === "chrome.debugger.onDetach") {
      const reason = args[1];
      this._notifyTabDetached(tabId);
      if (reason === "target_closed" && this._maybeScheduleReattach(tabId))
        return;
      this._checkLastTabDetached();
    }
  }
  _maybeScheduleReattach(tabId) {
    if (this._closed)
      return false;
    if (this._recentReattach.has(tabId)) {
      debugLog(`Not re-attaching tab ${tabId}: re-detached within ${REATTACH_COOLDOWN_MS}ms`);
      return false;
    }
    this._recentReattach.add(tabId);
    setTimeout(() => this._recentReattach.delete(tabId), REATTACH_COOLDOWN_MS);
    this._pendingReattach.add(tabId);
    setTimeout(() => void this._tryReattach(tabId), REATTACH_DELAY_MS);
    return true;
  }
  _reattachAborted(tabId) {
    return this._closed || !this._pendingReattach.has(tabId);
  }
  async _tryReattach(tabId) {
    if (this._reattachAborted(tabId))
      return;
    let tab;
    try {
      tab = await chrome.tabs.get(tabId);
    } catch {
      this._pendingReattach.delete(tabId);
      this._checkLastTabDetached();
      return;
    }
    if (this._reattachAborted(tabId))
      return;
    if (this._attachedTabs.has(tabId)) {
      this._pendingReattach.delete(tabId);
      return;
    }
    this.attachTab(tab);
    setTimeout(() => {
      if (this._reattachAborted(tabId))
        return;
      this._pendingReattach.delete(tabId);
      if (!this._attachedTabs.has(tabId))
        this._checkLastTabDetached();
    }, REATTACH_VERIFY_MS);
  }
  // Returns the tabId an event refers to, for filtering by _attachedTabs.
  _tabIdForEventArgs(fullMethod, args) {
    switch (fullMethod) {
      case "chrome.debugger.onEvent":
      case "chrome.debugger.onDetach":
        return args[0]?.tabId;
      case "chrome.tabs.onCreated": {
        const tab = args[0];
        return tab.openerTabId;
      }
      case "chrome.tabs.onRemoved":
        return args[0];
    }
    return void 0;
  }
  _onMessage(event) {
    this._onMessageAsync(event).catch((e) => debugLog("Error handling message:", e));
  }
  async _onMessageAsync(event) {
    let message;
    try {
      message = JSON.parse(event.data);
    } catch (error) {
      debugLog(`Error parsing message ${event.data}:`, error);
      this._sendError(-32700, `Error parsing message: ${error.message}`);
      return;
    }
    const response = {
      id: message.id
    };
    try {
      response.result = await this._handleCommand(message);
    } catch (error) {
      debugLog(`Error handling command ${JSON.stringify(message)}:`, error);
      response.error = error.message;
    }
    this._sendMessage(response);
  }
  async _handleCommand(message) {
    if (this._closed) throw new Error("Connection closed");
    if (!ALLOWED_CHROME_COMMANDS.has(message.method))
      throw new Error(`Unknown method: ${message.method}`);
    const args = message.params ?? [];
    if (message.method.startsWith("chrome.debugger.")) {
      if (!this._ownedTabs.has(args[0]?.tabId)) throw new Error("Tab outside this connection");
    }
    if (message.method === "chrome.debugger.attach") await this._beforeAttach?.(args[0].tabId);
    if (this._closed) throw new Error("Connection closed");
    if (message.method === "chrome.tabs.remove") {
      const ids = Array.isArray(args[0]) ? args[0] : [args[0]];
      if (ids.some((id) => !this._ownedTabs.has(id))) throw new Error("Tab outside this connection");
    }
    const result = await invokeChromeMethod(message.method, args);
    if (message.method === "chrome.tabs.create" && result?.id !== void 0 && !this._closed) this._ownedTabs.add(result.id);
    if (message.method === "chrome.debugger.attach") {
      const target = args[0];
      if (target?.tabId !== void 0) {
        if (this._closed) chrome.debugger.detach({ tabId: target.tabId }).catch(() => {
        });
        else this._notifyTabAttached(target.tabId);
      }
    }
    return result ?? {};
  }
  _sendError(code, message) {
    this._sendMessage({
      error: {
        code,
        message
      }
    });
  }
  _sendMessage(message) {
    if (this._ws.readyState === WebSocket.OPEN)
      this._ws.send(JSON.stringify(message));
  }
};
function resolveChromeMember(fullMethod) {
  const parts = fullMethod.split(".");
  if (parts[0] !== "chrome" || parts.length < 3)
    throw new Error(`Invalid chrome method: ${fullMethod}`);
  let obj = chrome;
  for (let i = 1; i < parts.length - 1; i++) {
    obj = obj?.[parts[i]];
    if (obj === void 0)
      throw new Error(`Unknown chrome path: ${parts.slice(0, i + 1).join(".")}, calling ${fullMethod}`);
  }
  return { obj, name: parts[parts.length - 1] };
}
async function invokeChromeMethod(fullMethod, args) {
  const { obj, name } = resolveChromeMember(fullMethod);
  const fn = obj[name];
  if (typeof fn !== "function")
    throw new Error(`Not a function: ${fullMethod}`);
  return await fn.apply(obj, args);
}

// browser-extension/playwright/connectedTabGroup.ts
var PLAYWRIGHT_GROUP_TITLE = "DSH Work";
var PLAYWRIGHT_GROUP_TITLE_PREFIX = `${PLAYWRIGHT_GROUP_TITLE} \xB7 `;
var PLAYWRIGHT_GROUP_COLORS = ["green", "blue", "purple", "orange", "pink", "cyan", "yellow", "red", "grey"];
var NON_DEBUGGABLE_SCHEMES = ["chrome:", "edge:", "devtools:"];
var CONNECTED_BADGE = { text: "\u2713", color: "#4CAF50", title: "DSH Work" };
function isNonDebuggableUrl(url) {
  return !!url && NON_DEBUGGABLE_SCHEMES.some((s) => url.startsWith(s));
}
function uniqueGroupStyle(clientName, taken) {
  const titles = new Set(taken.map((style) => style.title));
  const chars = Array.from(clientName || "Untitled");
  const base = PLAYWRIGHT_GROUP_TITLE_PREFIX + chars.slice(0, 30).join("") + (chars.length > 30 ? "\u2026" : "");
  let title = base;
  for (let i = 2; titles.has(title); i++)
    title = `${base} (${i})`;
  const colors = new Set(taken.map((style) => style.color));
  const color = PLAYWRIGHT_GROUP_COLORS.find((candidate) => !colors.has(candidate)) ?? PLAYWRIGHT_GROUP_COLORS[0];
  return { title, color };
}
var ConnectedTabGroup = class {
  clientName;
  groupStyle;
  _connection;
  _isTabReserved;
  _groupId = null;
  _groupTabIds = /* @__PURE__ */ new Set();
  _onTabUpdatedListener;
  _onTabRemovedListener;
  onclose;
  ongroup;
  _closed = false;
  _groupTail = Promise.resolve();
  constructor(connection, selectedTab, clientName, groupStyle, isTabReserved) {
    this.clientName = clientName;
    this.groupStyle = groupStyle;
    this._isTabReserved = isTabReserved;
    this._connection = connection;
    this._connection.onclose = () => this._onConnectionClose();
    this._connection.ontabattached = (tabId) => this._onTabAttached(tabId);
    this._connection.ontabdetached = (tabId) => this._onTabDetached(tabId);
    this._onTabUpdatedListener = this._onTabUpdated.bind(this);
    this._onTabRemovedListener = this._onTabRemoved.bind(this);
    chrome.tabs.onUpdated.addListener(this._onTabUpdatedListener);
    chrome.tabs.onRemoved.addListener(this._onTabRemovedListener);
    this._connection.attachTab(selectedTab);
    this._connection.didInitialize();
  }
  connectedTabIds() {
    return [...this._groupTabIds];
  }
  close(reason) {
    this._connection.close(reason);
  }
  releaseTab(tabId) {
    if (!this._groupTabIds.has(tabId))
      return;
    this._groupTabIds.delete(tabId);
    this._connection.detachTab(tabId);
  }
  _onTabUpdated(tabId, changeInfo, tab) {
    if (changeInfo.groupId !== void 0)
      this._onTabGroupChanged(tabId, tab);
    if (changeInfo.url === void 0)
      return;
    if (this._connection.attachedTabs.has(tabId))
      void this._updateBadge(tabId, CONNECTED_BADGE);
    else if (this._groupTabIds.has(tabId) && !isNonDebuggableUrl(changeInfo.url))
      this._connection.attachTab(tab);
  }
  // Single entry point for group membership changes, whether the user dragged
  // or we grouped the tab ourselves. Attaches on entry (if debuggable) and
  // detaches on exit; a chrome:// tab stays in the group until it navigates
  // (handled in _onTabUpdated).
  _onTabGroupChanged(tabId, tab) {
    const inOurGroup = this._groupId !== null && tab.groupId === this._groupId;
    const wasInGroup = this._groupTabIds.has(tabId);
    if (inOurGroup === wasInGroup)
      return;
    if (inOurGroup) {
      if (this._isTabReserved(tabId)) {
        void ungroupTabs([tabId]);
        return;
      }
      this._groupTabIds.add(tabId);
      if (!isNonDebuggableUrl(tab.url))
        this._connection.attachTab(tab);
    } else {
      this._groupTabIds.delete(tabId);
      if (this._connection.attachedTabs.has(tabId))
        this._connection.detachTab(tabId);
    }
    if (this._groupId !== null) this.ongroup?.(this._groupId, [...this._groupTabIds]);
  }
  _onTabRemoved(tabId) {
    this._groupTabIds.delete(tabId);
    if (this._groupId !== null) this.ongroup?.(this._groupId, [...this._groupTabIds]);
  }
  _onTabAttached(tabId) {
    void this._updateBadge(tabId, CONNECTED_BADGE);
    void this._addTabToGroup(tabId);
  }
  // The debugger detached (drag-out, tab close, or external action). Clear the
  // badge but leave the tab in the group — the user's intent is still there,
  // and a subsequent navigation will re-attach via _onTabUpdated.
  _onTabDetached(tabId) {
    void this._updateBadge(tabId, { text: "" });
  }
  _onConnectionClose() {
    this._closed = true;
    chrome.tabs.onUpdated.removeListener(this._onTabUpdatedListener);
    chrome.tabs.onRemoved.removeListener(this._onTabRemovedListener);
    const groupTabs = [...this._groupTabIds];
    this._groupTabIds.clear();
    if (groupTabs.length)
      void ungroupTabs(groupTabs);
    this.onclose?.();
  }
  async _updateBadge(tabId, { text, color, title }) {
    try {
      await Promise.all([
        chrome.action.setBadgeText({ tabId, text }),
        chrome.action.setTitle({ tabId, title: title || "" }),
        color ? chrome.action.setBadgeBackgroundColor({ tabId, color }) : Promise.resolve()
      ]);
    } catch (error) {
    }
  }
  // Moves an already-attached tab into our Chrome tab group, creating it on
  // first use. `_groupTabIds` is updated after the await so an onUpdated event
  // that arrives concurrently (`_groupId` still null, wasInGroup still false)
  // becomes a harmless no-op rather than taking the drag-out branch.
  _addTabToGroup(tabId) {
    this._groupTail = this._groupTail.then(() => this._groupTab(tabId));
    return this._groupTail;
  }
  async _groupTab(tabId) {
    if (this._closed) return;
    if (this._groupTabIds.has(tabId))
      return;
    try {
      await retryOnDrag(async () => {
        if (this._groupId === null) {
          this._groupId = await chrome.tabs.group({ tabIds: [tabId] });
          await chrome.tabGroups.update(this._groupId, this.groupStyle);
        } else {
          await chrome.tabs.group({ groupId: this._groupId, tabIds: [tabId] });
        }
      });
      if (this._closed) {
        await ungroupTabs([tabId]);
        return;
      }
      this._groupTabIds.add(tabId);
      this.ongroup?.(this._groupId, [...this._groupTabIds]);
    } catch (error) {
      debugLog("Error adding tab to group:", error);
    }
  }
};
async function ungroupTabs(tabIds) {
  try {
    await retryOnDrag(() => chrome.tabs.ungroup(tabIds));
  } catch (error) {
    debugLog("Error ungrouping tabs:", error);
  }
}
async function retryOnDrag(fn) {
  const delays = [0, 100, 200, 400, 800];
  let lastError;
  for (const delay of delays) {
    if (delay)
      await new Promise((resolve) => setTimeout(resolve, delay));
    try {
      await fn();
      return;
    } catch (error) {
      if (!error?.message?.includes("user may be dragging a tab"))
        throw error;
      lastError = error;
    }
  }
  throw lastError;
}

// browser-extension/src/routes.ts
var MAX_FROM_EXTENSION = 8 * 1024 * 1024;
var routePattern = /^s-[a-f0-9]{32}$/;
var storageTail = Promise.resolve();
function createRoutes(post) {
  const channels = /* @__PURE__ */ new Map(), groups = /* @__PURE__ */ new Map();
  const openings = /* @__PURE__ */ new Set();
  const ownership = /* @__PURE__ */ new Map();
  let connected = true;
  const saveOwnership = () => {
    const rows = [...ownership.values()];
    const write = storageTail.then(() => chrome.storage.session.set({ ownedGroups: rows }));
    storageTail = write.catch(console.error);
    return write;
  };
  const recovery = storageTail.then(async () => {
    const { ownedGroups = [] } = await chrome.storage.session.get("ownedGroups");
    for (const row of ownedGroups) {
      if (!Number.isInteger(row.group) || !Array.isArray(row.tabs)) continue;
      const ids = [];
      for (const id of row.tabs) try {
        const tab = await chrome.tabs.get(id);
        try {
          await chrome.debugger.detach({ tabId: id });
        } catch {
        }
        ;
        if (row.group >= 0 && tab.groupId === row.group) ids.push(id);
      } catch {
      }
      if (ids.length) await ungroupTabs(ids);
    }
    await chrome.storage.session.set({ ownedGroups: [] });
  });
  storageTail = recovery.catch(console.error);
  function notify(message) {
    if (connected) try {
      post(message);
    } catch (e) {
      disconnect();
      console.error(e);
    }
  }
  function send(message) {
    if (!connected) throw Error("BROWSER_NATIVE_DISCONNECTED");
    if (new TextEncoder().encode(JSON.stringify(message)).length > MAX_FROM_EXTENSION) throw Error("BROWSER_NATIVE_MESSAGE_TOO_LARGE");
    post(message);
  }
  function disconnect() {
    if (!connected) return;
    connected = false;
    for (const socket of [...channels.values()]) try {
      socket.releaseLocal();
    } catch (e) {
      console.error(e);
    }
  }
  async function receive(m) {
    if (!["open", "wire", "close"].includes(m.kind)) return false;
    if (!routePattern.test(m.route)) throw Error("BROWSER_ROUTE_INVALID");
    try {
      if (m.kind === "open") {
        if (m.wireProtocol !== 2 || channels.has(m.route) || openings.has(m.route)) throw Error("BROWSER_UPDATE_REQUIRED");
        if (channels.size + openings.size >= 9) throw Error("BROWSER_ROUTE_LIMIT");
        openings.add(m.route);
        const token = {};
        const pending = token;
        openingTokens.set(m.route, pending);
        await recovery;
        if (!connected || openingTokens.get(m.route) !== pending) return true;
        const tab = await chrome.tabs.create({ url: "about:blank", active: true });
        if (!connected || openingTokens.get(m.route) !== pending) return true;
        const socket = {
          readyState: 1,
          onmessage: null,
          onclose: null,
          send: (data) => {
            try {
              send({ kind: "wire", route: m.route, payload: JSON.parse(data) });
            } catch (e) {
              try {
                socket.releaseLocal();
              } finally {
                notify({ kind: "route-error", route: m.route, error: e.message });
              }
            }
          },
          releaseLocal: () => {
            if (socket.readyState !== 1) return;
            socket.readyState = 3;
            try {
              socket.onclose?.();
            } finally {
              channels.delete(m.route);
              groups.delete(m.route);
              ownership.delete(m.route);
              void saveOwnership().catch(console.error);
            }
          },
          close: () => {
            try {
              socket.releaseLocal();
            } finally {
              notify({ kind: "closed", route: m.route });
            }
          }
        };
        channels.set(m.route, socket);
        const relay = new RelayConnection(socket, async (tabId) => {
          const old = ownership.get(m.route) ?? { group: -1, tabs: [] };
          ownership.set(m.route, { group: old.group, tabs: [.../* @__PURE__ */ new Set([...old.tabs, tabId])] });
          await saveOwnership();
          if (socket.readyState !== 1) throw Error("BROWSER_ROUTE_CLOSED");
        });
        const group = new ConnectedTabGroup(
          relay,
          tab,
          String(m.title || "Untitled"),
          uniqueGroupStyle(String(m.title || "Untitled"), [...groups.values()].map((g) => g.groupStyle)),
          (tabId) => [...groups].some(([route, g]) => route !== m.route && g.connectedTabIds().includes(tabId))
        );
        group.ongroup = (id, tabs) => {
          ownership.set(m.route, { group: id, tabs });
          void saveOwnership().catch(console.error);
        };
        group.onclose = () => {
          channels.delete(m.route);
          groups.delete(m.route);
          ownership.delete(m.route);
          void saveOwnership().catch(console.error);
        };
        groups.set(m.route, group);
      } else if (m.kind === "wire") {
        const socket = channels.get(m.route);
        if (!socket) throw Error("BROWSER_ROUTE_CLOSED");
        socket.onmessage?.({ data: JSON.stringify(m.payload) });
      } else {
        openingTokens.delete(m.route);
        channels.get(m.route)?.close();
      }
    } catch (e) {
      try {
        channels.get(m.route)?.releaseLocal();
      } finally {
        notify({ kind: "route-error", route: m.route, error: e.message });
      }
    } finally {
      if (m.kind === "open") {
        openings.delete(m.route);
        openingTokens.delete(m.route);
      }
    }
    return true;
  }
  const openingTokens = /* @__PURE__ */ new Map();
  return { receive, disconnect };
}
export {
  createRoutes
};
