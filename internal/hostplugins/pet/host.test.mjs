import assert from 'node:assert/strict';
import {EventEmitter} from 'node:events';
import {Readable} from 'node:stream';
import test from 'node:test';
import {apply} from './host.js';

class FakeClock {
  #next = 0;
  #timers = new Map();
  setTimeout(callback, delay) { return this.#add(callback, delay, 0); }
  setInterval(callback, delay) { return this.#add(callback, delay, delay); }
  clearTimeout(id) { this.#timers.delete(id); }
  clearInterval(id) { this.#timers.delete(id); }
  #add(callback, delay, interval) {
    const id = ++this.#next;
    this.#timers.set(id, {callback, due: delay, interval});
    return id;
  }
  async tick(delay) {
    for (const timer of this.#timers.values()) timer.due -= delay;
    while (true) {
      const due = [...this.#timers].find(([, timer]) => timer.due <= 0);
      if (!due) break;
      const [id, timer] = due;
      if (timer.interval) timer.due += timer.interval;
      else this.#timers.delete(id);
      timer.callback();
      await Promise.resolve();
    }
  }
}

class Response extends EventEmitter {
  constructor() { super(); this.headers = {}; this.chunks = []; this.writable = true; this.writableEnded = false; this.destroyed = false; }
  writeHead(status, headers) { this.status = status; Object.assign(this.headers, headers); }
  setHeader(name, value) { this.headers[name] = value; }
  flushHeaders() {}
  write(chunk) { if (this.destroyed) throw new Error('closed'); this.chunks.push(String(chunk)); return true; }
  end(chunk = '') { this.chunks.push(String(chunk)); this.writableEnded = true; }
  body() { return this.chunks.join(''); }
}

function setup() {
  const routes = new Map(), listeners = new Map(), cleanups = [];
  const clock = new FakeClock();
  const ctx = {
    webServer: {register(route) { routes.set(route.path, route.handler); return () => routes.delete(route.path); }},
    connection: {requestRejection: () => undefined},
    sessions: {list: () => [], get: () => null},
    sessionController: {inspect: async () => ({events: []}), list: async () => ({items: []})},
    sessionProjections: {snapshot: () => ({values: {}})},
    agents: {get: () => null},
    get: () => undefined,
    on(name, listener) { listeners.set(name, listener); },
    effect(effect) { const cleanup = effect(); if (cleanup) cleanups.push(cleanup); },
    inject(_dependencies, callback) {
      callback({jobs: {onJobsChanged: () => () => {}, list: () => []}, effect: effect => { const cleanup = effect(); if (cleanup) cleanups.push(cleanup); }});
    },
  };
  apply(ctx, {generation: 'generation-1', token: 't'.repeat(64)}, clock);
  return {routes, listeners, cleanups, clock};
}

function request(method, accept) {
  const req = new EventEmitter();
  req.method = method;
  req.headers = {'x-dsh-work-token': 't'.repeat(64)};
  if (accept !== undefined) req.headers.accept = accept;
  return req;
}

test('pet activity streams a snapshot, merges changes for 100 ms, pings, and stops after close', async () => {
  const {routes, listeners, clock, cleanups} = setup();
  const handler = routes.get('/__dshwork/pet-activity');
  const req = request('GET', 'text/event-stream');
  const res = new Response();
  await handler(req, res);
  assert.equal(res.status, 200);
  assert.equal(res.headers['Content-Type'], 'text/event-stream');
  assert.equal(res.headers['Cache-Control'], 'no-store');
  assert.match(res.body(), /^event: snapshot\ndata: .*"generation":"generation-1".*\n\n$/);
  assert.equal((res.body().match(/event: snapshot/g) ?? []).length, 1);

  listeners.get('api-session/status')('session-1', true);
  listeners.get('api-session/status')('session-1', false);
  await clock.tick(99);
  assert.equal((res.body().match(/event: snapshot/g) ?? []).length, 1);
  await clock.tick(1);
  assert.equal((res.body().match(/event: snapshot/g) ?? []).length, 2);
  assert.match(res.body(), /"running":false/);

  await clock.tick(15000);
  assert.match(res.body(), /: ping\n\n/);
  const beforeClose = res.body();
  res.destroyed = true;
  req.emit('close');
  listeners.get('api-session/status')('session-1', true);
  await clock.tick(15000);
  assert.equal(res.body(), beforeClose);
  for (const cleanup of cleanups) cleanup();
});

test('pet activity requires an event-stream Accept and no longer accepts navigation POSTs', async () => {
  const {routes} = setup();
  const handler = routes.get('/__dshwork/pet-activity');
  const noAccept = new Response();
  await handler(request('GET'), noAccept);
  assert.equal(noAccept.status, 406);

  const post = new Response();
  await handler(request('POST', 'application/json'), post);
  assert.equal(post.status, 405);
});

test('pet activity client processes updates and responds with an empty object', async () => {
  const {routes} = setup();
  const handler = routes.get('/__dshwork/pet-activity-client');
  const req = Readable.from([Buffer.from(JSON.stringify({current: '', visible: false, sessions: []}))]);
  req.method = 'POST';
  req.headers = {origin: 'http://localhost', host: 'localhost'};
  const res = new Response();
  await handler(req, res);
  assert.equal(res.status, 200);
  assert.equal(res.body(), '{}');
});
