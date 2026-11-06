// Exercise the shipped carrier and the selected runtime's real WebServer methods.
import {createRequire} from 'node:module';
import {pathToFileURL} from 'node:url';
const [carrierPath, runtime] = process.argv.slice(2);
const require = createRequire(runtime);
const {Service} = await import(pathToFileURL(require.resolve('@deepseek-ai/cordis')));
const {default: Carrier} = await import(pathToFileURL(carrierPath));
const carrier = Object.create(Carrier.prototype);
Object.assign(carrier, {exact: new Map(), prefixes: new Map(), upgrades: new Map()});
let cancellations = 0, aborts = 0;
const warnings = [];
carrier.ctx = {
  effect() {},
  logger: {warn(error) { warnings.push(error.message); }},
};
const resource = request => {
  if (request.headers.get('if-none-match') === '"fixture"') return new Response(null, {status: 304, headers: {etag: '"fixture"'}});
  return new Response(`${request.method} ${new URL(request.url).pathname}${new URL(request.url).search}`, {status: 202, headers: {etag: '"fixture"', 'x-resource': 'yes'}});
};
carrier.register({kind: 'exact', path: '/resource', resource});
carrier.register({kind: 'prefix', path: '/prefix', resource});
carrier.register({kind: 'exact', path: '/handler', handler(req, res) {res.end('handler');}});
carrier.register({kind: 'exact', path: '/failure', resource() {throw new TypeError('secret-token-must-not-be-logged');}});
carrier.register({kind: 'exact', path: '/partial-failure', handler(req, res) {res.writeHead(200); res.flushHeaders(); res.write('partial'); throw new Error('secret');}});
carrier.register({kind: 'exact', path: '/stream', resource(request) {
  request.signal.addEventListener('abort', () => aborts++, {once: true});
  return new Response(new ReadableStream({
    start(controller) {controller.enqueue(new TextEncoder().encode('stream'));},
    cancel() {cancellations++;},
  }));
}});
carrier.register({kind: 'exact', path: '/state', handler(req, res) {res.end(JSON.stringify({cancellations, aborts, warnings}));}});
carrier.register({kind: 'exact', path: '/fallback-function', handler(req, res) {
  disposeFallback();
  disposeFallback = carrier.registerFallback((req, res) => res.end('function fallback'));
  res.end('changed');
}});
carrier.register({kind: 'exact', path: '/fallback-clear', handler(req, res) {disposeFallback(); res.end('cleared');}});
let disposeFallback = carrier.registerFallback({resource});
await carrier[Service.init]();
console.log('carrier-ready');
