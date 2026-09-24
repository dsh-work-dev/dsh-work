import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import vm from 'node:vm';

const source = readFileSync(new URL('./boot.js', import.meta.url), 'utf8');
function bootHarness() {
  let callback, boot = null;
  const reports = [];
  const root = {childElementCount: 0, textContent: '', querySelector: () => boot};
  const context = {
    __WORK_GENERATION__: 'generation-1',
    fetch: async (_url, init) => { reports.push(JSON.parse(init.body)); return {ok: true}; },
    document: {documentElement: {}, getElementById: () => root},
    MutationObserver: class { constructor(fn) { callback = fn; } observe() {} disconnect() {} },
    setTimeout,
  };
  vm.runInNewContext(source, context);
  return {reports, root, change(value, records = []) { boot = value; callback(records); }};
}
test('pending and failed plugin boot never reports success', () => {
  const h = bootHarness();
  h.change({textContent: 'Loading plugins…', querySelector: () => ({})});
  assert.equal(h.reports.length, 0);
  h.change({textContent: 'Failed to load plugins\nweb boot: 1 entry did not activate\ndsh-just-chat: pending (waiting for service: settingsScope)', querySelector: () => null});
  assert.equal(h.reports.length, 1);
  assert.match(h.reports[0].Detail, /settingsScope/);
  assert.equal(h.reports[0].Generation, 'generation-1');
});
test('only BootHandoff replacing boot with mounted content reports success', () => {
  const h = bootHarness();
  h.root.childElementCount = 1;
  h.root.textContent = 'shell';
  h.change(null);
  assert.equal(h.reports.length, 0);
  h.change({textContent: 'Loading plugins…', querySelector: () => ({})});
  h.change(null);
  assert.deepEqual(h.reports, [{Generation: 'generation-1', Detail: ''}]);
});
test('cached modules adding and removing boot in one mutation batch', () => {
  const h = bootHarness();
  h.root.childElementCount = 1;
  h.root.textContent = 'mounted';
  h.change(null, [{addedNodes: [{nodeType: 1, matches: () => true}]}]);
  assert.equal(h.reports[0].Detail, '');
});
