import {SOURCE_IDENTITY} from './protocol.js';
import {HOST_NAME, PROTOCOL} from './protocol.js';
import {createRoutes} from './routes.js';

let port, opening, connection, candidate, failure, routes;
const pending = new Map();
const rejectAll = message => { for (const p of pending.values()) p.reject(Error(message)); pending.clear(); };
function drop() { routes?.disconnect(); routes = undefined; connection = undefined; candidate = undefined; port = undefined; rejectAll('BROWSER_NATIVE_DISCONNECTED'); }
async function connect() {
  if (connection && port) return;
  if (opening) return opening;
  opening = (async () => {
    const stored = await chrome.storage.local.get('instance');
    const instance = stored.instance ?? crypto.randomUUID().replaceAll('-', '');
    if (!stored.instance) await chrome.storage.local.set({instance});
    const p = chrome.runtime.connectNative(HOST_NAME); port = p;
    const manager=createRoutes(message=>p.postMessage(message));routes=manager;
    await new Promise((resolve, reject) => {
      const timer = setTimeout(() => { if (port === p) { drop(); p.disconnect(); } reject(Error('BROWSER_HOST_TIMEOUT')); }, 6000);
      p.onMessage.addListener(m => {
        if (port !== p) return;
        if (['open','wire','close'].includes(m.kind)) { manager.receive(m).catch(console.error); return; }
        if (m.kind === 'welcome') {
          if(m.protocol!==PROTOCOL||m.hostVersion!=='0.2.0'||m.wireProtocol!==2){clearTimeout(timer);failure='BROWSER_UPDATE_REQUIRED';reject(Error(failure));p.disconnect();return;}
          clearTimeout(timer); failure = undefined; connection = m.connection; resolve();
        }
        else if (m.kind === 'error') { clearTimeout(timer); failure = m.error; reject(Error(m.error)); }
        else { const task = pending.get(m.id); if (task) { pending.delete(m.id); m.error ? task.reject(Error(m.error)) : task.resolve(m); } }
      });
      p.onDisconnect.addListener(() => {
        manager.disconnect();
        const message = chrome.runtime.lastError?.message ?? 'BROWSER_NATIVE_DISCONNECTED';
        clearTimeout(timer); if (port === p) drop(); reject(Error(message));
        chrome.alarms.create('native-reconnect', {delayInMinutes: 0.5});
      });
      p.postMessage({kind: 'hello', source:SOURCE_IDENTITY, protocol: PROTOCOL, wireProtocol:2, extensionId: chrome.runtime.id, extensionVersion: chrome.runtime.getManifest().version, instance});
    });
  })();
  try { await opening; } finally { opening = undefined; }
}
async function request(kind, extra = {}) {
  await connect();
  if (pending.size >= 32) throw Error('BROWSER_BUSY');
  return new Promise((resolve, reject) => {
    const id = crypto.randomUUID();
    const timer = setTimeout(() => { pending.delete(id); reject(Error('BROWSER_HOST_TIMEOUT')); }, 5000);
    const finish = fn => value => { clearTimeout(timer); fn(value); };
    pending.set(id, {resolve: finish(resolve), reject: finish(reject)});
    try { port.postMessage({id, kind, ...extra}); } catch (e) { pending.delete(id); clearTimeout(timer); reject(e); }
  });
}
chrome.runtime.onMessage.addListener((m, sender, reply) => {
  if (sender.id !== chrome.runtime.id || sender.url !== chrome.runtime.getURL('popup.html')) return false;
  if (!['status', 'bind', 'cancel', 'reconnect'].includes(m.kind)) return false;
  (async () => {
    if (failure && m.kind !== 'reconnect') throw Error(failure);
    if (m.kind === 'status') { const r = await request('status'); if (r.status?.id || candidate && Date.now() >= candidate.expires) candidate = undefined; return {...r, candidate: candidate?.id}; }
    if (m.kind === 'bind') { const r = await request('bind'); candidate = {id: r.candidate, expires: r.expires}; return r; }
    if (m.kind === 'cancel') { const r = await request('cancel', {candidate: candidate?.id}); candidate = undefined; return r; }
    if (m.kind === 'reconnect') { const old = port; failure = undefined; drop(); old?.disconnect(); await connect(); return {kind: 'connected'}; }
    throw Error('BROWSER_SETUP_OPERATION_REJECTED');
  })().then(reply, e => reply({error: e.message}));
  return true;
});
chrome.alarms.onAlarm.addListener(a => { if (a.name === 'native-reconnect') connect().catch(() => {}); });
chrome.runtime.onStartup.addListener(() => connect().catch(() => {}));
connect().catch(() => {});
