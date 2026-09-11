// Launch-only carrier for the published DSH webServer service. Route matching,
// index injection, authentication and plugin ownership remain upstream-owned.
import {createRequire} from 'node:module';
import {readFileSync} from 'node:fs';
import {pathToFileURL} from 'node:url';
import {join} from 'node:path';
import {createServer} from 'node:http';
import {connect} from 'node:net';
import {createHmac, timingSafeEqual} from 'node:crypto';

const config = JSON.parse(readFileSync(new URL('./connection.json', import.meta.url), 'utf8'));
const require = createRequire(join(config.profileRoot, 'package.json'));
const {WebServer} = await import(pathToFileURL(require.resolve('@deepseek-ai/dsh-host-webserver')).href);
const {Service} = await import(pathToFileURL(require.resolve('@deepseek-ai/cordis')).href);

export default class PipeWebServer extends WebServer {
  get port() { return 1; }
  async [Service.init]() {
    const sockets = new Set(), timers = new Set();
    let stopped = false;
    const server = createServer(async (req, res) => {
      try {
        const route = this.match(new URL(req.url, 'http://127.0.0.1:1').pathname);
        const handler = route?.handler ?? this.fallback;
        if (!handler) { res.writeHead(404).end(); return; }
        await handler(req, res);
      } catch {
        if (res.headersSent) res.destroy();
        else res.writeHead(500).end();
      }
    });
    server.on('upgrade', (req, socket, head) => {
      socket.on('error', () => socket.destroy());
      const route = this.upgrades.get(new URL(req.url, 'http://127.0.0.1:1').pathname);
      if (!route) { socket.destroy(); return; }
      Promise.resolve().then(() => route.handler(req, socket, head)).catch(() => socket.destroy());
    });
    const open = () => {
      if (stopped) return;
      const socket = connect(config.pipe);
      sockets.add(socket);
      socket.setTimeout(3000, () => socket.destroy());
      socket.on('error', () => socket.destroy());
      socket.once('close', () => {
        sockets.delete(socket);
        if (!stopped) { const timer = setTimeout(() => { timers.delete(timer); open(); }, 100); timers.add(timer); }
      });
      let bytes = Buffer.alloc(0), nonce;
      const mac = role => createHmac('sha256', config.token).update(role).update(nonce).digest();
      const handshake = chunk => {
        bytes = Buffer.concat([bytes, chunk]);
        if (!nonce && bytes.length >= 32) {
          nonce = bytes.subarray(0,32); bytes = bytes.subarray(32);
          socket.write(mac('worker'));
        }
        if (nonce && bytes.length >= 32) {
          if (!timingSafeEqual(bytes.subarray(0,32),mac('host'))) { socket.destroy(); return; }
          socket.pause();
          socket.off('data',handshake);
          socket.setTimeout(0);
          const rest = bytes.subarray(32);
          if (rest.length) socket.unshift(rest);
          server.emit('connection',socket);
          socket.resume();
        }
      };
      socket.on('data',handshake);
    };
    for (let i=0;i<16;i++) open();
    this.ctx.effect(() => () => {
      stopped=true;
      for (const timer of timers) clearTimeout(timer);
      for (const socket of sockets) socket.destroy();
      server.closeAllConnections();
    }, 'dsh-work.pipe');
  }
}
