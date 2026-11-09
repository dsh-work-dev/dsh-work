// Read-only inventory of the real development UI. Does not launch an app.
// Usage: node scripts/e67-ui-cdp.mjs [9333|9343]
// Screenshots from dev-cdp render WebView2 only; CEF child pixels require the
// admitted CEF/MCP path and must not be inferred from a shell screenshot.
import {execFileSync} from 'node:child_process';
import {readFileSync} from 'node:fs';
// Use the same generated IDs over Wails' documented runtime module, which is
// available in both Vite and bundled shells. Never expose another Host route.
const generated=readFileSync(new URL('../frontend/bindings/github.com/local/dsh-work/internal/desktopclient/embeddedbrowserservice.ts',import.meta.url),'utf8');
const methodId=name=>{const match=generated.match(new RegExp('export function '+name+'\\([^]*?\\$Call\\.ByID\\((\\d+)'));if(!match)throw Error('Generated binding unavailable: '+name);return Number(match[1])};
const ids={Capability:methodId('Capability'),State:methodId('State')};
const port=process.argv[2]||'9333';
if(!/^\d+$/.test(port))throw Error('numeric CDP port required');
const env={...process.env,DSH_WORK_CDP_PORT:port};
function evaluate(which,expression){
 const output=execFileSync(process.execPath,['scripts/dev-cdp.mjs','eval',which,expression],{env,encoding:'utf8',timeout:20000});
 return JSON.parse(output);
}
const shell= evaluate('shell',`(async()=>{
 const frame=document.querySelector('iframe');
 if(!frame)return{error:'DSH frame unavailable'};
 const seen=new Map();
 const listener=event=>{if(event.source!==frame.contentWindow)return;const m=event.data;if(m?.version===1&&m.type==='dsh-work/browser'&&typeof m.slot==='string')seen.set(m.slot,m.action)};
 addEventListener('message',listener);try{
 await new Promise(resolve=>setTimeout(resolve,1800));
 const {Call}=await import('/wails/runtime.js');const ids=${JSON.stringify(ids)};const EmbeddedBrowserService={Capability:()=>Call.ByID(ids.Capability),State:(...args)=>Call.ByID(ids.State,...args)};
 const capability=await EmbeddedBrowserService.Capability();const token=decodeURIComponent(new URL(frame.src).hash.slice(1).match(/(?:^|&)__dsh_work_doc=([^&]+)/)?.[1]||'');const slots=[];
 for(const [slot,lastAction] of seen){try{slots.push({slot,lastAction,state:await EmbeddedBrowserService.State(token,slot)})}catch(error){slots.push({slot,error:String(error)})}}
 return{url:location.href,frame:frame.src,capability,slots,settingsVisible:!document.getElementById('manager-surface')?.hidden,frameBounds:frame.getBoundingClientRect().toJSON()};
 }finally{removeEventListener('message',listener)}
})()`);
let dsh;
try {dsh=evaluate('dsh',`({url:location.href,provider:globalThis.__DSH_WORK_BROWSER_PROVIDER__?.protocolVersion,version:globalThis.__DSH_WORK_EMBEDDED_VERSION__,modals:[...document.querySelectorAll('[role="dialog"][aria-modal="true"], [data-shortcut-modal][role="dialog"], dialog[open]')].map(e=>({role:e.getAttribute('role'),ready:e.hasAttribute('data-dsh-work-modal-ready'),hidden:e.hidden,visibility:getComputedStyle(e).visibility,bounds:e.getBoundingClientRect().toJSON()})),pendingMasks:document.querySelectorAll('[data-dsh-work-modal-pending]').length})`)}catch(error){dsh={error:String(error)}}
console.log(JSON.stringify({port:Number(port),shell,dsh},null,2));
