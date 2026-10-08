import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import test from 'node:test';

const SHELL = 'http://wails.localhost';

// Load the DSH client plugin framed by a fake shell, with a fake shortcut catalog.
async function framed(entries) {
  const source = await readFile(new URL('./plugin/client.js', import.meta.url), 'utf8');
  let definition;
  const listeners = {};
  const posted = [];
  const dispatched = [];
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
  globalThis.document = {visibilityState: 'visible', hasFocus: () => true, body: {dispatchEvent: event => dispatched.push(event)}};
  new Function(source)();
  let snapshot = entries;
  let notify;
  const ctx = {
    sessions: {list: {getSnapshot: () => ({byId: {}})}},
    effect: fn => { fn(); },
    inject(deps, callback) { if (deps[0] === 'shortcuts') callback(ctx); },
    shortcuts: {catalog: {getSnapshot: () => snapshot, subscribe: fn => { notify = fn; return () => {}; }}},
  };
  definition.factory(() => undefined).apply(ctx);
  const emit = (type, event) => (listeners[type] ?? []).forEach(fn => fn(event));
  return {posted, dispatched, emit, parent, update(next) { snapshot = next; notify(); }};
}

const entry = (id, binding, keys = []) => ({id, label: id, keys, binding});
const catalogs = posted => posted.filter(p => p.message.type === 'dsh-work/catalog').map(p => p.message.commands);

test('reports only the menu commands with their current binding', async () => {
  const shell = await framed([
    entry('session.new', {code: 'KeyN', modifiers: ['control', 'alt']}, ['Ctrl', 'Alt', 'N']),
    entry('terminal.new', null),
    entry('plugin.private', {code: 'KeyP', modifiers: ['control']}, ['Ctrl', 'P']),
  ]);
  assert.equal(shell.posted[0].origin, SHELL);
  assert.deepEqual(catalogs(shell.posted).at(-1), [
    {id: 'session.new', keys: ['Ctrl', 'Alt', 'N'], bound: true},
    {id: 'terminal.new', keys: [], bound: false},
  ]);
  shell.update([entry('session.new', null)]);
  assert.deepEqual(catalogs(shell.posted).at(-1), [{id: 'session.new', keys: [], bound: false}]);
});

test('runs a command from the shell by dispatching its binding', async () => {
  const shell = await framed([entry('sidebar.left.toggle', {code: 'KeyB', modifiers: ['control', 'shift']}, ['Ctrl', 'Shift', 'B'])]);
  shell.emit('message', {source: shell.parent, origin: SHELL, data: {version: 1, type: 'dsh-work/command', id: 'sidebar.left.toggle'}});
  assert.equal(shell.dispatched.length, 1);
  const event = shell.dispatched[0];
  assert.equal(event.type, 'keydown');
  assert.equal(event.code, 'KeyB');
  assert.equal(event.key, 'b');
  assert.equal(event.ctrlKey, true);
  assert.equal(event.shiftKey, true);
  assert.equal(event.altKey, false);
  assert.equal(event.bubbles, true);
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
