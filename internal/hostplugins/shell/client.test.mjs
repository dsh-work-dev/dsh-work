import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import test from 'node:test';

const SHELL = 'http://wails.localhost';

// Load the DSH client plugin framed by a fake shell, with a fake shortcut catalog.
async function framed(entries, {status = 'ready', fixed = [], handles = () => true, publicServices = true, sidebarCollapsed = false, servicesPresent = true, modals = [], controls = []} = {}) {
  const source = await readFile(new URL('./client.js', import.meta.url), 'utf8');
  let definition;
  const listeners = {};
  const posted = [];
  const dispatched = [];
  const publicCalls = [];
  const observers = [];
  const appended = [];
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
  globalThis.requestAnimationFrame = fn => setTimeout(fn, 0);
  globalThis.cancelAnimationFrame = id => clearTimeout(id);
  globalThis.CSS = {supports: (property, value) => property === 'color' ? /^(#[0-9a-f]{3,8}|rgba?\(.*\))$/i.test(value) : !/url\(/.test(value)};
  globalThis.getComputedStyle = element => element.computed ?? {getPropertyValue: () => ''};
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
    head: {append: element => { appended.push(element); }},
    createElement: tag => ({tagName: tag, dataset: {}, remove() { appended.splice(appended.indexOf(this), 1); }}),
    body: {attributes: {}, setAttribute(name, value) { this.attributes[name] = value; }, removeAttribute(name) { delete this.attributes[name]; }, dispatchEvent: event => {
      dispatched.push(event);
      if (handles(event)) event.defaultPrevented = true;
      return !event.defaultPrevented;
    }},
    documentElement: {},
    querySelectorAll: selector => selector === '[data-dsh-work-control]' ? controls : modals,
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
  const theme = {
    themes: [{id: 'light', colorScheme: 'light', tokens: {}}, {id: 'dark', colorScheme: 'dark', tokens: {}}],
    preference: 'system',
    register(definition) {
      this.themes.push(definition);
      return () => {
        this.themes = this.themes.filter(item => item !== definition);
        if (this.preference === definition.id) this.preference = 'system';
      };
    },
    setTheme(id) { this.preference = id; themeListeners.forEach(fn => fn({preference: id, themes: this.themes})); },
  };
  const themeListeners = [];
  const slotEntries = [];
  const ctx = {
    theme,
    on: (name, fn) => { if (name === 'theme/change') themeListeners.push(fn); },
    slots: {inject: (_, fn) => fn(), register: (options, component) => { slotEntries.push({options, component}); return () => {}; }},
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
  await new Promise(resolve => setTimeout(resolve, 0));
  const emit = (type, event) => (listeners[type] ?? []).forEach(fn => fn(event));
  return {posted, dispatched, publicCalls, emit, parent, theme, themeListeners, slotEntries, appended, update(next) { snapshot = next; notify(); }};
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

const appearance = (mode, tokens) => ({source: undefined, origin: SHELL, data: {version: 1, type: 'dsh-work/appearance', mode, tokens}});

test('paints DSH with the dsh-work appearance as a page-only theme', async () => {
  const shell = await framed([]);
  const send = (mode, tokens) => shell.emit('message', {...appearance(mode, tokens), source: shell.parent});
  send('dark', {'--dsw-alias-bg-base': '#101010', '--dsw-alias-brand-primary': 'rgb(1, 2, 3)'});
  const first = shell.theme.preference;
  assert.match(first, /^dsh-work-/);
  assert.deepEqual(shell.theme.themes.find(item => item.id === first), {id: first, colorScheme: 'dark', tokens: {'--dsw-alias-bg-base': '#101010', '--dsw-alias-brand-primary': 'rgb(1, 2, 3)'}});
  send('light', {'--dsw-alias-bg-base': '#ffffff'});
  assert.notEqual(shell.theme.preference, first);
  assert.equal(shell.theme.themes.some(item => item.id === first), false, 'the replaced theme stays registered');
  // DSH re-adopting its stored preference does not undo the dsh-work theme.
  const current = shell.theme.preference;
  shell.theme.setTheme('dark');
  assert.equal(shell.theme.preference, current);
});

test('ignores appearance values that are not DSH colour tokens', async () => {
  const shell = await framed([]);
  const send = tokens => shell.emit('message', {...appearance('dark', tokens), source: shell.parent});
  send({'--other-token': '#000000'});
  send({'--dsw-alias-bg-base': 'url(https://example.com/x.png)'});
  shell.emit('message', {...appearance('sepia', {'--dsw-alias-bg-base': '#000000'}), source: shell.parent});
  shell.emit('message', {...appearance('dark', {'--dsw-alias-bg-base': '#000000'}), source: {}});
  assert.equal(shell.theme.preference, 'system');
});

test('shadows the DSH Appearance row', async () => {
  const shell = await framed([]);
  assert.deepEqual(shell.slotEntries.map(entry => entry.options), [{name: 'settings.general.item', id: 'appearance', priority: -1}]);
  assert.equal(shell.slotEntries[0].component(), null);
});

test('reports a visible DSH modal and its mask colour', async () => {
  const mask = {getAttribute: name => name === 'aria-hidden' ? 'true' : null, className: 'x_mask', computed: {backgroundColor: 'rgba(0, 0, 0, 0.24)', backdropFilter: 'blur(4px)'}};
  const parent = {children: []};
  const modal = {parentElement: parent, getClientRects: () => [{}], computed: {display: 'block', visibility: 'visible'}};
  parent.children = [mask, modal];
  parent.parentElement = globalThis.document?.body;
  const shell = await framed([], {modals: [modal]});
  assert.deepEqual(messages(shell.posted, 'dsh-work/modal-mask'), [{version: 1, type: 'dsh-work/modal-mask', active: true, background: 'rgba(0, 0, 0, 0.24)', blur: 'blur(4px)'}]);
});

test('changes the interface font but keeps the DSH conversation font', async () => {
  const shell = await framed([]);
  globalThis.document.body.computed = {getPropertyValue: name => name === '--dsw-font-family' ? 'DSH Sans' : ''};
  shell.emit('message', {...appearance('light', {'--dsw-font-family': 'Tahoma, sans-serif', '--dsw-radius-sm': '0'}), source: shell.parent});
  const tokens = shell.theme.themes.find(item => item.id === shell.theme.preference).tokens;
  assert.equal(tokens['--dsw-font-family'], 'Tahoma, sans-serif');
  assert.equal(tokens['--dsw-radius-sm'], '0');
  assert.equal(tokens['--dsw-font-markdown-base-font-family'], 'DSH Sans');
  assert.equal(tokens['--dsw-font-markdown-h1-font-family'], 'DSH Sans');
});

test('redeclares DSH elevation from the dsh-work decoration values', async () => {
  const shell = await framed([]);
  shell.emit('message', {...appearance('light', {'--dsh-work-raised': '0 0 0 3px #111111, 6px 6px 0 #111111', '--dsh-work-floating': 'none', '--dsh-work-stroke': '0 0 0 3px #111111'}), source: shell.parent});
  assert.equal(shell.appended.length, 1);
  assert.ok(shell.appended[0].textContent.includes('--dsw-elevation-panel: var(--dsh-work-raised)'));
  assert.ok('data-dsh-work-decor' in globalThis.document.body.attributes);
  shell.emit('message', {...appearance('light', {'--dsh-work-raised': 'url(https://example.com/x)'}), source: shell.parent});
  assert.equal(shell.theme.themes.find(item => item.id === shell.theme.preference).tokens['--dsh-work-raised'], '0 0 0 3px #111111, 6px 6px 0 #111111');
});

test('removes dsh-work decoration and control marks when switching to empty tokens', async () => {
  const controls = ['button', 'field', 'select'].map(value => ({
    attributes: {'data-dsh-work-control': value},
    removeAttribute(name) { delete this.attributes[name]; },
  }));
  const shell = await framed([], {controls});
  shell.emit('message', {...appearance('light', {'--dsh-work-raised': '0 1px 2px #0003'}), source: shell.parent});
  assert.equal(shell.appended.length, 1);
  assert.equal(globalThis.document.body.attributes['data-dsh-work-decor'], '');
  assert.ok(controls.every(element => element.attributes['data-dsh-work-control']));

  shell.emit('message', {...appearance('light', {}), source: shell.parent});
  assert.equal(globalThis.document.body.attributes['data-dsh-work-decor'], undefined);
  assert.ok(controls.every(element => !('data-dsh-work-control' in element.attributes)));
  assert.equal(shell.appended.length, 0, 'the injected decoration stylesheet is removed');
  assert.deepEqual(shell.theme.themes.find(item => item.id === shell.theme.preference).tokens, {});
});

// Every CSS-module class hook is a DSH-version risk; this list changes only on purpose.
test('lists the DSH class hooks the decoration depends on', async () => {
  const shell = await framed([]);
  shell.emit('message', {...appearance('light', {'--dsh-work-raised': 'none'}), source: shell.parent});
  const hooks = [...shell.appended[0].textContent.matchAll(/\[class\$="([^"]+)"\]/g)].map(match => match[1]);
  assert.deepEqual(hooks, [], 'controls are found by role and structure, not CSS-module classes');
});

test('styles DSH switches by structure with the measured dsh-work switch', async () => {
  const shell = await framed([]);
  shell.emit('message', {...appearance('light', {'--dsh-work-raised': 'none', '--dsh-work-switch-w': '38px', '--dsh-work-switch-on-track-bg': '#111111', '--dsh-work-switch-off-thumb-radius': '999px', '--dsh-work-button-border': '3px solid #111111'}), source: shell.parent});
  const tokens = shell.theme.themes.find(item => item.id === shell.theme.preference).tokens;
  assert.equal(tokens['--dsh-work-switch-w'], '38px');
  assert.equal(tokens['--dsh-work-button-border'], '3px solid #111111');
  const css = shell.appended[0].textContent;
  assert.ok(css.includes('[role="switch"][aria-checked="true"]:has(> :only-child):not(:has(> :only-child > *))'));
  assert.ok(css.includes('[data-dsh-work-control="button"]'));
  assert.ok(!/class[$*^]?=/.test(css), 'no CSS-module class selectors');
});
