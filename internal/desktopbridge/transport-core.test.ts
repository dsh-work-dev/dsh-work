import assert from 'node:assert/strict';
import test from 'node:test';
import {createUploadWorkerAdapter, isGatewayRemoteMux, isUploadFileRoute, parseSessionExportRoute, sessionExportCommand, sessionExportDownloadRoute} from './transport-core';

const page = 'http://wails.localhost:48217/index.html';

function fakeWorker() {
  const calls: Array<{message: unknown; transfer?: Transferable[]}> = [];
  let terminations = 0;
  const worker = {
    onmessage: null as ((event: MessageEvent) => void) | null,
    onerror: null as ((event: ErrorEvent) => void) | null,
    postMessage(message: unknown, transfer?: Transferable[]) { calls.push({message, transfer}); },
    terminate() { terminations++; },
  } as unknown as Worker;
  return {worker, calls, get terminations() { return terminations; }};
}

test('selects only the same-origin Gateway mux and exact attachment upload route', () => {
  assert.equal(isGatewayRemoteMux('/api/remote.mux', page), true);
  assert.equal(isGatewayRemoteMux('/api/remote.mux?x=1', page), true);
  assert.equal(isGatewayRemoteMux('ws://wails.localhost:48217/api/remote.mux', page), true);
  assert.equal(isGatewayRemoteMux('/api/remote.mux/other', page), false);
  assert.equal(isGatewayRemoteMux('https://example.test/api/remote.mux', page), false);
  assert.equal(isGatewayRemoteMux('wss://wails.localhost:48217/api/remote.mux', page), false);
  assert.equal(isGatewayRemoteMux('wss://wails.localhost:48217/api/remote.mux', 'https://wails.localhost:48217/'), true);
  assert.equal(isUploadFileRoute('/api/session/uploadFileBinary?sessionId=s1', page), true);
  assert.equal(isUploadFileRoute('/api/session/uploadFileBinary/other', page), false);
  assert.equal(isUploadFileRoute('https://example.test/api/session/uploadFileBinary', page), false);
});

test('adapts only the DSH upload worker route and relays stream progress and completion', async () => {
  const native = fakeWorker();
  const events: unknown[] = [];
  let completed!: () => void;
  const completion = new Promise<void>(resolve => { completed = resolve; });
  let call: {input: RequestInfo | URL; init?: RequestInit & {uploadTotal?: number; onUploadProgress?: (progress: {loaded: number; total?: number}) => void}} | undefined;
  const adapter = createUploadWorkerAdapter(native.worker, async (input, init) => {
    call = {input, init};
    init?.onUploadProgress?.({loaded: 5, total: init.uploadTotal});
    return new Response('stored', {status: 200});
  }, page);
  adapter.onmessage = event => {
    events.push(event.data);
    if ((event.data as {kind?: string}).kind === 'complete') completed();
  };
  const body = new Blob(['12345']);
  adapter.postMessage({
    url: 'http://wails.localhost:48217/api/session/uploadFileBinary?sessionId=s1',
    headers: {'content-type': 'application/octet-stream'},
    body,
  });
  await completion;
  assert.equal(native.terminations, 1);
  assert.equal(call?.input, 'http://wails.localhost:48217/api/session/uploadFileBinary?sessionId=s1');
  assert.equal(call?.init?.method, 'POST');
  assert.equal(call?.init?.credentials, 'include');
  assert.equal(call?.init?.body, body);
  assert.deepEqual(events, [
    {kind: 'progress', loaded: 5, total: 5},
    {kind: 'complete', status: 200, body: 'stored'},
  ]);
});

test('delegates non-upload Worker messages and aborts the stream when terminated', async () => {
  const native = fakeWorker();
  const delegated = {url: '/other', body: new Blob(['x'])};
  const transfer: Transferable[] = [];
  let signal: AbortSignal | undefined;
  const adapter = createUploadWorkerAdapter(native.worker, async (_input, init) => {
    signal = init?.signal ?? undefined;
    await new Promise<never>((_resolve, reject) => signal?.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError')), {once: true}));
    throw new Error('unreachable');
  }, page);
  adapter.postMessage(delegated, transfer);
  assert.deepEqual(native.calls, [{message: delegated, transfer}]);
  adapter.postMessage({
    url: '/api/session/uploadFileBinary?sessionId=s1',
    headers: {},
    body: new Blob(['x']),
  });
  assert.ok(signal);
  adapter.terminate();
  assert.equal(signal?.aborted, true);
  assert.equal(native.terminations, 1);
});

test('runs the native Worker methods on the Worker itself', () => {
  // Native Worker methods throw "Illegal invocation" unless `this` is the Worker.
  const listeners: string[] = [];
  const worker = {
    onmessage: null,
    postMessage() {},
    terminate() {},
    addEventListener(this: unknown, type: string) {
      if (this !== worker) throw new TypeError('Illegal invocation');
      listeners.push(type);
    },
  } as unknown as Worker;
  const adapter = createUploadWorkerAdapter(worker, async () => new Response(''), page);
  adapter.addEventListener('message', () => {});
  assert.deepEqual(listeners, ['message']);
});

test('recognizes only the same-origin DSH session export route', () => {
  const route = parseSessionExportRoute('/api/session.export?sessionId=s%2F1&includeDescendants=true', page);
  assert.deepEqual(route, {sessionId: 's/1', includeDescendants: true});
  assert.equal(sessionExportCommand(route!), '/__work/session-export?sessionId=s%2F1&includeDescendants=true');
  assert.equal(parseSessionExportRoute('/api/session.export/other?sessionId=s1', page), undefined);
  assert.equal(parseSessionExportRoute('https://example.test/api/session.export?sessionId=s1', page), undefined);
  assert.equal(parseSessionExportRoute('/api/session.export?sessionId=s1&sessionId=s2', page), undefined);
  assert.equal(parseSessionExportRoute('/api/session.export?sessionId=s1&includeDescendants=yes', page), undefined);
  assert.equal(parseSessionExportRoute('/api/session.export', page), undefined);
  assert.deepEqual(sessionExportDownloadRoute('/api/session.export?sessionId=s1', 'dsh-session-s1.zip', page), {sessionId: 's1', includeDescendants: false});
  assert.equal(sessionExportDownloadRoute('/api/session.export?sessionId=s1', '', page), undefined);
});