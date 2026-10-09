import assert from 'node:assert/strict';
import test from 'node:test';
import {apply} from './host.js';

test('account Host contributes only the DSH account compatibility marker', () => {
  const listeners = new Map();
  apply({on(name, listener) { listeners.set(name, listener); }});
  const rows = [];
  listeners.get('webserver/index-inject')(rows);
  assert.deepEqual(rows, [{kind: 'global', name: 'dshDesktop', value: {}}]);
});
