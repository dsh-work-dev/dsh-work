// Installed as a per-launch DSH patch. It observes domain events and never answers a request.
import {timingSafeEqual} from 'node:crypto';

export const name = 'dsh-work-pet-activity';
export const inject = ['webServer', 'connection', 'sessions', 'sessionController', 'agents', 'sessionProjections'];
const text = (value, max = 240) => typeof value === 'string' ? [...value.slice(0, max * 2).replace(/[\u0000-\u001f\u007f]/g, ' ').trim()].slice(0, max).join('') : '';
const plain = blocks => {
  let result = '';
  for (const b of Array.isArray(blocks) ? blocks.slice(0, 32) : []) {
    if (b?.type === 'text') result += text(b.text) + ' ';
    if (result.length >= 240) break;
  }
  return text(result);
};
export function apply(ctx, config) {
  const rows = new Map();
  let revision = 0, requestID = 0, navigation = null, navigationError = '';
  const changed = () => { revision++; };
  function row(id) {
    if (typeof id !== 'string' || !id || id.length > 256) return null;
    if (!rows.has(id)) {
      if (rows.size >= 256) {
        const old = [...rows.values()].find(r => !r.running && !r.pending.size);
        if (!old) return null;
        rows.delete(old.sessionId);
      }
      rows.set(id, {sessionId: id, title: '', parentSessionId: '', seq: -1, turn: 0, running: false,
        workPhase: '', tools: new Map(), toolActivity: '', outcome: '', summary: '', completedSeq: -1, readSeq: -1, updatedAt: Date.now(), pending: new Map()});
    }
    return rows.get(id);
  }
  function onEvent(session, event, replay = false) {
    const r = row(session.id);
    if (!r || event.seq <= r.seq) return;
    r.seq = event.seq;
    const data = event.data ?? {};
    switch (event.type) {
      case 'turn/start': r.running = true; r.turn = data.turn; r.outcome = ''; r.summary = ''; r.tools.clear(); r.workPhase = 'thinking'; r.toolActivity = ''; break;
      case 'turn/end':
        r.tools.clear(); r.workPhase = ''; r.toolActivity = ''; r.running = false; r.outcome = data.reason?.kind ?? 'interrupted';
        if (r.outcome === 'completed') r.completedSeq = event.seq;
        if (r.outcome === 'error') r.summary = text(data.reason?.error?.message);
        break;
      case 'step/start':
        if (r.running && !r.tools.size) r.workPhase = 'thinking';
        break;
      case 'tool/call': {
        if (!r.running) break;
        const id = text(data.callId,256), name = text(data.name,120);
        if (id && r.tools.size < 128) r.tools.set(id,name);
        r.workPhase = 'working'; r.toolActivity = classifyTool(name); break;
      }
      case 'tool/result': {
        if (!r.running) break;
        const m=data.message;
        const id=text(m?.source?.callId ?? m?.content?.find?.(b=>b.toolCallId)?.toolCallId ?? m?.toolCallId ?? m?.callId ?? data.callId,256);
        // An unidentified result cannot prove other concurrent calls have finished.
        if (id) r.tools.delete(id);
        r.workPhase = r.tools.size ? 'working' : 'result';
        r.toolActivity = r.tools.size ? classifyTool(r.tools.values().next().value) : '';
        break;
      }
      case 'assistant/message': if (!data.interrupted) r.summary = plain(data.message?.content); break;
      case 'session/title': r.title = text(data.title, 120); break;
      default: return;
    }
    if (replay) r.readSeq = event.seq;
    r.updatedAt = event.time || Date.now();
    changed();
  }
  // Seed restored sessions quietly; live events alone create new unread completions.
  function seed(session) {
    const r = row(session.id);
    for (const event of (session.events ?? []).slice(-512)) onEvent(session, event, true);
    // A historical open turn is not evidence of a live execution after restart.
    if (r) r.running = false;
  }
  for (const session of ctx.sessions.list().slice(-64)) seed(session);
  // Cold sessions remain navigable without activating agents or replaying old alerts.
  const lifetime = new AbortController();
  ctx.effect(() => () => lifetime.abort());
  void ctx.sessionController.list({}, AbortSignal.any([lifetime.signal, AbortSignal.timeout(2000)])).then(baseline => {
    if (lifetime.signal.aborted) return;
    for (const s of baseline.items.slice(0, 256)) {
      const r = row(s.sessionId); if (!r) continue;
      r.parentSessionId = text(s.parentSessionId, 256);
      r.hints = s.projections?.values ?? {};
      if (r.seq < 0) { r.running = s.running; r.updatedAt = s.updatedAt; }
    }
    changed();
  }).catch(() => { /* Live sessions continue; the authenticated client can reconcile its list. */ });
  ctx.on('session/created', seed);
  ctx.on('session/event', (session, event) => onEvent(session, event));
  ctx.on('session/disposed', session => {
    const r = rows.get(session.id); if (!r) return;
    if (r.running) r.outcome = 'interrupted';
    r.running = false; r.pending.clear(); r.jobs = []; changed();
  });
  ctx.on('api-session/status', (id, running) => { const r = row(id); if (r) { r.running = running; changed(); } });
  ctx.on('api-session/added', s => { const r = row(s.sessionId); if (r) { r.parentSessionId = text(s.parentSessionId, 256); r.running = s.running; changed(); } });
  ctx.on('api-session/error', (id, message) => { const r = row(id); if (r) { r.outcome = 'error'; r.summary = text(message); r.running = false; r.updatedAt = Date.now(); changed(); } });
  ctx.on('api-session/removed', id => { rows.delete(id); changed(); });
  // DSH exposes no paginated job query. Cache its authorized list on change so
  // 500ms snapshots never repeatedly materialize a session's retained history.
  ctx.inject(['jobs'], jobsCtx => {
    const dirty = owner => {
      if (owner) { const r = rows.get(owner.id); if (r) r.jobsDirty = true; }
      else for (const r of rows.values()) r.jobsDirty = true;
    };
    dirty();
    jobsCtx.effect(() => jobsCtx.jobs.onJobsChanged(dirty));
  });
  for (const [eventName, kind] of [['approval/request', 'approval'], ['user-questions/request', 'question']]) {
    ctx.on(eventName, async (request, next) => {
      const r = row(request.agent?.id);
      const key = String(++requestID);
      if (r) {
        const question = request.questions?.[0];
        r.pending.set(key, {key, kind: question?.intent?.kind === 'plan-review' ? 'plan-review' : kind,
          count: kind === 'approval' ? 1 : request.questions?.length ?? 0,
          text: text(kind === 'approval' ? (request.reason || request.toolName) : [question?.header, question?.question].filter(Boolean).join(': '))});
        r.updatedAt = Date.now(); changed();
      }
      const clear = () => { if (r?.pending.delete(key)) changed(); };
      request.signal?.addEventListener('abort', clear, {once: true});
      if (request.signal?.aborted) clear();
      try { return await next(); }
      finally { request.signal?.removeEventListener('abort', clear); clear(); }
    }, {prepend: true, global: true});
  }
  function snapshot() {
    if (navigation && Date.now() > navigation.expiresAt) {
      navigation = null; navigationError = 'Conversation is unavailable'; changed();
    }
    const sessions = [];
    for (const r of rows.values()) {
      const agent = ctx.agents.get(r.sessionId);
      const session = agent?.session ?? ctx.sessions.get(r.sessionId);
      const values = session ? ctx.sessionProjections.snapshot(session).values : r.hints ?? {};
      const goal = values.goal?.goal;
      if (agent && ctx.get('jobs') && (r.jobsDirty || !r.jobs)) {
        r.jobs = ctx.get('jobs').list(agent).slice(-32);
        r.jobsDirty = false;
      }
      const jobs = r.jobs ?? [];
      const pending = [...r.pending.values()].at(-1);
      sessions.push({sessionId: r.sessionId, title: r.title || text(values.title?.title || values.title, 120) || r.sessionId,
        parentSessionId: r.parentSessionId, seq: r.seq, turn: r.turn, running: r.running,
        workPhase: r.running ? r.workPhase : '', toolActivity: r.running ? r.toolActivity : '', outcome: r.outcome, summary: r.summary, unread: r.completedSeq > r.readSeq,
        updatedAt: r.updatedAt, interaction: pending ?? null,
        goal: goal ? {phase: text(goal.phase, 32), objective: text(goal.objective), reason: text(goal.blockedReason?.message),
          rounds: values.goal.roundsStarted ?? 0, maxRounds: goal.maxGoalRounds ?? 0} : null,
        queued: agent?.inbox?.nextTurn?.length ?? 0,
        steering: agent?.inbox?.nextStep?.filter(m => m.source?.kind === 'user').length ?? 0,
        jobs: jobs.slice(0, 32).map(j => ({id: text(j.id, 256), label: text(j.label, 120), status: text(j.status, 32), detail: text(j.detail, 160)}))});
    }
    return {schemaVersion: 1, generation: config.generation, revision, sessions, navigationError};
  }
  function authorized(req) {
    const actual = Buffer.from(req.headers['x-dsh-work-token'] ?? '');
    const expected = Buffer.from(config.token ?? '');
    return expected.length >= 32 && actual.length === expected.length && timingSafeEqual(actual, expected);
  }
  const reply = (res, status, data) => { res.writeHead(status, {'Content-Type': 'application/json', 'Cache-Control': 'no-store'}); res.end(JSON.stringify(data)); };
  async function body(req) {
    let size = 0; const chunks = [];
    for await (const chunk of req) { size += chunk.length; if (size > 65536) throw new Error('request too large'); chunks.push(chunk); }
    return JSON.parse(Buffer.concat(chunks).toString());
  }
  ctx.effect(() => ctx.webServer.register({kind: 'exact', path: '/__dshwork/activity', handler: async (req, res) => {
    const rejection = ctx.connection.requestRejection(req);
    if (rejection !== undefined) return reply(res, rejection, {});
    if (!authorized(req)) return reply(res, 403, {});
    try {
      if (req.method === 'GET') return reply(res, 200, snapshot());
      if (req.method === 'POST') {
        const input = await body(req);
        if (!rows.has(input.sessionId)) return reply(res, 404, {});
        navigationError = '';
        navigation = {id: String(++requestID), sessionId: input.sessionId, parentSessionId: rows.get(input.sessionId).parentSessionId, expiresAt: Date.now() + 15000};
        return reply(res, 200, {});
      }
      reply(res, 405, {});
    } catch { reply(res, 400, {}); }
  }}));
  // This route also passes through DSH's normal authenticated web trust boundary.
  ctx.effect(() => ctx.webServer.register({kind: 'exact', path: '/__dshwork/activity-client', handler: async (req, res) => {
    const rejection = ctx.connection.requestRejection(req);
    if (rejection !== undefined) return reply(res, rejection, {});
    if (req.method !== 'POST' || req.headers.origin !== `http://${req.headers.host}`) return reply(res, 403, {});
    try {
      const input = await body(req);
      for (const meta of Array.isArray(input.sessions) ? input.sessions.slice(0, 256) : []) {
        const r = row(meta.id); if (!r) continue;
        r.title = text(meta.title, 120) || r.title;
        r.parentSessionId = text(meta.parentId, 256) || r.parentSessionId;
      }
      const r = rows.get(input.current);
      if (r && input.visible === true) r.readSeq = r.seq;
      if (navigation && input.ack === navigation.id) { navigation = null; navigationError = text(input.error); }
      reply(res, 200, {navigation});
    } catch { reply(res, 400, {}); }
  }}));
}

function classifyTool(name) {
 const tokens=String(name??'').toLowerCase().split(/[^a-z0-9]+/);
 if(tokens.some(t=>['test','check','lint','build','verify'].includes(t))) return 'testing';
 if(tokens.some(t=>['write','edit','patch','replace','create','move','delete'].includes(t))) return 'editing';
 if(tokens.some(t=>['search','grep','find','glob','web','read','fetch','open'].includes(t))) return 'searching';
 if(tokens.some(t=>['shell','bash','exec','command','terminal','powershell','pwsh'].includes(t))) return 'commanding';
 return 'using-tool';
}
