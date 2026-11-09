import test from 'node:test';
import assert from 'node:assert/strict';
import {ExtensionProtocolV2} from './cdp-adapter.mjs';

test('tab-created event and createTarget share one pending debugger attachment', async () => {
  let release;
  const attached = new Promise(resolve => {release = resolve;});
  let attaches = 0;
  const events = [];
  const model = new ExtensionProtocolV2(async (method) => {
    if (method === 'chrome.tabs.create') {
      model.handleExtensionEvent('chrome.tabs.onCreated', [{id: 42}]);
      return {id: 42};
    }
    if (method === 'chrome.debugger.attach') { attaches++; await attached; return {}; }
    if (method === 'chrome.debugger.sendCommand') return {targetInfo: {targetId: 'actual-target'}};
    throw Error(method);
  });
  model.connectOverCDP(event => events.push(event));
  await model.handleCDPCommand('Target.setAutoAttach', {});
  const created = model.handleCDPCommand('Target.createTarget', {url: 'about:blank'});
  await Promise.resolve();
  release();
  assert.deepEqual(await created, {result: {targetId: 'actual-target'}});
  assert.equal(attaches, 1);
  assert.equal(events.filter(e => e.method === 'Target.attachedToTarget').length, 1);
});
