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

async function mountPetClient(fetchImpl, initialStatuses = new Map()) {
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
  let list = {byId: {'session-1': {id: 'session-1', displayTitle: 'A task', parentId: '', retainedBy: {mainView: 1}}}};
  const subscribers = new Set();
  const statusSubscribers = new Set();
  let statusSnapshot = initialStatuses;
  const opened = [];
  const ctx = {
    sessions: {
      list: {getSnapshot: () => list, subscribe(listener) { subscribers.add(listener); return () => subscribers.delete(listener); }},
      refresh: async () => opened.push(['refresh']),
    },
    uiWorkspace: {openSession: sessionId => { opened.push(['openSession', sessionId]); if (sessionId === 'throws') throw new Error('unknown session'); }},
    uiSession: {sessionStatus: {
      getSnapshot: () => statusSnapshot,
      subscribe(listener) { statusSubscribers.add(listener); return () => statusSubscribers.delete(listener); },
    }},
    effect(effect) { const cleanup = effect(); if (cleanup) disposers.push(cleanup); },
  };
  definition.factory(() => undefined).apply(ctx, clock);
  return {
    clock, document, window, parent, posted, requests, opened,
    setList(value, notify = true) { list = value; if (notify) for (const listener of subscribers) listener(); },
    setStatuses(value, notify = true) { statusSnapshot = value; if (notify) for (const listener of statusSubscribers) listener(); },
    dispose() { for (const cleanup of disposers) cleanup(); Object.assign(globalThis, {window: original.window, document: original.document, fetch: original.fetch, __ModuleLoader__: original.loader}); },
  };
}

function interactionPosts(client) {
  return client.posted.map(item => item.message).filter(message => message.type === 'dsh-work/interaction');
}

test('pet client reports the retained main-view session and ignores focus changes', async () => {
  const client = await mountPetClient(async () => ({ok: true}));
  try {
    assert.deepEqual(client.posted[0], {message: {version: 1, type: 'dsh-work/component-ready', id: 'pet'}, origin: 'http://wails.localhost'});
    const initialInteractionReport = interactionPosts(client);
    assert.equal(initialInteractionReport.length, 1);
    assert.equal(typeof initialInteractionReport[0].instance, 'string');
    assert.ok(initialInteractionReport[0].instance.length > 0);
    assert.deepEqual(initialInteractionReport[0], {version: 1, type: 'dsh-work/interaction', instance: initialInteractionReport[0].instance,
      sessionId: '', kind: '', key: '', toolName: '', current: 'session-1', visible: true});
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

    const latest = {byId: {'session-2': {id: 'session-2', displayTitle: 'Newest', parentId: 'parent-1', retainedBy: {mainView: 1}}}};
    client.setList(latest);
    const currentReports = interactionPosts(client).filter(message => message.sessionId === '');
    assert.equal(currentReports.at(-1).current, 'session-2');
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

test('pet client treats the first pending-interaction snapshot as baseline', async () => {
  const baseline = new Map([['session-1', {pendingInteraction: {kind: 'approval', key: 'old', sessionId: 'session-1', toolName: 'shell.exec'}}]]);
  const client = await mountPetClient(async () => ({ok: true}), baseline);
  try {
    const reports = interactionPosts(client);
    assert.equal(reports.length, 1);
    assert.equal(reports[0].sessionId, '');
    assert.equal(reports[0].kind, '');
    assert.equal(reports[0].current, 'session-1');
    assert.equal(reports[0].visible, true);
    await client.clock.tick(250);
    assert.equal(interactionPosts(client).length, 1);
  } finally { client.dispose(); }
});

test('pet client waits 250 ms and reports only the latest key for a session', async () => {
  const client = await mountPetClient(async () => ({ok: true}));
  try {
    const pending = key => new Map([['session-1', {pendingInteraction: {kind: 'approval', key, sessionId: 'session-1', toolName: 'shell.exec'}}]]);
    client.setStatuses(pending('first'));
    await client.clock.tick(249);
    client.setStatuses(pending('last'));
    await client.clock.tick(249);
    assert.equal(interactionPosts(client).filter(message => message.sessionId).length, 0);
    await client.clock.tick(1);
    const reports = interactionPosts(client).filter(message => message.sessionId);
    assert.deepEqual(reports.map(({key}) => key), ['last']);
    assert.equal(reports[0].toolName, 'shell.exec');
    assert.equal(client.requests.length, 1);
    assert.deepEqual(Object.keys(client.requests[0].body), ['sessions']);
  } finally { client.dispose(); }
});

test('pet client reports a disappeared interaction immediately', async () => {
  const client = await mountPetClient(async () => ({ok: true}));
  try {
    client.setStatuses(new Map([['session-1', {pendingInteraction: {kind: 'question', key: 'question:1', sessionId: 'session-1'}}]]));
    await client.clock.tick(250);
    client.setStatuses(new Map());
    const reports = interactionPosts(client).filter(message => message.sessionId);
    assert.deepEqual(reports.map(({kind, key}) => [kind, key]), [['question', 'question:1'], ['', '']]);
  } finally { client.dispose(); }
});

test('pet client reports current and visibility changes without sending Worker requests', async () => {
  const client = await mountPetClient(async () => ({ok: true}));
  try {
    client.setList({byId: {'session-2': {id: 'session-2', displayTitle: 'Second', parentId: '', retainedBy: {mainView: 1}}}});
    const afterCurrent = interactionPosts(client);
    assert.equal(afterCurrent.length, 2);
    assert.equal(afterCurrent[1].sessionId, '');
    assert.equal(afterCurrent[1].current, 'session-2');

    client.document.visibilityState = 'hidden';
    client.document.dispatch('visibilitychange');
    const afterVisibility = interactionPosts(client);
    assert.equal(afterVisibility.length, 3);
    assert.equal(afterVisibility[2].sessionId, '');
    assert.equal(afterVisibility[2].visible, false);
    assert.equal(client.requests.length, 0);
  } finally { client.dispose(); }
});

test('pet client reports an empty current session when no session is retained by the main view', async () => {
  const client = await mountPetClient(async () => ({ok: true}));
  try {
    client.setList({byId: {
      'session-1': {id: 'session-1', displayTitle: 'Background task', parentId: '', retainedBy: {mainView: 0}},
    }});
    const currentReports = interactionPosts(client).filter(message => message.sessionId === '');
    assert.equal(currentReports.length, 2);
    assert.equal(currentReports.at(-1).current, '');
  } finally { client.dispose(); }
});

test('pet client reports only approval, question, and plan-review interactions', async () => {
  const client = await mountPetClient(async () => ({ok: true}));
  try {
    client.setStatuses(new Map([
      ['approval-session', {pendingInteraction: {kind: 'approval', key: 'approval:1', sessionId: 'approval-session', toolName: 'shell.exec'}}],
      ['question-session', {pendingInteraction: {kind: 'question', key: 'question:1', sessionId: 'question-session'}}],
      ['plan-session', {pendingInteraction: {kind: 'plan-review', key: 'plan:1', sessionId: 'plan-session'}}],
      ['other-session', {pendingInteraction: {kind: 'completion', key: 'completion:1', sessionId: 'other-session'}}],
    ]));
    await client.clock.tick(250);
    const reports = interactionPosts(client).filter(message => message.sessionId);
    assert.deepEqual(reports.map(({kind, sessionId}) => [kind, sessionId]).sort(), [
      ['approval', 'approval-session'], ['plan-review', 'plan-session'], ['question', 'question-session'],
    ]);
    assert.equal(reports.find(message => message.kind === 'approval').toolName, 'shell.exec');
    assert.equal(reports.find(message => message.kind === 'question').toolName, '');
    assert.equal(reports.find(message => message.kind === 'plan-review').toolName, '');
  } finally { client.dispose(); }
});

test('pet client resets its page instance when disposed', async () => {
  const client = await mountPetClient(async () => ({ok: true}));
  const instance = interactionPosts(client)[0].instance;
  client.dispose();
  assert.deepEqual(client.posted.at(-1), {message: {version: 1, type: 'dsh-work/interaction-reset', instance}, origin: 'http://wails.localhost'});
});

test('pet client retries the latest snapshot after one second, at most three times', async () => {
  const client = await mountPetClient(async () => ({ok: false}));
  try {
    await client.clock.tick(100);
    assert.equal(client.requests.length, 1);
    client.setList({byId: {'latest': {id: 'latest', displayTitle: 'Latest', parentId: '', retainedBy: {mainView: 1}}}}, false);
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
    client.setList({byId: {'session-2': {id: 'session-2'}}}, false);
    const message = {version: 1, type: 'dsh-work/open-session', id: '42', sessionId: 'session-2', parentSessionId: 'parent-1'};
    client.window.dispatch('message', {source: {}, origin: 'http://wails.localhost', data: message});
    client.window.dispatch('message', {source: client.parent, origin: 'https://attacker.test', data: message});
    await settle();
    assert.deepEqual(client.opened, []);

    client.window.dispatch('message', {source: client.parent, origin: 'http://wails.localhost', data: message});
    await settle();
    assert.deepEqual(client.opened, [['refresh'], ['openSession', 'session-2']]);
    assert.deepEqual(client.posted.at(-1), {message: {version: 1, type: 'dsh-work/open-session-result', id: '42', error: ''}, origin: 'http://wails.localhost'});

    client.opened.length = 0;
    client.window.dispatch('message', {source: client.parent, origin: 'http://wails.localhost', data: {...message, id: '43', sessionId: 'missing'}});
    await settle();
    assert.deepEqual(client.opened, [['refresh']]);
    assert.deepEqual(client.posted.at(-1), {message: {version: 1, type: 'dsh-work/open-session-result', id: '43', error: 'Conversation is unavailable'}, origin: 'http://wails.localhost'});

    client.setList({byId: {throws: {id: 'throws'}}}, false);
    client.opened.length = 0;
    client.window.dispatch('message', {source: client.parent, origin: 'http://wails.localhost', data: {...message, id: '44', sessionId: 'throws'}});
    await settle();
    assert.deepEqual(client.opened, [['refresh'], ['openSession', 'throws']]);
    assert.deepEqual(client.posted.at(-1), {message: {version: 1, type: 'dsh-work/open-session-result', id: '44', error: 'Conversation is unavailable'}, origin: 'http://wails.localhost'});
  } finally { client.dispose(); }
});


test('pet client answers only the matching answerable approval from the trusted shell', async () => {
  const client = await mountPetClient(async () => ({ok: true}));
  try {
    const instance = interactionPosts(client)[0].instance;
    const answerCalls = [];
    const dispatch = async (message, source = client.parent, origin = 'http://wails.localhost') => {
      client.window.dispatch('message', {source, origin, data: message});
      await settle();
    };
    const message = overrides => ({version: 1, type: 'dsh-work/answer-approval', instance, sessionId: 'session-1', key: 'approval:1', outcome: 'allowed-once', ...overrides});
    const pending = overrides => ({kind: 'approval', key: 'approval:1', answerable: true, answer: outcome => { answerCalls.push(outcome); return Promise.resolve(); }, ...overrides});

    client.setStatuses(new Map([['session-1', {pendingInteraction: pending()}]]), false);
    await dispatch(message({instance: 'another-page'}));
    await dispatch(message(), {}, 'http://wails.localhost');
    await dispatch(message(), client.parent, 'https://attacker.test');
    await dispatch(message({outcome: 'allow'}));
    client.setStatuses(new Map([['session-1', {pendingInteraction: pending({kind: 'question'})}]]), false);
    await dispatch(message());
    client.setStatuses(new Map([['session-1', {pendingInteraction: pending({key: 'approval:2'})}]]), false);
    await dispatch(message());
    client.setStatuses(new Map([['session-1', {pendingInteraction: pending({answerable: false})}]]), false);
    await dispatch(message());
    assert.deepEqual(answerCalls, []);

    client.setStatuses(new Map([['session-1', {pendingInteraction: pending()}]]), false);
    await dispatch(message());
    assert.deepEqual(answerCalls, ['allowed-once']);
  } finally { client.dispose(); }
});

test('pet client swallows a rejected approval answer', async () => {
  const client = await mountPetClient(async () => ({ok: true}));
  try {
    const instance = interactionPosts(client)[0].instance;
    let calls = 0;
    client.setStatuses(new Map([['session-1', {pendingInteraction: {
      kind: 'approval', key: 'approval:1', answerable: true,
      answer: async () => { calls++; throw new Error('already answered'); },
    }}]]), false);
    client.window.dispatch('message', {source: client.parent, origin: 'http://wails.localhost', data: {
      version: 1, type: 'dsh-work/answer-approval', instance, sessionId: 'session-1', key: 'approval:1', outcome: 'rejected',
    }});
    await settle();
    assert.equal(calls, 1);
  } finally { client.dispose(); }
});
