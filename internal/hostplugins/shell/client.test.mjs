import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import test from 'node:test';

const SHELL = 'http://wails.localhost';

// Load the DSH client plugin framed by a fake shell, with a fake shortcut catalog.
async function framed(entries, {status = 'ready', fixed = [], handles = () => true, publicServices = true, sidebarCollapsed = false, servicesPresent = true} = {}) {
  const source = await readFile(new URL('./client.js', import.meta.url), 'utf8');
  let definition;
  const listeners = {};
  const posted = [];
  const dispatched = [];
  const publicCalls = [];
  const observers = [];
  let collapsed = sidebarCollapsed;
  const layoutFrame = {
    style: {gridTemplateColumns: '280px minmax(400px, 1fr) minmax(0px, 0px)'},
    hasAttribute: name => name === 'data-sidebar-collapsed' && collapsed,
  };
  const root = {querySelector: selector => selector === '[style*="grid-template-columns"]' ? layoutFrame : null};
  globalThis.MutationObserver = class {
    constructor(callback) { this.callback = callback; observers.push(this); }
    observe() { this.callback(); }
    disconnect() {}
  };
  const parent = {postMessage: (message, origin) => posted.push({message, origin})};
  globalThis.KeyboardEvent = class { constructor(type, init) { Object.assign(this, init, {type}); } };
  globalThis.window = {
    parent,
    location: {ancestorOrigins: [SHELL]},
    __ModuleLoader__: {load: value => { definition = value; }},
    addEventListener: (type, fn) => { (listeners[type] ??= []).push(fn); },
    removeEventListener: (type, fn) => { listeners[type] = (listeners[type] ?? []).filter(x => x !== fn); },
  };
  globalThis.fetch = () => new Promise(() => {});
  globalThis.document = {
    visibilityState: 'visible',
    hasFocus: () => true,
    body: {dispatchEvent: event => {
      dispatched.push(event);
      if (handles(event)) event.defaultPrevented = true;
      return !event.defaultPrevented;
    }},
    documentElement: {},
    getElementById: id => id === 'root' ? root : null,
    querySelector: selector => root.querySelector(selector),
  };
  new Function(source)();
  let snapshot = entries;
  let notify;
  const uiWorkspace = publicServices ? {startSession: () => publicCalls.push('session.new')} : {};
  const layout = publicServices ? {toggleSidebar: () => {
    publicCalls.push('sidebar.left.toggle');
    collapsed = !collapsed;
    observers.forEach(observer => observer.callback());
  }} : {};
  const ctx = {
    sessions: {list: {getSnapshot: () => ({byId: {}})}},
    effect: fn => { fn(); },
    // Like Cordis, a callback runs only once every dependency is available.
    inject(deps, callback) { if (deps.every(dep => ctx[dep] !== undefined)) callback(ctx); },
    shortcuts: {
      catalog: {getSnapshot: () => snapshot, subscribe: fn => { notify = fn; return () => {}; }},
      config: {getSnapshot: () => ({status}), subscribe: () => () => {}},
      fixedCatalog: {getSnapshot: () => fixed, subscribe: () => () => {}},
    },
    uiWorkspace: servicesPresent ? uiWorkspace : undefined,
    layout: servicesPresent ? layout : undefined,
  };
  definition.factory(() => undefined).apply(ctx);
  const emit = (type, event) => (listeners[type] ?? []).forEach(fn => fn(event));
  return {posted, dispatched, publicCalls, emit, parent, update(next) { snapshot = next; notify(); }};
}
const entry = (id, binding, keys = [], extra = {}) => ({id, label: id, keys, binding, conflicts: [], issue: null, ...extra});
const messages = (posted, type) => posted.filter(p => p.message.type === type).map(p => p.message);
const catalogs = posted => posted.filter(p => p.message.type === 'dsh-work/catalog').map(p => p.message.commands);

test('reports its built-in component handshake to the shell', async () => {
  const shell = await framed([]);
  assert.deepEqual(messages(shell.posted, 'dsh-work/component-ready'), [{version: 1, type: 'dsh-work/component-ready', id: 'shell'}]);
});

test('reports only the menu commands with their current binding', async () => {
  const shell = await framed([
    entry('session.new', {code: 'KeyN', modifiers: ['control', 'alt']}, ['Ctrl', 'Alt', 'N']),
    entry('terminal.new', null),
    entry('plugin.private', {code: 'KeyP', modifiers: ['control']}, ['Ctrl', 'P']),
  ]);
  assert.equal(shell.posted[0].origin, SHELL);
  assert.deepEqual(catalogs(shell.posted).at(-1), [
    {id: 'session.new', keys: ['Ctrl', 'Alt', 'N'], state: 'ready'},
    {id: 'terminal.new', keys: [], state: 'unbound'},
    {id: 'sidebar.left.toggle', keys: [], state: 'ready'},
  ]);
  shell.update([entry('session.new', null)]);
  assert.deepEqual(catalogs(shell.posted).at(-1), [
    {id: 'session.new', keys: [], state: 'ready'},
    {id: 'sidebar.left.toggle', keys: [], state: 'ready'},
  ]);
});

test('reports bindings the menu cannot run as unavailable', async () => {
  const bound = {code: 'KeyN', modifiers: ['control']};
  const shell = await framed([
    entry('session.new', bound, ['Ctrl', 'N'], {conflicts: ['plugin.other']}),
    entry('terminal.new', bound, ['Ctrl', 'N'], {issue: 'reserved'}),
    entry('browser.new', {code: 'KeyK', modifiers: ['control'], secondCode: 'KeyB'}, ['Ctrl', 'K', 'B']),
  ]);
  assert.deepEqual(catalogs(shell.posted).at(-1).map(command => command.state), ['ready', 'unavailable', 'unavailable', 'ready']);
  const loading = await framed([entry('session.new', bound, ['Ctrl', 'N'])], {status: 'loading', publicServices: false});
  assert.deepEqual(catalogs(loading.posted).at(-1), [
    {id: 'session.new', keys: [], state: 'unavailable'},
    {id: 'sidebar.left.toggle', keys: [], state: 'unavailable'},
  ]);
});

test('tells the shell whether DSH took the command', async () => {
  const shell = await framed([
    entry('session.new', {code: 'KeyN', modifiers: ['control']}),
    entry('terminal.new', {code: 'Backquote', modifiers: ['control']}),
    entry('browser.new', null),
  ], {handles: event => event.code === 'KeyN'});
  for (const id of ['session.new', 'terminal.new', 'browser.new']) {
    shell.emit('message', {source: shell.parent, origin: SHELL, data: {version: 1, type: 'dsh-work/command', id}});
  }
  assert.deepEqual(messages(shell.posted, 'dsh-work/command-result').map(({id, handled}) => [id, handled]), [
    ['session.new', true], ['terminal.new', false], ['browser.new', false],
  ]);
  assert.equal(shell.dispatched.length, 1);
});

test('forwards window zoom and full-screen keys that DSH does not use', async () => {
  const shell = await framed([entry('plugin.zoom', {code: 'Digit0', modifiers: ['control']})], {
    fixed: [{id: 'fixed.fullscreen', bindings: [{code: 'F12', modifiers: []}]}],
  });
  const press = init => {
    const event = {key: '', ctrlKey: false, altKey: false, shiftKey: false, metaKey: false, defaultPrevented: false, ...init};
    event.preventDefault = () => { event.defaultPrevented = true; };
    shell.emit('keydown', event);
    return event.defaultPrevented;
  };
  assert.equal(press({code: 'Equal', ctrlKey: true}), true);
  assert.equal(press({code: 'Equal', ctrlKey: true, shiftKey: true}), true);
  assert.equal(press({code: 'NumpadSubtract', ctrlKey: true}), true);
  assert.equal(press({code: 'F11', key: 'F11'}), true);
  assert.equal(press({code: 'Digit0', ctrlKey: true}), false, 'DSH binds Ctrl+0');
  assert.equal(press({code: 'Equal', ctrlKey: true, altKey: true}), false);
  assert.equal(press({code: 'Equal', ctrlKey: true, defaultPrevented: true}), true);
  assert.deepEqual(messages(shell.posted, 'dsh-work/window-key').map(message => message.action), ['zoom-in', 'zoom-in', 'zoom-out', 'fullscreen']);
});

test('runs a command from the shell by dispatching its binding', async () => {
  const shell = await framed([entry('terminal.new', {code: 'Backquote', modifiers: ['control', 'shift']}, ['Ctrl', 'Shift', 'Backquote'])]);
  shell.emit('message', {source: shell.parent, origin: SHELL, data: {version: 1, type: 'dsh-work/command', id: 'terminal.new'}});
  assert.equal(shell.dispatched.length, 1);
  const event = shell.dispatched[0];
  assert.equal(event.type, 'keydown');
  assert.equal(event.code, 'Backquote');
  assert.equal(event.key, 'Backquote');
  assert.equal(event.ctrlKey, true);
  assert.equal(event.shiftKey, true);
  assert.equal(event.altKey, false);
  assert.equal(event.bubbles, true);
});

test('uses public Workspace and Layout services and reports the live sidebar state', async () => {
  const shell = await framed([], {sidebarCollapsed: true});
  assert.deepEqual(catalogs(shell.posted).at(-1), [
    {id: 'session.new', keys: [], state: 'ready'},
    {id: 'sidebar.left.toggle', keys: [], state: 'ready'},
  ]);
  assert.deepEqual(messages(shell.posted, 'dsh-work/sidebar-state').map(message => message.open), [false]);
  for (const id of ['session.new', 'sidebar.left.toggle']) {
    shell.emit('message', {source: shell.parent, origin: SHELL, data: {version: 1, type: 'dsh-work/command', id}});
  }
  assert.deepEqual(shell.publicCalls, ['session.new', 'sidebar.left.toggle']);
  assert.equal(shell.dispatched.length, 0);
  assert.deepEqual(messages(shell.posted, 'dsh-work/command-result').map(({id, handled}) => [id, handled]), [
    ['session.new', true], ['sidebar.left.toggle', true],
  ]);
  assert.deepEqual(messages(shell.posted, 'dsh-work/sidebar-state').map(message => message.open), [false, true]);
});
test('ignores commands from other origins, other windows or outside the menu', async () => {
  const shell = await framed([
    entry('session.new', {code: 'KeyN', modifiers: ['control']}),
    entry('plugin.private', {code: 'KeyP', modifiers: ['control']}),
  ]);
  shell.emit('message', {source: shell.parent, origin: 'http://evil.localhost', data: {type: 'dsh-work/command', id: 'session.new'}});
  shell.emit('message', {source: {}, origin: SHELL, data: {type: 'dsh-work/command', id: 'session.new'}});
  shell.emit('message', {source: shell.parent, origin: SHELL, data: {type: 'dsh-work/command', id: 'plugin.private'}});
  assert.equal(shell.dispatched.length, 0);
});

test('forwards Alt pressed alone and F10, but not Alt chords', async () => {
  const shell = await framed([]);
  const keys = () => shell.posted.filter(p => p.message.type === 'dsh-work/menu-key').map(p => p.message.key);
  let prevented = false;
  shell.emit('keydown', {key: 'F10', preventDefault: () => { prevented = true; }});
  assert.deepEqual(keys(), ['F10']);
  assert.equal(prevented, true);
  shell.emit('keydown', {key: 'Alt', repeat: false});
  shell.emit('keyup', {key: 'Alt'});
  assert.deepEqual(keys(), ['F10', 'Alt']);
  shell.emit('keydown', {key: 'Alt', repeat: false});
  shell.emit('keydown', {key: 'n', altKey: true});
  shell.emit('keyup', {key: 'Alt'});
  assert.deepEqual(keys(), ['F10', 'Alt']);
});

test('keeps the menu bridge when a profile lacks the workspace or layout service', async () => {
  const shell = await framed([entry('terminal.new', {code: 'Backquote', modifiers: ['control']}, ['Ctrl', '`'])], {servicesPresent: false});
  const commands = new Map(catalogs(shell.posted).at(-1).map(command => [command.id, command.state]));
  assert.equal(commands.get('terminal.new'), 'ready');
  assert.equal(commands.get('session.new'), 'unavailable');
  assert.equal(commands.get('sidebar.left.toggle'), 'unavailable');
  assert.deepEqual(messages(shell.posted, 'dsh-work/component-ready').map(message => message.id), ['shell']);
});
