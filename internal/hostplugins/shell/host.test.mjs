import assert from 'node:assert/strict';
import test from 'node:test';
import {apply} from './host.js';

test('shell Host contributes generation-scoped transport bootstrap before DSH client modules', () => {
  const listeners = new Map();
  apply({on(name, listener) { listeners.set(name, listener); }}, {generation: 'g/1'});
  const rows = [];
  listeners.get('webserver/index-inject')(rows);
  assert.deepEqual(rows, [
    {kind: 'global', name: '__WORK_GENERATION__', value: 'g/1'},
    {kind: 'global', name: '__DSH_TRANSPORT__', value: {ownsHost: true}},
    {kind: 'script-src', placement: 'head', src: '/__work/boot.js?generation=g%2F1'},
    {kind: 'html', placement: 'head', html: '<script type="module" src="/__work/bridge.js?generation=g%2F1"></script>'},
  ]);
});
