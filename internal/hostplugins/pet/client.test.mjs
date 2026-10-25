import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import test from 'node:test';

class FakeClock {
  #next = 0;
  #timers = new Map();
  setTimeout(callback, delay) { const id = ++this.#next; this.#timers.set(id, {callback, due: delay}); return id; }
  clearTimeout(id) { this.#timers.delete(id); }
  async tick(delay) {
    for (const timer of this.#timers.values()) timer.due -= delay;
    while (true) {
      const due = [...this.#timers].find(([, timer]) => timer.due <= 0);
      if (!due) break;
      const [id, timer] = due;
      this.#timers.delete(id);
      timer.callback();
      await settle();
    }
  }
}

class EventTarget {
  listeners = new Map();
  addEventListener(type, listener) { const set = this.listeners.get(type) ?? new Set(); set.add(listener); this.listeners.set(type, set); }
  removeEventListener(type, listener) { this.listeners.get(type)?.delete(listener); }
  dispatch(type, values = {}) { for (const listener of this.listeners.get(type) ?? []) listener({type, ...values}); }
}

async function settle() { for (let i = 0; i < 8; i++) await Promise.resolve(); }

async function mountPetClient(fetchImpl) {
  const source = await readFile(new URL('./client.js', import.meta.url), 'utf8');
  let definition;
  const posted = [], requests = [], disposers = [];
  const parent = {postMessage: (message, origin) => posted.push({message, origin})};
  const window = Object.assign(new EventTarget(), {parent, location: {ancestorOrigins: ['http://wails.localhost']}, __ModuleLoader__: {load(value) { definition = value; }}});
  const document = Object.assign(new EventTarget(), {visibilityState: 'visible', focused: true, hasFocus() { return this.focused; }});
  const original = {window: globalThis.window, document: globalThis.document, fetch: globalThis.fetch, loader: globalThis.__ModuleLoader__};
  globalThis.window = window;
  globalThis.document = document;
  globalThis.fetch = async (input, init) => {
    const request = {input, init, body: JSON.parse(init.body)};
    requests.push(request);
    return fetchImpl(request, requests.length);
  };
  globalThis.__ModuleLoader__ = window.__ModuleLoader__;
  new Function(source)();
  const clock = new FakeClock();
  let list = {current: 'session-1', byId: {'session-1': {id: 'session-1', displayTitle: 'A task', parentId: ''}}};
  const subscribers = new Set();
  const opened = [];
  const ctx = {
    sessions: {
      list: {getSnapshot: () => list, subscribe(listener) { subscribers.add(listener); return () => subscribers.delete(listener); }},
      refresh: async () => {}, refreshSubagents: async parentSessionId => opened.push(['refreshSubagents', parentSessionId]),
      subagentAddress: sessionId => sessionId === 'session-2' ? '/sub/2' : '',
      open: async sessionId => { if (sessionId === 'missing') throw new Error('unknown session'); opened.push(['open', sessionId]); },
      openSubagent: async address => opened.push(['openSubagent', address]),
    },
    effect(effect) { const cleanup = effect(); if (cleanup) disposers.push(cleanup); },
  };
  definition.factory(() => undefined).apply(ctx, clock);
  return {
    clock, document, window, parent, posted, requests, opened,
    setList(value, notify = true) { list = value; if (notify) for (const listener of subscribers) listener(); },
    dispose() { for (const cleanup of disposers) cleanup(); Object.assign(globalThis, {window: original.window, document: original.document, fetch: original.fetch, __ModuleLoader__: original.loader}); },
  };
}

test('pet client posts only session-list changes and ignores focus changes', async () => {
  const client = await mountPetClient(async () => ({ok: true}));
  try {
    assert.deepEqual(client.posted, [{message: {version: 1, type: 'dsh-work/component-ready', id: 'pet'}, origin: 'http://wails.localhost'}]);
    await client.clock.tick(100);
    assert.equal(client.requests.length, 1);
    assert.deepEqual(client.requests[0].body, {sessions: [{id: 'session-1', title: 'A task', parentId: ''}]});

    client.document.visibilityState = 'hidden';
    client.document.dispatch('visibilitychange');
    client.window.dispatch('blur');
    client.document.visibilityState = 'visible';
    client.window.dispatch('focus');
    await client.clock.tick(100);
    assert.equal(client.requests.length, 1);

    const latest = {current: 'session-2', byId: {'session-2': {id: 'session-2', displayTitle: 'Newest', parentId: 'parent-1'}}};
    client.setList(latest);
    await client.clock.tick(99);
    assert.equal(client.requests.length, 1);
    await client.clock.tick(1);
    assert.equal(client.requests.length, 2);
    assert.equal(client.requests[0].input, '/__dshwork/pet-activity-client');
    assert.equal(client.requests[0].init.method, 'POST');
    assert.deepEqual(client.requests[1].body, {sessions: [{id: 'session-2', title: 'Newest', parentId: 'parent-1'}]});
    assert.equal('current' in client.requests[1].body, false);
    assert.equal('visible' in client.requests[1].body, false);

    client.document.dispatch('visibilitychange');
    client.window.dispatch('blur');
    client.window.dispatch('focus');
    await client.clock.tick(100);
    assert.equal(client.requests.length, 2);
  } finally { client.dispose(); }
});

test('pet client retries the latest snapshot after one second, at most three times', async () => {
  const client = await mountPetClient(async () => ({ok: false}));
  try {
    await client.clock.tick(100);
    assert.equal(client.requests.length, 1);
    client.setList({current: 'latest', byId: {'latest': {id: 'latest', displayTitle: 'Latest', parentId: ''}}}, false);
    await client.clock.tick(999);
    assert.equal(client.requests.length, 1);
    await client.clock.tick(1);
    assert.equal(client.requests.length, 2);
    assert.equal(client.requests[1].body.sessions[0].id, 'latest');
    await client.clock.tick(1000);
    await client.clock.tick(1000);
    assert.equal(client.requests.length, 4);
    await client.clock.tick(10000);
    assert.equal(client.requests.length, 4);
  } finally { client.dispose(); }
});

test('pet client opens only a trusted parent message and reports child-open results', async () => {
  const client = await mountPetClient(async () => ({ok: true}));
  try {
    const message = {version: 1, type: 'dsh-work/open-session', id: '42', sessionId: 'session-2', parentSessionId: 'parent-1'};
    client.window.dispatch('message', {source: {}, origin: 'http://wails.localhost', data: message});
    client.window.dispatch('message', {source: client.parent, origin: 'https://attacker.test', data: message});
    await settle();
    assert.deepEqual(client.opened, []);

    client.window.dispatch('message', {source: client.parent, origin: 'http://wails.localhost', data: message});
    await settle();
    assert.deepEqual(client.opened, [['refreshSubagents', 'parent-1'], ['openSubagent', '/sub/2']]);
    assert.deepEqual(client.posted.at(-1), {message: {version: 1, type: 'dsh-work/open-session-result', id: '42', error: ''}, origin: 'http://wails.localhost'});

    client.window.dispatch('message', {source: client.parent, origin: 'http://wails.localhost', data: {...message, id: '43', sessionId: 'missing'}});
    await settle();
    assert.deepEqual(client.posted.at(-1), {message: {version: 1, type: 'dsh-work/open-session-result', id: '43', error: 'Conversation is unavailable'}, origin: 'http://wails.localhost'});
  } finally { client.dispose(); }
});
