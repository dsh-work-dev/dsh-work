import assert from 'node:assert/strict';
import {resolve} from 'node:path';
import test from 'node:test';
import {collectOverview, foldSession, gitStatus, periodWindow, privacyHealth, apply} from './host.js';

const day = Date.parse('2026-11-08T00:00:00Z'), now = day + 3600000;
const event = (type, seq, data, time = day + seq * 1000) => ({type, seq, data, time});
const row = {sessionId: 's', updatedAt: now, running: false};
const message = (turn, step, input = 10, output = 5) => ({turn, step, usage: {inputTokens: input, outputTokens: output, cacheReadTokens: 20, reasoningTokens: 3},
  message: {source: {provider: 'p', model: 'm'}, content: [{type: 'text', text: 'Done'}]}});
const window = {start: day, end: now};

test('calendar windows are machine-local with Monday week and calendar month', () => {
  const sunday = Date.parse('2026-11-08T16:30:00Z');
  assert.equal(new Date(periodWindow('today', sunday, 28800).start).toISOString(), '2026-11-08T16:00:00.000Z');
  assert.equal(new Date(periodWindow('week', sunday, 28800).start).toISOString(), '2026-11-08T16:00:00.000Z');
  assert.equal(new Date(periodWindow('month', sunday, 28800).start).toISOString(), '2026-10-31T16:00:00.000Z');
});

const reportingRows = () => ['dsh-host-product-telemetry-otel', 'dsh-client-product-analytics', 'dsh-session-telemetry-otel', 'dsh-session-log-deepseek', 'dsh-plugin-package-inventory-deepseek']
  .map(module => ({moduleName: `@deepseek-ai/${module}`, enabled: false, fiberPhase: null}));

test('privacy reflects all five effective live reporting entries, never default patch or control availability', () => {
  const rows = reportingRows();
  assert.deepEqual(privacyHealth(rows), {id: 'privacy', state: 'ok', summary: '遥测与上报当前已停用'});
  assert.equal(privacyHealth(rows.slice(1)).state, 'unknown');
  assert.equal(privacyHealth(undefined).state, 'unknown');
  assert.equal(privacyHealth([...rows, {...rows[0], enabled: true}]).state, 'error');
  assert.equal(privacyHealth(rows.map((row, i) => i ? row : {...row, fiberPhase: 'unloading'})).state, 'warning');
  assert.equal(privacyHealth(rows.map(row => ({...row, readOnlyReason: 'unaddressable'})), 'en').summary, 'Telemetry and reporting are currently disabled');
});

test('collection projects live reporting and leaves actual billing explicitly unavailable', async () => {
  const base = context([], {});
  const ctx = {...base, get: key => key === 'pluginManager' ? {listPlugins: async () => reportingRows()} : base.get(key)};
  const result = await collectOverview(ctx, {now, gitStatus: async () => ({})});
  assert.equal(result.data.health[0].state, 'ok');
  assert.equal(result.availability.privacy.state, 'ready');
  assert.equal(result.availability.billing.state, 'unavailable');
  assert.equal(result.data.cost.spent, undefined);
  assert.equal(result.data.cost.runwaySeconds, undefined);
});

test('whole durable log counts retries once, excludes fork-inherited work, and keeps ordinary forks visible', () => {
  const events = [event('assistant/message', 0, message(1, 1, 999)), event('turn/start', 1, {turn: 2}),
    event('assistant/attempt', 2, {turn: 2, step: 1, stream: [{type: 'chunk', time: day, chunk: {type: 'usage', usage: {inputTokens: 10, outputTokens: 2}}}]}),
    event('assistant/message', 3, message(2, 1)), // replaces the same settlement, not another bill
    event('llm/retry-started', 4, {turn: 2, step: 1}), event('assistant/message', 5, message(2, 1, 15)),
    event('turn/end', 6, {turn: 2, reason: {kind: 'completed'}})];
  const fold = foldSession({...row, parentSessionId: 'fork-parent'}, {events, inheritedEventCount: 1}, window);
  assert.equal(fold.top, true);
  assert.equal(fold.attempts.length, 2);
  assert.equal(fold.attempts.reduce((n, a) => n + a.cacheMiss + a.cacheHit + a.output, 0), 75);
  assert.equal(fold.agentMs, 5000);
  assert.equal(fold.session.summary, 'Done');
  assert.equal(fold.completed, true);
});

test('tools pair by call id, pending calls do not become false failures, waiting overrides running', () => {
  const events = [event('turn/start', 0, {turn: 1}), event('tool/call', 1, {callId: 'a', name: 'bash'}),
    event('tool/call', 2, {callId: 'b', name: 'read_file'}), event('tool/result', 3, {message: {source: {callId: 'a'}, isError: true}})];
  const fold = foldSession({...row, running: true}, {events}, window, true);
  assert.equal(fold.session.state, 'waiting');
  assert.equal(fold.calls[0].failed, true);
  assert.equal(fold.calls[1].settled, undefined);
});

test('reused provider tool IDs retain both calls and settle only the cited call', () => {
  const events = [event('tool/call', 1, {turn: 1, step: 1, callId: 'x', name: 'read'}),
    event('tool/result', 2, {turn: 1, step: 1, message: {source: {callId: 'x'}, isError: true}}),
    event('tool/call', 3, {turn: 1, step: 2, callId: 'x', name: 'write'}),
    {...event('tool/result', 4, {turn: 1, step: 2, message: {source: {callId: 'x'}}}), sourceEventSeqs: [3]}];
  const fold = foldSession(row, {events}, window);
  assert.deepEqual(fold.calls.map(({name, settled, failed}) => [name, settled, failed]), [['read', true, true], ['write', true, false]]);
});

test('cross-midnight work clips duration but retains pre-window tool correlation', () => {
  const events = [event('turn/start', 0, {turn: 1}, day - 10000), event('tool/call', 1, {callId: 'x', name: 'bash'}, day - 5000),
    event('tool/result', 2, {message: {toolCallId: 'x'}}, day + 1000), event('turn/end', 3, {turn: 1, reason: {kind: 'completed'}}, day + 3000)];
  const fold = foldSession(row, {events}, window);
  assert.equal(fold.agentMs, 3000); assert.equal(fold.calls.length, 0);
});

function context(rows, observations) {
  return {sessionController: {list: async () => ({items: rows})}, get: key => ({
    sessionQuery: {observeSession: async id => { if (observations[id] instanceof Error) throw observations[id]; return observations[id]; }},
    workspaceRegistry: {list: () => [{id: 'w', title: 'Project', path: 'C:/project'}]},
    schedule: {catalog: async () => [{id: 'daily', title: 'Review', sessionId: 's', status: 'active', scheduledAt: new Date(now + 1000).toISOString()}]},
    skills: {list: async () => [{name: 'a'}]},
    accountController: {getBalance: async () => ({status: 'ready', value: [{currency: 'CNY', balance: '1.25'}], bonusWallets: [{currency: 'CNY', balance: '0.75'}]})},
  })[key]};
}

test('actual services aggregate complete history and isolate corrupt sessions, missing billing and change records', async () => {
  const events = [event('turn/start', 0, {turn: 1}), event('assistant/message', 1, message(1, 1)), event('workspace/changes', 2, {turn: 1}), event('turn/end', 3, {turn: 1, reason: {kind: 'completed'}})];
  const ctx = context([row, {...row, sessionId: 'broken'}], {s: {events}, broken: Error('bad history')});
  const result = await collectOverview(ctx, {now, gitStatus: async () => ({branch: 'main', changedFiles: 0})});
  assert.equal(result.availability.sessions.state, 'partial');
  assert.equal(result.availability.changes.state, 'partial');
  assert.equal(result.data.output.completed, 1);
  assert.equal(result.data.output.changedFiles, undefined);
  assert.equal(result.data.cost.tokens, 35);
  assert.equal(result.data.cost.balance, 2);
  assert.equal(result.data.cost.spent, undefined);
  assert.equal(result.data.runtime.scheduled, 1);
  assert.equal(result.data.runtime.workspaces[0].branch, 'main');
  assert.equal(result.data.sessions.items.at(-1).state, 'scheduled');
});

test('workspace changes deduplicate absolute and cwd-relative paths without losing turn line counts', async () => {
  const events = [event('workspace/changes', 1, {turn: 1}), event('workspace/changes', 2, {turn: 2}), event('workspace/changes', 3, {turn: 3})];
  const base = context([row], {s: {events}});
  const root = resolve('project');
  const locations = [[root, 'src/file.js'], [resolve(root, 'src'), 'file.js'], [resolve('other-project'), resolve(root, 'src/file.js')]];
  const ctx = {...base, get: key => key === 'workspaceChanges' ? {summary: (_, seq) => ({
    cwd: locations[seq - 1][0], files: [{path: locations[seq - 1][1]}], total: 1, added: seq, deleted: 1,
  })} : base.get(key)};
  const result = await collectOverview(ctx, {now, gitStatus: async () => ({})});
  assert.equal(result.data.output.changedFiles, 1);
  assert.equal(result.data.output.addedLines, 6);
  assert.equal(result.data.output.removedLines, 3);
});

test('cold fold cache follows durable revision and waits for events beyond the first cut', async () => {
  const observations = {};
  const ctx = context([row], observations);
  const options = {now, foldCache: new Map(), gitStatus: async () => ({})};
  const observation = (revision, at, input) => ({source: 'prepared', revision, cursor: 1,
    events: [event('assistant/message', 1, message(1, 1, input), at)]});
  observations.s = observation('r1', now + 100, 10);
  assert.equal((await collectOverview(ctx, options)).data.cost.tokens, 0);
  options.now += 1000;
  assert.equal((await collectOverview(ctx, options)).data.cost.tokens, 35);
  observations.s = observation('r2', now + 100, 100);
  assert.equal((await collectOverview(ctx, options)).data.cost.tokens, 125);
});

test('partial usage on either side withholds the comparison, not measured token totals', async () => {
  const history = [event('assistant/message', 0, message(1, 1, 20), day - 2000),
    event('assistant/message', 2, message(3, 1, 60), day + 2000)];
  const options = {now, gitStatus: async () => ({})};
  const complete = await collectOverview(context([row], {s: {events: history}}), options);
  assert.equal(complete.data.cacheDelta, -0.25);
  for (const at of [day - 1000, day + 1000]) {
    const missing = event('assistant/attempt', 1, {turn: 2, step: 1}, at);
    const partial = await collectOverview(context([row], {s: {events: [history[0], missing, history[1]]}}), options);
    assert.equal(partial.data.cacheDelta, undefined);
    assert.equal(partial.data.cost.tokens, 85);
  }
});

test('scheduled nodes and timeline use the selected calendar horizon even when sessions fail', async () => {
  const base = context([row], {s: Error('bad history')});
  const localMonday = Date.parse('2026-11-08T16:00:00Z');
  const schedules = [[1, 'today'], [86400000, 'tomorrow'], [7 * 86400000, 'next-week']].map(([ms, title]) => ({
    id: title, title, status: 'active', scheduledAt: new Date(localMonday + ms).toISOString(),
  }));
  const ctx = {...base, get: key => key === 'schedule' ? {catalog: async () => schedules} : base.get(key)};
  for (const [period, count] of [['today', 1], ['week', 2], ['month', 3]]) {
    const result = await collectOverview(ctx, {now: localMonday + 500, period, timezoneOffsetSeconds: 28800, gitStatus: async () => ({})});
    assert.equal(result.availability.sessions.state, 'unavailable');
    assert.equal(result.availability.schedules.state, 'ready');
    assert.deepEqual(result.data.sessions.items.map(s => s.title), schedules.slice(0, count).map(s => s.title));
    assert.equal(result.data.events.length, count);
    assert.equal(result.data.runtime.scheduled, 3);
    assert.equal(result.data.runtime.nextScheduledAt, schedules[0].scheduledAt);
  }
});

test('missing usage stays unavailable instead of inventing zero; subagent inherits no duplicated usage', async () => {
  const events = [event('turn/start', 0, {turn: 1}), event('assistant/message', 1, {turn: 1, step: 1, message: {content: []}})];
  const result = await collectOverview(context([{...row, origin: 'subagent'}], {s: {events}}), {now, gitStatus: async () => ({})});
  assert.equal(result.data.cost.tokens, undefined);
  assert.equal(result.availability.usage.state, 'partial');
  assert.equal(result.data.sessions.items.filter(s => s.state !== 'scheduled').length, 0);
});

test('subagent approvals surface on their top-level conversation without merging ordinary fork ownership', async () => {
  const events = [event('turn/start', 0, {turn: 1})];
  const ctx = context([{...row, running: true}, {...row, sessionId: 'child', origin: 'subagent', parentSessionId: 's', running: true}], {s: {events}, child: {events}});
  const result = await collectOverview(ctx, {now, waiting: new Set(['child']), gitStatus: async () => ({})});
  assert.equal(result.data.sessions.items[0].id, 's');
  assert.equal(result.data.sessions.items[0].state, 'waiting');
});

test('git uses a fixed argument vector and counts rename pairs as one change', async () => {
  const result = await gitStatus('C:/project', undefined, async (cmd, args, options) => {
    assert.equal(cmd, 'git'); assert.deepEqual(args, ['--no-optional-locks', '-c', 'core.fsmonitor=false', '-C', 'C:/project',
      'status', '--porcelain=v1', '--branch', '--untracked-files=all', '-z']);
    assert.equal(options.windowsHide, true); assert.equal(options.shell, undefined);
    return {stdout: '## main...origin/main\0R  new.txt\0old.txt\0 M other.txt\0'};
  });
  assert.deepEqual(result, {branch: 'main', changedFiles: 2});
});

test('generation route refuses rejected trust, unsupported methods and malformed period', async () => {
  const routes = [], disposers = [];
  const ctx = {on() {}, effect: fn => { disposers.push(fn()); }, connection: {requestRejection: () => 403}, webServer: {register: route => { routes.push(route); return () => {}; }}};
  apply(ctx);
  const call = async req => { let status; const res = {writeHead(s) {status = s;}, end() {}, setHeader() {}}; await routes[0].handler(req, res); return status; };
  assert.equal(await call({method: 'GET', url: '/__dshwork/overview'}), 403);
  ctx.connection.requestRejection = () => undefined;
  assert.equal(await call({method: 'POST', url: '/__dshwork/overview'}), 405);
  assert.equal(await call({method: 'GET', url: '/__dshwork/overview?period=no'}), 400);
  for (const dispose of disposers) dispose?.();
});

test('route does not coalesce the old and new day across local midnight', async () => {
  const routes = [], base = context([row], {s: {events: [event('assistant/message', 1, message(1, 1), day - 28800000 - 10000)]}});
  const config = {now: day - 28800000 - 1, gitStatus: async () => ({})};
  const ctx = {...base, on() {}, effect: fn => fn(), connection: {requestRejection() {}},
    webServer: {register: route => { routes.push(route); }}};
  apply(ctx, config);
  const call = async () => {
    let status, body;
    await routes[0].handler({method: 'GET', url: '/__dshwork/overview?timezoneOffsetSeconds=28800'},
      {writeHead(code) {status = code;}, end(json) {body = JSON.parse(json);}});
    assert.equal(status, 200);
    return body.data.cost.tokens;
  };
  assert.equal(await call(), 35);
  config.now += 2;
  assert.equal(await call(), 0);
});
