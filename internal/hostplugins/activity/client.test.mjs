import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import test from 'node:test';

test('activity client reports the current DSH session to its Host route', async () => {
  const source = await readFile(new URL('./client.js', import.meta.url), 'utf8');
  let definition;
  const requests = [];
  const posted = [];
  const disposers = [];
  globalThis.window = {parent: {postMessage: (message, origin) => posted.push({message, origin})}, location: {ancestorOrigins: ['http://wails.localhost']}, __ModuleLoader__: {load(value) { definition = value; }}};
  globalThis.document = {visibilityState: 'visible', hasFocus: () => true};
  globalThis.fetch = async (input, init) => {
    requests.push({input, init});
    return {ok: true, json: async () => ({navigation: null})};
  };
  globalThis.__ModuleLoader__ = {load(value) { definition = value; }};
  new Function(source)();
  const plugin = definition.factory(() => undefined);
  const ctx = {
    sessions: {list: {getSnapshot: () => ({
      current: 'session-1',
      byId: {'session-1': {id: 'session-1', displayTitle: 'A task', parentId: ''}},
    })}},
    effect(fn) { disposers.push(fn()); },
  };
  plugin.apply(ctx);
  try {
    await new Promise(resolve => setTimeout(resolve, 0));
    assert.deepEqual(posted, [{message: {version: 1, type: 'dsh-work/component-ready', id: 'activity'}, origin: 'http://wails.localhost'}]);
    assert.equal(requests.length, 1);
    assert.equal(requests[0].input, '/__dshwork/activity-client');
    assert.equal(requests[0].init.method, 'POST');
    const body = JSON.parse(requests[0].init.body);
    assert.equal(body.current, 'session-1');
    assert.equal(body.visible, true);
    assert.deepEqual(body.sessions, [{id: 'session-1', title: 'A task', parentId: ''}]);
  } finally {
    for (const dispose of disposers) dispose?.();
  }
});