import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import test from 'node:test';

// Load the injected DSH client plugin with a fake module loader and Remote
// account stream, then drive the official account/watch values through it.
async function loadPlugin() {
  const source = await readFile(new URL('./client.js', import.meta.url), 'utf8');
  let definition;
  const opened = [];
  const posted = [];
  const children = [];
  const body = {children, appendChild(element) { children.push(element); element.isConnected = true; }};
  const document = {
    visibilityState: 'visible',
    hasFocus: () => true,
    body,
    documentElement: {lang: 'zh-CN'},
    createElement: () => ({style: {}, attributes: {}, setAttribute(name, value) { this.attributes[name] = value; }}),
  };
  globalThis.window = {
    __ModuleLoader__: {load: value => { definition = value; }},
    parent: {postMessage: (message, origin) => posted.push({message, origin})},
    location: {ancestorOrigins: ['http://wails.localhost']},
    open: (url, target, features) => { opened.push({url, target, features}); return null; },
    setTimeout: () => 1,
    clearTimeout: () => {},
  };
  globalThis.dshDesktop = {};
  globalThis.fetch = () => new Promise(() => {});
  globalThis.document = document;
  new Function(source)();
  return {plugin: definition.factory(() => undefined), opened, posted, document};
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

function context(stream, accountOverrides = {}) {
  const disposers = [];
  const account = {watch: signal => ({signal}), ...accountOverrides};
  const ctx = {
    sessions: {list: {getSnapshot: () => ({byId: {}})}},
    effect: fn => { disposers.push(fn()); },
    inject(deps, callback) {
      assert.deepEqual(deps, ['remote', 'remote.account']);
      callback(ctx);
    },
    remote: {
      account,
      $stream: options => { stream.open = options.open; return stream; },
    },
  };
  return {ctx, account, dispose: () => { for (const dispose of disposers) dispose?.(); }};
}

const tick = () => new Promise(resolve => setTimeout(resolve, 0));

test('reports the account component handshake to the framed shell', async () => {
  const {plugin, posted} = await loadPlugin();
  const stream = accountStream();
  const {ctx, dispose} = context(stream);
  plugin.apply(ctx);
  try {
    assert.deepEqual(posted, [{message: {version: 1, type: 'dsh-work/component-ready', id: 'account'}, origin: 'http://wails.localhost'}]);
  } finally { dispose(); }
});

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

const signOutStates = posted => posted.filter(p => p.message.type === 'dsh-work/account-signout').map(p => p.message.state);

test('reports account sign-out progress and success to the shell, then restores the Remote method', async () => {
  const {plugin, posted} = await loadPlugin();
  const stream = accountStream();
  const signOut = async () => new Response(JSON.stringify({type: 'server-response', result: {ok: true}}));
  const {ctx, account, dispose} = context(stream, {signOut});
  plugin.apply(ctx);
  try {
    const pending = account.signOut();
    assert.deepEqual(signOutStates(posted), ['pending']);
    const result = await pending;
    assert.equal(result.ok, true);
    await tick();
    assert.deepEqual(signOutStates(posted), ['pending', 'success']);
    dispose();
    assert.equal(account.signOut, signOut);
  } finally {
    dispose();
  }
});

test('reports sign-out failure to the shell when the Remote method rejects', async () => {
  const {plugin, posted} = await loadPlugin();
  const stream = accountStream();
  const {ctx, account, dispose} = context(stream, {signOut: async () => { throw new Error('network'); }});
  plugin.apply(ctx);
  try {
    await assert.rejects(account.signOut(), /network/);
    await tick();
    assert.deepEqual(signOutStates(posted), ['pending', 'error']);
  } finally {
    dispose();
  }
});
