import {execFile} from 'node:child_process';
import {promisify} from 'node:util';

export const name = 'dsh-work-overview';
export const inject = ['webServer', 'connection', 'sessionController'];
const exec = promisify(execFile);
const text = (value, limit = 240) => typeof value === 'string' ? value.replace(/[\u0000-\u001f\u007f]/g, ' ').trim().slice(0, limit) : '';
const number = value => Number.isFinite(value) && value >= 0 ? value : 0;
const iso = time => new Date(time).toISOString();
const buckets = () => ({cacheHit: 0, cacheMiss: 0, output: 0});
const sum = value => value.cacheHit + value.cacheMiss + value.output;
const service = (ctx, key) => typeof ctx.get === 'function' ? ctx.get(key) : ctx[key];

// The five reporting entries pinned off by dshadapter.ensureAppProfilePatch.
// This is live observation of those entries, not another enablement policy.
const reportingModules = [
  '@deepseek-ai/dsh-host-product-telemetry-otel',
  '@deepseek-ai/dsh-client-product-analytics',
  '@deepseek-ai/dsh-session-telemetry-otel',
  '@deepseek-ai/dsh-session-log-deepseek',
  '@deepseek-ai/dsh-plugin-package-inventory-deepseek',
];

export function privacyHealth(entries, locale = 'zh-CN') {
  const copy = {
    'zh-CN': {unknown: '遥测与上报状态暂不可用', enabled: '遥测或上报已启用', stopping: '遥测与上报停用尚未完成', disabled: '遥测与上报当前已停用'},
    en: {unknown: 'Reporting status unavailable', enabled: 'Telemetry or reporting is enabled', stopping: 'Reporting has not fully stopped', disabled: 'Telemetry and reporting are currently disabled'},
    'ja-JP': {unknown: 'テレメトリーと送信の状態を確認できません', enabled: 'テレメトリーまたは送信が有効です', stopping: 'テレメトリーと送信の停止が完了していません', disabled: 'テレメトリーと送信は現在無効です'},
  }[locale] ?? {unknown: 'Reporting status unavailable', enabled: 'Reporting is enabled', stopping: 'Reporting has not fully stopped', disabled: 'Reporting is currently disabled'};
  const rows = Array.isArray(entries) ? entries.filter(row => reportingModules.includes(row.moduleName)) : [];
  if (rows.some(row => row.enabled === true)) return {id: 'privacy', state: 'error', summary: copy.enabled};
  if (reportingModules.some(module => !rows.some(row => row.moduleName === module && typeof row.enabled === 'boolean'))) return {id: 'privacy', state: 'unknown', summary: copy.unknown};
  if (rows.some(row => row.enabled !== false || row.fiberPhase !== null)) return {id: 'privacy', state: 'warning', summary: copy.stopping};
  return {id: 'privacy', state: 'ok', summary: copy.disabled};
}

/** Calendar windows follow the requesting machine's UTC offset; week begins Monday. */
export function periodWindow(period, now, offset = 0) {
  const shift = offset * 1000;
  const date = new Date(now + shift);
  date.setUTCHours(0, 0, 0, 0);
  if (period === 'week') date.setUTCDate(date.getUTCDate() - (date.getUTCDay() + 6) % 7);
  if (period === 'month') date.setUTCDate(1);
  return {start: date.getTime() - shift, end: now, offset};
}

function usageOf(event) {
  if (event.type === 'assistant/message' && event.data.usage) return event.data.usage;
  return event.data.stream?.findLast(record => record.type === 'chunk' && record.chunk?.type === 'usage')?.chunk.usage;
}

/** Fold owned durable events, not the projected/compacted conversation surface. */
export function foldSession(row, observation, window, pending = false, changes) {
  const all = observation.events ?? [];
  const own = all.slice(observation.inheritedEventCount ?? observation.header?.inheritedEventCount ?? 0);
  const calls = new Map(), attempts = new Map(), turns = new Map(), changesByTurn = new Map();
  const top = row.origin !== 'subagent' && !(observation.header?.delegationDepth > 0) && !(observation.meta?.delegationDepth > 0);
  let route = {}, title = text(row.projections?.values?.title?.title ?? row.projections?.values?.title), summary = '', outcome;
  let lastTime = 0, latestEnd = 0, agentMs = 0, missingUsage = false;
  const events = [];
  const inside = time => time >= window.start && time <= window.end;
  for (const e of own) {
    const d = e.data ?? {};
    if (Number.isFinite(e.time)) lastTime = Math.max(lastTime, e.time);
    if (e.type === 'session/title') title = text(d.title, 120);
    if (e.type === 'request/header') route = d.header?.config ?? {};
    if (e.type === 'request/context') route = d.context ?? d;
    if (e.type === 'turn/start') turns.set(d.turn, {start: e.time});
    if (e.type === 'turn/end') {
      const t = turns.get(d.turn); if (t) t.end = e.time;
      outcome = d.reason?.kind ?? 'interrupted'; latestEnd = e.time;
      if (inside(e.time) && top) events.push({id: `${row.sessionId}:${e.seq}`, at: iso(e.time), title: title || row.sessionId,
        source: 'DSH', state: outcome === 'completed' ? 'completed' : 'interrupted', sessionId: row.sessionId});
    }
    if (e.type === 'tool/call') calls.set(d.callId, {name: text(d.name, 120), at: e.time});
    if (e.type === 'tool/result') {
      const call = calls.get(d.message?.source?.callId ?? d.message?.toolCallId);
      if (call) { call.failed = d.message?.isError === true; call.settled = true; }
    }
    if (e.type === 'llm/retry-started') {
      // A retry settles a separate billed attempt; later messages cannot replace it.
      const key = `${d.turn}:${d.step}`;
      const previous = attempts.get(key);
      if (previous) { attempts.set(`${key}:retry:${e.seq}`, previous); attempts.delete(key); }
    }
    if (e.type === 'assistant/message' || e.type === 'assistant/attempt') {
      const usage = usageOf(e);
      if (!usage && inside(e.time)) missingUsage = true;
      if (usage) {
        const source = d.message?.source ?? route;
        const model = [text(source.provider, 120), text(source.model, 120)].filter(Boolean).join('/');
        attempts.set(`${d.turn}:${d.step}`, {at: e.time, model: model || 'unknown',
          cacheHit: number(usage.cacheReadTokens), cacheMiss: number(usage.inputTokens) + number(usage.cacheWriteTokens), output: number(usage.outputTokens)});
      }
      if (e.type === 'assistant/message' && !d.interrupted) summary = text(d.message?.content?.filter(b => b.type === 'text').map(b => b.text).join(' '));
    }
    if (e.type === 'workspace/changes' && inside(e.time)) {
      const value = changes?.summary(row.sessionId, e.seq);
      if (value) changesByTurn.set(d.turn, value);
    }
  }
  for (const t of turns.values()) agentMs += Math.max(0, Math.min(t.end ?? window.end, window.end) - Math.max(t.start, window.start));
  const state = pending ? 'waiting' : row.running ? 'running' : outcome === 'completed' ? 'completed' : 'interrupted';
  return {
    session: {id: row.sessionId, title: title || row.sessionId, state, summary: summary || undefined, workspace: row.cwd,
      updatedAt: iso(lastTime || row.updatedAt || window.end), turns: turns.size},
    relevant: row.running || pending || (lastTime >= window.start && (turns.size > 0 || outcome)), top,
    completed: state === 'completed' && inside(latestEnd),
    agentMs, missingUsage, calls: [...calls.values()].filter(call => inside(call.at)),
    attempts: [...attempts.values()].filter(attempt => inside(attempt.at)), events, changes: [...changesByTurn.values()],
    missingChanges: own.some(e => e.type === 'workspace/changes' && inside(e.time) && !changes?.summary(row.sessionId, e.seq)),
  };
}

/** Read-only Git inspection; roots originate solely in the composed Workspace registry. */
export async function gitStatus(cwd, signal, run = exec) {
  const {stdout} = await run('git', ['--no-optional-locks', '-c', 'core.fsmonitor=false', '-C', cwd, 'status', '--porcelain=v1', '--branch', '-z'],
    {encoding: 'utf8', timeout: 3000, maxBuffer: 1024 * 1024, windowsHide: true, signal});
  const records = stdout.split('\0').filter(Boolean);
  const heading = records.shift() ?? '';
  const branch = heading.replace(/^## /, '').split('...')[0];
  // Rename/copy records carry a second path; those are not another changed file.
  let changed = 0;
  for (let i = 0; i < records.length; i++) { changed++; if (/^[RC]|^.[RC]/.test(records[i])) i++; }
  return {branch: text(branch, 120), changedFiles: changed};
}

async function mapConcurrent(items, work, concurrency = 4) {
  const result = new Array(items.length); let next = 0;
  await Promise.all(Array.from({length: Math.min(concurrency, items.length)}, async () => {
    while (next < items.length) { const i = next++; result[i] = await work(items[i]); }
  }));
  return result;
}

/** A feature lacking its service is unavailable, and one corrupt session does not erase the others. */
export async function collectOverview(ctx, options = {}) {
  const now = options.now ?? Date.now(), period = options.period ?? 'today', offset = options.timezoneOffsetSeconds ?? 0;
  const window = periodWindow(period, now, offset), signal = options.signal ?? AbortSignal.timeout(30000);
  const availability = {billing: {state: 'unavailable', reason: 'no-authoritative-billing'}}, data = {now: iso(now), period, locale: options.locale ?? 'zh-CN'};
  const read = async (key, fn) => {
    try { const value = await fn(); availability[key] = {state: 'ready'}; return value; }
    catch { availability[key] = {state: 'unavailable'}; return undefined; }
  };
  const query = service(ctx, 'sessionQuery'), changes = service(ctx, 'workspaceChanges');
  const sessions = await read('sessions', async () => {
    const listed = await ctx.sessionController.list({}, signal);
    const rows = [...listed.items];
    // Controller presentation intentionally omits cold Sessions without a cwd.
    // Analytics must still include their owned work, so reconcile the complete
    // public query corpus rather than treating that presentation list as a ledger.
    if (query?.listSessions) {
      const known = new Set(listed.items.map(row => row.sessionId));
      for (const record of await query.listSessions(signal)) if (!known.has(record.header.id)) {
        const h = record.header;
        rows.push({sessionId: h.id, origin: h.origin, parentSessionId: h.parentSession, cwd: h.cwd, updatedAt: h.createdAt, running: false});
      }
    }
    const waiting = new Set();
    const byId = new Map(rows.map(row => [row.sessionId, row]));
    for (const row of rows) if (options.waiting?.has(row.sessionId)) {
      let owner = row; const seen = new Set();
      while (owner.origin === 'subagent' && owner.parentSessionId && !seen.has(owner.sessionId)) {
        seen.add(owner.sessionId);
        const parent = byId.get(owner.parentSessionId); if (!parent) break;
        owner = parent;
      }
      waiting.add(owner.sessionId);
    }
    let failures = 0;
    const folds = await mapConcurrent(rows, async row => {
      let observation;
      try {
        signal.throwIfAborted();
        observation = query?.observeSession ? await query.observeSession(row.sessionId, {signal, projectionMode: 'none'})
          : await ctx.sessionController.inspect(row.sessionId, signal);
        const cacheKey = `${period}:${window.start}:${offset}:${row.sessionId}`;
        const cached = options.foldCache?.get(cacheKey);
        if (!row.running && !waiting.has(row.sessionId) && observation.cursor !== undefined && cached?.cursor === observation.cursor) return cached.fold;
        const fold = foldSession(row, observation, window, waiting.has(row.sessionId), changes);
        if (period === 'today') fold.previous = foldSession(row, observation, {start: window.start - 86400000, end: window.start - 1}, false).attempts;
        if (options.foldCache && !row.running && !waiting.has(row.sessionId)) {
          if (options.foldCache.size >= 2048) options.foldCache.delete(options.foldCache.keys().next().value);
          options.foldCache.set(cacheKey, {cursor: observation.cursor, fold});
        }
        return fold;
      } catch { failures++; return undefined; }
      finally { observation?.[Symbol.dispose]?.(); }
    });
    return {folds: folds.filter(Boolean), failures};
  });
  if (sessions) {
    const {folds, failures} = sessions;
    if (failures) availability.sessions = {state: folds.length ? 'partial' : 'unavailable', failedSessions: failures};
    if (folds.length || !failures) {
      data.sessions = {items: folds.filter(f => f.top && f.relevant).map(f => f.session).sort((a, b) => b.updatedAt.localeCompare(a.updatedAt))};
      const calls = folds.flatMap(f => f.calls), toolNames = new Map();
      for (const c of calls) toolNames.set(c.name, (toolNames.get(c.name) ?? 0) + 1);
      const settled = calls.filter(c => c.settled);
      data.output = {completed: folds.filter(f => f.top && f.completed).length, agentSeconds: folds.reduce((n, f) => n + f.agentMs, 0) / 1000,
        toolCalls: [...toolNames].map(([name, count]) => ({name, count})),
        toolFailureRate: settled.length ? settled.filter(c => c.failed).length / settled.length : null};
      const recorded = folds.flatMap(f => f.changes);
      if (recorded.length) {
        data.output.changedFiles = new Set(recorded.flatMap(c => c.files.map(f => `${c.cwd}/${f.path}`))).size;
        data.output.addedLines = recorded.reduce((n, c) => n + c.added, 0);
        data.output.removedLines = recorded.reduce((n, c) => n + c.deleted, 0);
      }
      availability.changes = {state: folds.some(f => f.missingChanges) || recorded.some(c => c.total > c.files.length) ? 'partial' : recorded.length ? 'ready' : 'unavailable'};
      const hourly = new Map(), models = new Map(), totals = buckets();
      for (const a of folds.flatMap(f => f.attempts)) {
        const hour = Math.floor((a.at + offset * 1000) / 3600000) * 3600000 - offset * 1000;
        if (!hourly.has(hour)) hourly.set(hour, {at: iso(hour), ...buckets()});
        for (const key of ['cacheHit', 'cacheMiss', 'output']) { hourly.get(hour)[key] += a[key]; totals[key] += a[key]; }
        models.set(a.model, (models.get(a.model) ?? 0) + sum(a));
      }
      data.hourlyTokens = [...hourly.values()].sort((a, b) => a.at.localeCompare(b.at));
      data.models = [...models].map(([name, tokens]) => ({name, tokens})).sort((a, b) => b.tokens - a.tokens);
      data.cacheHitRate = totals.cacheHit + totals.cacheMiss ? totals.cacheHit / (totals.cacheHit + totals.cacheMiss) : null;
      if (period === 'today' && data.cacheHitRate !== null && !failures) {
        const previous = folds.flatMap(f => f.previous ?? []).reduce((total, a) => ({hit: total.hit + a.cacheHit, input: total.input + a.cacheHit + a.cacheMiss}), {hit: 0, input: 0});
        if (previous.input) data.cacheDelta = data.cacheHitRate - previous.hit / previous.input;
      }
      data.cost = {currency: 'CNY', tokens: sum(totals)};
      data.events = folds.flatMap(f => f.events).sort((a, b) => a.at.localeCompare(b.at));
      availability.usage = {state: failures || folds.some(f => f.missingUsage) ? 'partial' : 'ready'};
      if (!models.size && folds.some(f => f.missingUsage)) {
        delete data.cost.tokens; delete data.hourlyTokens; delete data.models;
      }
    }
  }
  const runtime = {};
  const workspaceRegistry = service(ctx, 'workspaceRegistry');
  const workspaces = await read('workspaces', () => { if (!workspaceRegistry) throw Error(); return workspaceRegistry.list(); });
  if (workspaces) {
    let failedGit = 0;
    runtime.workspaces = await mapConcurrent(workspaces, async w => {
      const base = {id: w.id, name: text(w.title, 120)};
      try { return {...base, ...await (options.gitStatus ?? gitStatus)(w.path, signal)}; }
      catch { failedGit++; return base; }
    });
    availability.git = {state: failedGit ? failedGit === workspaces.length ? 'unavailable' : 'partial' : 'ready'};
  }
  const schedule = service(ctx, 'schedule');
  const schedules = await read('schedules', () => { if (!schedule?.catalog) throw Error(); return schedule.catalog(); });
  if (schedules) {
    const active = schedules.filter(s => s.status === 'active');
    runtime.scheduled = active.length;
    runtime.nextScheduledAt = active.map(s => s.scheduledAt).sort()[0];
    if (data.sessions) for (const s of active) data.sessions.items.push({id: `schedule:${s.id}`, title: text(s.title, 120), state: 'scheduled', nextRunAt: s.scheduledAt});
    data.events ??= [];
    for (const s of active) data.events.push({id: `schedule:${s.id}`, at: s.scheduledAt, title: text(s.title, 120), source: 'DSH', state: 'scheduled', sessionId: s.sessionId});
  }
  const skills = service(ctx, 'skills');
  const catalog = await read('skills', () => { if (!skills?.list) throw Error(); return skills.list({signal}); });
  if (catalog) runtime.skills = catalog.length;
  data.runtime = runtime;
  const pluginManager = service(ctx, 'pluginManager');
  const inventory = await read('privacy', () => {
    if (!pluginManager?.listPlugins) throw Error();
    return pluginManager.listPlugins(); // Safe inventory metadata; never plugin configuration or credentials.
  });
  const privacy = privacyHealth(inventory, options.locale);
  data.health = [privacy];
  if (inventory && privacy.state === 'unknown') availability.privacy = {state: 'partial', reason: 'missing-reporting-entries'};
  const account = service(ctx, 'accountController');
  const balance = await read('balance', () => {
    if (!account?.getBalance) throw Error();
    return account.getBalance({version: text(options.version, 100) || 'dsh-work', locale: options.locale ?? 'zh-CN', timezoneOffsetSeconds: offset});
  });
  if (balance?.status === 'ready') {
    const currencies = new Set([...balance.value, ...balance.bonusWallets].map(w => w.currency));
    // Never add CNY and USD together. The renderer contract currently carries one currency.
    const currency = currencies.has('CNY') ? 'CNY' : currencies.values().next().value;
    if (currency) data.cost = {...data.cost, currency, balance: [...balance.value, ...balance.bonusWallets].filter(w => w.currency === currency).reduce((n, w) => n + Number(w.balance), 0)};
    if (currencies.size > 1) availability.balance = {state: 'partial', reason: 'multiple-currencies'};
  } else availability.balance = {state: 'unavailable'};
  return {schemaVersion: 1, profileKey: options.profileKey, collectedAt: iso(now), availability, data};
}

/** Generation-bound, same-origin read endpoint. No credential material crosses this seam. */
export function apply(ctx, config = {}) {
  const waiting = new Map(), lifetime = new AbortController();
  for (const eventName of ['approval/request', 'user-questions/request']) ctx.on(eventName, async (request, next) => {
    const id = request.agent?.id;
    if (id) waiting.set(id, (waiting.get(id) ?? 0) + 1);
    try { return await next(); } finally { if (id) { const count = waiting.get(id) - 1; if (count) waiting.set(id, count); else waiting.delete(id); } }
  }, {prepend: true, global: true});
  ctx.effect(() => () => lifetime.abort());
  const inflight = new Map(), recent = new Map(), foldCache = new Map();
  ctx.effect(() => ctx.webServer.register({kind: 'exact', path: '/__dshwork/overview', handler: async (req, res) => {
    const reply = (status, body) => { res.writeHead(status, {'Content-Type': 'application/json', 'Cache-Control': 'no-store'}); res.end(JSON.stringify(body)); };
    const rejection = ctx.connection.requestRejection(req);
    if (rejection !== undefined) return reply(rejection, {});
    if (req.method !== 'GET') { res.setHeader?.('Allow', 'GET'); return reply(405, {}); }
    const url = new URL(req.url, 'http://localhost'), period = url.searchParams.get('period') ?? 'today';
    const offset = Number(url.searchParams.get('timezoneOffsetSeconds') ?? 0), locale = url.searchParams.get('locale') ?? 'zh-CN';
    if (!['today', 'week', 'month'].includes(period) || !Number.isInteger(offset) || Math.abs(offset) > 50400 || !['zh-CN', 'en', 'ja-JP'].includes(locale)) return reply(400, {});
    const key = `${period}:${offset}:${locale}`;
    try {
      const cached = recent.get(key);
      if (cached && Date.now() - cached.at < 5000) return reply(200, cached.snapshot);
      if (!inflight.has(key)) inflight.set(key, collectOverview(ctx, {...config, period, timezoneOffsetSeconds: offset, locale, waiting,
        foldCache, signal: AbortSignal.any([lifetime.signal, AbortSignal.timeout(30000)])}).then(snapshot => {
          if (recent.size >= 16) recent.delete(recent.keys().next().value);
          recent.set(key, {at: Date.now(), snapshot}); return snapshot;
        }).finally(() => inflight.delete(key)));
      return reply(200, await inflight.get(key));
    } catch { return reply(503, {}); }
  }}));
}
