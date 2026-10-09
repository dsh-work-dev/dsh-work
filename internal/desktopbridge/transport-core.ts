export type UploadProgress = {loaded: number; total?: number};
export type StreamRequestInit = RequestInit & {
  duplex?: 'half';
  uploadTotal?: number;
  onUploadProgress?: (progress: UploadProgress) => void;
};
export type StreamFetch = (input: RequestInfo | URL, init?: StreamRequestInit) => Promise<Response>;

type DSHUploadRequest = {
  url?: string | URL;
  headers?: Record<string, string>;
  body?: Blob | ReadableStream<Uint8Array>;
};

export function isGatewayRemoteMux(raw: string | URL, pageHref: string): boolean {
  try {
    const page = new URL(pageHref);
    const target = new URL(raw, page);
    const targetOrigin = target.origin.replace(/^ws:/, 'http:').replace(/^wss:/, 'https:');
    return target.username === '' && target.password === '' && targetOrigin === page.origin && target.pathname === '/api/remote.mux';
  } catch {
    return false;
  }
}

export function isUploadFileRoute(raw: string | URL, pageHref: string): boolean {
  try {
    const page = new URL(pageHref);
    const target = new URL(raw, page);
    return target.origin === page.origin && target.pathname === '/api/session/uploadFileBinary';
  } catch {
    return false;
  }
}

// DSH's built-in file-upload Worker emits XHR-compatible progress messages and
// terminates that Worker when its AbortSignal fires. Keep that message contract
// while carrying only the upload route over the Wails byte stream.
export function createUploadWorkerAdapter(worker: Worker, streamFetch: StreamFetch, pageHref: string): Worker {
  let controller: AbortController | undefined;
  let workerTerminated = false;
  const stopWorker = () => {
    if (workerTerminated) return;
    workerTerminated = true;
    worker.terminate();
  };
  const emit = (data: unknown) => {
    const event = new MessageEvent('message', {data});
    if (typeof worker.dispatchEvent === 'function') worker.dispatchEvent(event);
    else worker.onmessage?.call(worker, event);
  };
  const postMessage = (message: unknown, transfer?: Transferable[] | StructuredSerializeOptions) => {
    const request = message as DSHUploadRequest;
    if (typeof request?.url !== 'string' && !(request?.url instanceof URL)) {
      worker.postMessage(message, transfer as Transferable[]);
      return;
    }
    if (!isUploadFileRoute(request.url, pageHref)) {
      worker.postMessage(message, transfer as Transferable[]);
      return;
    }
    stopWorker();
    if (!(request.body instanceof Blob) && !(request.body instanceof ReadableStream)) {
      emit({kind: 'error', message: 'background upload worker received an invalid body'});
      return;
    }
    if (controller) {
      emit({kind: 'error', message: 'background upload worker already has an active request'});
      return;
    }
    const active = new AbortController();
    controller = active;
    const uploadTotal = request.body instanceof Blob ? request.body.size : undefined;
    const init: StreamRequestInit = {
      method: 'POST',
      headers: request.headers ?? {},
      credentials: 'include',
      body: request.body,
      signal: active.signal,
      ...(request.body instanceof ReadableStream ? {duplex: 'half'} : {}),
      ...(uploadTotal === undefined ? {} : {uploadTotal}),
      onUploadProgress: progress => emit({kind: 'progress', ...progress}),
    };
    void streamFetch(request.url, init)
      .then(async response => {
        const body = await response.text();
        if (!active.signal.aborted) emit({kind: 'complete', status: response.status, body});
      })
      .catch(error => {
        if (!active.signal.aborted) emit({kind: 'error', message: error instanceof Error ? error.message : String(error)});
      })
      .finally(() => {
        if (controller === active) controller = undefined;
      });
  };
  return new Proxy(worker, {
    get(target, property) {
      if (property === 'postMessage') return postMessage;
      if (property === 'terminate') return () => {
        controller?.abort();
        controller = undefined;
        stopWorker();
      };
      return Reflect.get(target, property, target);
    },
    set(target, property, value) {
      return Reflect.set(target, property, value, target);
    },
  });
}
