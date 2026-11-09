import assert from 'node:assert/strict';
import test from 'node:test';
import {tsImport} from '../../../frontend/node_modules/tsx/dist/esm/api/index.mjs';
const {parseDashboardDSH, parseDashboardDSHSnapshot} = await tsImport('../../../frontend/src/dashboard-dsh.ts', import.meta.url);
const at = '2026-11-08T12:00:00.000Z';
const encode = data => JSON.stringify({schemaVersion: 1, collectedAt: at, availability: {sessions: {state: 'ready'}, usage: {state: 'partial'}, balance: {state: 'ready'}, billing: {state: 'unavailable'}}, data});

test('parser rejects wrong schema and validates numeric/display boundaries rather than trusting Worker JSON', () => {
  assert.equal(parseDashboardDSHSnapshot('{'), undefined);
  assert.equal(parseDashboardDSHSnapshot('{"schemaVersion":2}'), undefined);
  const data = parseDashboardDSH(encode({sessions: {items: [{id: 's', title: 'Task', state: 'running'}, {id: 'bad', title: 'Bad', state: 'invented'}]},
    output: {completed: -1, agentSeconds: '12', changedFiles: null, toolFailureRate: 2},
    cost: {currency: 'CNY', balance: -0.5, tokens: 0}, hourlyTokens: [{at, cacheHit: 0, cacheMiss: null, output: 1}]}));
  assert.equal(data.sessions.items.length, 1);
  assert.equal(data.output.completed, undefined); assert.equal(data.output.agentSeconds, undefined);
  assert.equal(data.output.changedFiles, null); assert.equal(data.output.toolFailureRate, undefined);
  assert.equal(data.cost.balance, -0.5); assert.equal(data.cost.tokens, 0);
  assert.deepEqual(data.hourlyTokens, []);
  assert.equal(data.availability.hourlyTokens.state, 'partial');
});

test('offline projection retains dated facts but marks them unavailable; malformed sessions do not break renderer', () => {
  const data = parseDashboardDSH(encode({sessions: {items: []}, output: {completed: 3}}), at);
  assert.equal(data.sessions.staleAt, at); assert.equal(data.output.completed, 3);
  assert.equal(data.dshSampledAt, at); assert.equal(data.availability.sessions.state, 'unavailable');
  assert.equal(parseDashboardDSH(encode({sessions: {items: 'bad'}})).sessions, undefined);
});

test('privacy health crosses the validated data boundary and loses current-state claims offline', () => {
  const payload = encode({health: [{id: 'privacy', state: 'ok', summary: '遥测与上报当前已停用'}, {id: 'invented', state: 'ok', summary: 'invalid'}]});
  assert.equal(parseDashboardDSH(payload).health.length, 1);
  assert.equal(parseDashboardDSH(payload).health[0].state, 'ok');
  assert.equal(parseDashboardDSH(payload, at).health[0].state, 'unknown');
  assert.equal(parseDashboardDSH(payload, at).health[0].summary, '遥测与上报状态暂不可用');
});
