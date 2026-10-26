// Drives the development workbench over the WebView2 DevTools Protocol, without
// touching the user's mouse, keyboard or window focus. Development builds open
// the port only when DSH_WORK_WEBVIEW_DEBUG_PORT is set (see
// internal/desktopapp/debugport_dev.go).
//
//   node scripts/dev-cdp.mjs eval  <shell|dsh> "<expression>"
//   node scripts/dev-cdp.mjs click <x> <y>          # coordinates in the DSH frame
//   node scripts/dev-cdp.mjs type  "<text>" [--enter]
//   node scripts/dev-cdp.mjs shot  <file.png> [x y w h]
//
// Set DSH_WORK_CDP_PORT when the port is not 9333.
import {writeFileSync} from 'node:fs';

const port = process.env.DSH_WORK_CDP_PORT || '9333';
const DSH_ORIGIN = /wails\.localhost:48217/;
// The shell draws a 40 px top bar above the DSH frame.
const FRAME_TOP = 40;

const [, , command, ...args] = process.argv;
const targets = await (await fetch(`http://127.0.0.1:${port}/json/list`)).json();
const page = targets.find(t => t.type === 'page' && !/pet/i.test(t.url));
if (!page) throw new Error('no workbench page; is the window created and the debug port set?');

const ws = new WebSocket(page.webSocketDebuggerUrl);
let nextId = 0;
const pending = new Map();
const events = [];
ws.onmessage = message => {
  const data = JSON.parse(message.data);
  if (data.id && pending.has(data.id)) { pending.get(data.id)(data); pending.delete(data.id); } else events.push(data);
};
await new Promise(resolve => { ws.onopen = resolve; });
const send = (method, params = {}) => new Promise(resolve => {
  const id = ++nextId;
  pending.set(id, resolve);
  ws.send(JSON.stringify({id, method, params}));
});
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));

await send('Runtime.enable');
await sleep(400);
const contexts = events.filter(e => e.method === 'Runtime.executionContextCreated').map(e => e.params.context);
const contextFor = which => which === 'dsh'
  ? [...contexts].reverse().find(c => DSH_ORIGIN.test(c.origin))?.id
  : contexts.find(c => !DSH_ORIGIN.test(c.origin) && c.auxData?.isDefault)?.id;

async function evaluate(which, expression) {
  const contextId = contextFor(which);
  if (contextId === undefined) throw new Error(`no ${which} execution context`);
  const result = await send('Runtime.evaluate', {expression, contextId, awaitPromise: true, returnByValue: true});
  if (result.result?.exceptionDetails) throw new Error(JSON.stringify(result.result.exceptionDetails));
  return result.result?.result?.value;
}

switch (command) {
  case 'eval':
    console.log(JSON.stringify(await evaluate(args[0], args[1]), null, 1));
    break;
  case 'click': {
    const [x, y] = args.map(Number);
    for (const type of ['mouseMoved', 'mousePressed', 'mouseReleased']) {
      await send('Input.dispatchMouseEvent', {type, x, y: y + FRAME_TOP, button: 'left', clickCount: 1});
    }
    break;
  }
  case 'type':
    await evaluate('dsh', "document.querySelector('[contenteditable=true]')?.focus()");
    await send('Input.insertText', {text: args[0]});
    if (args.includes('--enter')) {
      for (const type of ['keyDown', 'keyUp']) await send('Input.dispatchKeyEvent', {type, key: 'Enter', code: 'Enter', windowsVirtualKeyCode: 13});
    }
    break;
  case 'shot': {
    const [file, x, y, width, height] = args;
    const clip = x === undefined ? undefined : {x: +x, y: +y, width: +width, height: +height, scale: 1};
    const shot = await send('Page.captureScreenshot', {format: 'png', ...(clip ? {clip} : {})});
    writeFileSync(file, Buffer.from(shot.result.data, 'base64'));
    break;
  }
  default:
    console.error('usage: dev-cdp.mjs eval|click|type|shot ...');
    process.exit(2);
}
process.exit(0);
