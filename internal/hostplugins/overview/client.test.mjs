import assert from 'node:assert/strict';
import test from 'node:test';
import vm from 'node:vm';
import {readFileSync} from 'node:fs';

test('public sidebar seat opens Host surface without selecting main and validates badge messages', () => {
  const sent = [], windowEvents = new Map(), buttonEvents = new Map(), columnEvents = new Map(), effects = [], attributes = new Map();
  let factory, component, panelChanged, sessionsChanged;
  let sessionSnapshot = {byId: {}};
  let panelSnapshot = {activePanelId: null};
  const parent = {postMessage: (message, origin) => sent.push({message, origin})};
  const column = {addEventListener: (type, handler) => columnEvents.set(type, handler), removeEventListener() {}, getBoundingClientRect: () => ({right: 248})};
  const button = {closest: name => name === 'nav' ? {parentElement: column} : undefined, contains: target => target === button,
    addEventListener: (type, handler) => buttonEvents.set(type, handler), removeEventListener() {}, setAttribute: (key, value) => attributes.set(key, value), removeAttribute: key => attributes.delete(key), style: {}};
  const window = {parent, location: {ancestorOrigins: ['http://shell.local']}, addEventListener: (type, handler) => windowEvents.set(type, handler), removeEventListener() {}, __ModuleLoader__: {load: value => {factory = value.factory;}}};
  const document = {createElement: () => ({remove() {}}), head: {append() {}}};
  class ResizeObserver {observe() {} disconnect() {}}
  const React = {createElement: (type, props, ...children) => ({type, props, children}), useSyncExternalStore: (_subscribe, get) => get(), useEffect: fn => fn()};
  vm.runInNewContext(readFileSync(new URL('./client.js', import.meta.url), 'utf8'), {window, document, ResizeObserver, console});
  const plugin = factory(() => React);
  plugin.apply({locale: {getSnapshot: () => ({active: 'en'})}, layout: {panelInfo: {getSnapshot: () => panelSnapshot, subscribe: listener => {panelChanged = listener; return () => {};}}}, sessions: {list: {getSnapshot: () => sessionSnapshot, subscribe: listener => {sessionsChanged = listener; return () => {};}}},
    effect: fn => effects.push(fn()), slots: {inject: (name, fn) => {assert.equal(name, 'sidebar.panellist'); fn();}, register: (options, renderer) => {assert.equal(options.name, 'sidebar.panellist'); component = renderer;}}});
  const render = component({size: 16});
  render.props.ref({closest: () => button});
  let prevented = false, stopped = false;
  buttonEvents.get('click')({preventDefault() {prevented = true;}, stopImmediatePropagation() {stopped = true;}});
  assert.ok(prevented && stopped);
  assert.equal(sent.at(-1).message.type, 'dsh-work/open-overview');
  assert.equal(sent.at(-1).message.sidebarWidth, 248); assert.equal(sent.at(-1).origin, 'http://shell.local');
  // Offline comes from the Host; a message from another origin is ignored.
  const badge = {version: 1, type: 'dsh-work/overview-badge', offline: true};
  windowEvents.get('message')({source: parent, origin: 'http://evil.local', data: badge});
  assert.equal(attributes.get('aria-label'), 'Overview');
  windowEvents.get('message')({source: parent, origin: 'http://shell.local', data: badge});
  assert.equal(attributes.get('aria-label'), 'Overview (!)');
  // Pending and running come from DSH's session status store and are reported to the Host.
  const statuses = new Map([['a', {pendingInteraction: 'approval'}], ['b', {running: true}], ['c', {pendingInteraction: 'question'}]]);
  const withStatus = component({size: 16, useSessionStatus: () => statuses});
  const reporter = withStatus.children[0];
  reporter.type(reporter.props);
  assert.deepEqual({type: sent.at(-1).message.type, pending: sent.at(-1).message.pending, running: sent.at(-1).message.running}, {type: 'dsh-work/overview-activity', pending: 2, running: true});
  assert.equal(attributes.get('aria-label'), 'Overview (2)');
  const withBadge = component({size: 16});
  assert.equal(withBadge.children[2].children[0], '2');
  columnEvents.get('click')({target: {closest: () => ({})}});
  assert.equal(sent.at(-1).message.type, 'dsh-work/close-overview');

  // A top-bar open does not activate the sidebar button. The Host message must
  // still select it and arm dismissal by DSH's public navigation subscriptions.
  const receive = windowEvents.get('message');
  const opened = {version: 1, type: 'dsh-work/overview-opened'};
  const countBeforeInvalid = sent.length;
  receive({source: {}, origin: 'http://shell.local', data: opened});
  receive({source: parent, origin: 'http://evil.local', data: opened});
  receive({source: parent, origin: 'http://shell.local', data: {...opened, version: 2}});
  assert.equal(attributes.has('aria-current'), false);
  panelChanged();
  assert.equal(sent.length, countBeforeInvalid);
  receive({source: parent, origin: 'http://shell.local', data: opened});
  assert.equal(attributes.get('aria-current'), 'page');
  const countBeforeGeometry = sent.length;
  // The public source is a selector read paired with a whole-root-store
  // subscription: resizing can notify it without changing panel identity.
  panelSnapshot = {activePanelId: null};
  panelChanged();
  assert.equal(attributes.get('aria-current'), 'page');
  assert.equal(sent.length, countBeforeGeometry);
  panelSnapshot = {activePanelId: 'plugins'};
  panelChanged();
  assert.equal(attributes.has('aria-current'), false);
  assert.equal(sent.at(-1).message.type, 'dsh-work/close-overview');

  // A replay before the icon mounts also restores its selected state.
  render.props.ref(null);
  receive({source: parent, origin: 'http://shell.local', data: opened});
  render.props.ref({closest: () => button});
  assert.equal(attributes.get('aria-current'), 'page');
  // The default conversation may restore after an overview deep link. Its
  // first main-view retention initializes the baseline, not dismissal.
  const countBeforeRestore = sent.length;
  sessionSnapshot = {byId: {s: {id: 's', retainedBy: {mainView: 1}}}};
  sessionsChanged();
  assert.equal(attributes.get('aria-current'), 'page');
  assert.equal(sent.length, countBeforeRestore);
  // Selecting the same conversation again may not change either store;
  // actual session rows are DSH tree items, not native buttons.
  columnEvents.get('click')({target: {closest: selector => selector.includes('[role="treeitem"][aria-selected]') ? {} : null}});
  assert.equal(sent.at(-1).message.type, 'dsh-work/close-overview');
  receive({source: parent, origin: 'http://shell.local', data: opened});
  const countBeforeSwitch = sent.length;
  sessionSnapshot = {byId: {t: {id: 't', retainedBy: {mainView: 1}}}};
  sessionsChanged();
  assert.equal(sent.length, countBeforeSwitch + 1);
  assert.equal(sent.at(-1).message.type, 'dsh-work/close-overview');
  receive({source: parent, origin: 'http://shell.local', data: opened});
  receive({source: parent, origin: 'http://shell.local', data: {version: 1, type: 'dsh-work/overview-closed'}});
  const countAfterClosed = sent.length;
  panelChanged();
  assert.equal(attributes.has('aria-current'), false);
  assert.equal(sent.length, countAfterClosed);
  for (const dispose of effects) dispose?.();
});
