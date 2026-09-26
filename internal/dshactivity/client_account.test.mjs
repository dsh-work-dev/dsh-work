import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import test from 'node:test';

// Load the injected DSH client plugin with a fake module loader and Remote
// account stream, then drive the official account/watch values through it.
async function loadPlugin() {
  const source = await readFile(new URL('./plugin/client.js', import.meta.url), 'utf8');
  let definition;
  const opened = [];
  globalThis.window = {
    __ModuleLoader__: {load: value => { definition = value; }},
    open: (url, target, features) => { opened.push({url, target, features}); return null; },
  };
  globalThis.dshDesktop = {};
  globalThis.fetch = () => new Promise(() => {});
  globalThis.document = {visibilityState: 'visible', hasFocus: () => true};
  new Function(source)();
  return {plugin: definition.factory(() => undefined), opened};
}

function accountStream() {
  const values = [];
  let wake;
  let disposed = false;
  const stream = {
    push(value) { values.push(value); wake?.(); },
    dispose() { disposed = true; wake?.(); },
    async *[Symbol.asyncIterator]() {
      for (;;) {
        while (!values.length && !disposed) await new Promise(resolve => { wake = resolve; });
        if (disposed) return;
        const value = values.shift();
        yield {value, accept() {}};
      }
    },
  };
  return stream;
}

function context(stream) {
  const disposers = [];
  const ctx = {
    sessions: {list: {getSnapshot: () => ({byId: {}})}},
    effect: fn => { disposers.push(fn()); },
    inject(deps, callback) {
      assert.deepEqual(deps, ['remote', 'remote.account']);
      callback(ctx);
    },
    remote: {
      account: {watch: signal => ({signal})},
      $stream: options => { stream.open = options.open; return stream; },
    },
  };
  return {ctx, dispose: () => { for (const dispose of disposers) dispose?.(); }};
}

const tick = () => new Promise(resolve => setTimeout(resolve, 0));

test('opens the DSH account authorization URL once when sign-in waits for the browser', async () => {
  const {plugin, opened} = await loadPlugin();
  const stream = accountStream();
  const {ctx, dispose} = context(stream);
  plugin.apply(ctx);
  try {
    stream.push({status: 'signed-out'});
    stream.push({status: 'signed-out', attempt: {id: 'a1', phase: 'initializing'}});
    const authorizeUrl = 'https://platform.deepseek.com/dsh/authorize?authorize_id=1';
    stream.push({status: 'signed-out', attempt: {id: 'a1', phase: 'waiting-browser', authorizeUrl}});
    stream.push({status: 'signed-out', attempt: {id: 'a1', phase: 'waiting-browser', authorizeUrl}});
    await tick();
    assert.deepEqual(opened.map(item => item.url), [authorizeUrl]);
  } finally {
    dispose();
  }
});

test('does not reopen an attempt that was already waiting when the page connected', async () => {
  const {plugin, opened} = await loadPlugin();
  const stream = accountStream();
  const {ctx, dispose} = context(stream);
  plugin.apply(ctx);
  try {
    stream.push({status: 'signed-out', attempt: {id: 'old', phase: 'waiting-browser', authorizeUrl: 'https://platform.deepseek.com/dsh/authorize?authorize_id=old'}});
    stream.push({status: 'signed-out', attempt: {id: 'new', phase: 'waiting-browser', authorizeUrl: 'http://127.0.0.1:1/dsh/authorize'}});
    await tick();
    assert.deepEqual(opened, []);
  } finally {
    dispose();
  }
});
