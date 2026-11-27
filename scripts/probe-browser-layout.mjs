// Exercise an already-open, isolated ready Browser slot through the real DSH UI.
// No launch/navigation/model/permission calls. Restores its sidebar width/open state.
// Usage: node scripts/probe-browser-layout.mjs <port> <output.json>
// Native HWND visibility/pixels remain a separate OS check; State is its ACK.
import {readFileSync,writeFileSync} from 'node:fs';
import assert from 'node:assert/strict';
const [port='9333',output='bin/browser-layout.json']=process.argv.slice(2);
const targets=await (await fetch(`http://127.0.0.1:${port}/json/list`)).json();
const target=targets.find(t=>t.type==='page'&&!/pet/i.test(t.url));
assert(target,'Workbench target required');
const ws=new WebSocket(target.webSocketDebuggerUrl),pending=new Map(),contexts=[];let id=0;
ws.onmessage=event=>{const m=JSON.parse(event.data);if(m.id){const p=pending.get(m.id);if(p){pending.delete(m.id);m.error?p.reject(Error(JSON.stringify(m.error))):p.resolve(m.result)}}else if(m.method==='Runtime.executionContextCreated')contexts.push(m.params.context)};
await new Promise((resolve,reject)=>{ws.onopen=resolve;ws.onerror=reject});
const send=(method,params={})=>new Promise((resolve,reject)=>{const key=++id;pending.set(key,{resolve,reject});ws.send(JSON.stringify({id:key,method,params}))});
const pause=ms=>new Promise(resolve=>setTimeout(resolve,ms));
const main=(await send('Page.getFrameTree')).frameTree.frame.id;
await send('Runtime.enable');await pause(200);
const shell=contexts.find(c=>c.auxData?.frameId===main&&c.auxData?.isDefault)?.id;
const dsh=contexts.find(c=>/wails\.localhost:48217/.test(c.origin)&&c.auxData?.isDefault)?.id;
assert(shell&&dsh,'Shell and DSH contexts required');
const evaluate=async(contextId,expression)=>{const r=await send('Runtime.evaluate',{contextId,expression,awaitPromise:true,returnByValue:true});if(r.exceptionDetails)throw Error(JSON.stringify(r.exceptionDetails));return r.result.value};
const generated=readFileSync(new URL('../frontend/bindings/github.com/local/dsh-work/internal/desktopclient/embeddedbrowserservice.ts',import.meta.url),'utf8');
const stateId=Number(generated.match(/export function State\([^]*?\$Call\.ByID\((\d+)/)?.[1]);assert(stateId);
const report={port:Number(port),samples:[]};let original,installed=false;
async function snapshot(){return evaluate(shell,`(async()=>{const frame=document.querySelector('iframe'),token=decodeURIComponent(new URL(frame.src).hash.slice(1).match(/(?:^|&)__dsh_work_doc=([^&]+)/)?.[1]||'');const {Call}=await import('/wails/runtime.js');const rows=[];for(const slot of __browserLayoutProbe.slots){try{rows.push({slot,state:await Call.ByID(${stateId},token,slot)})}catch(error){rows.push({slot,error:String(error)})}}return {at:performance.now(),rows,updates:__browserLayoutProbe.updates.slice()}})()`)}
const until=async(label,fn)=>{const deadline=Date.now()+6000;while(Date.now()<deadline){const value=await fn();if(value)return value;await pause(30)}throw Error(label+' deadline')};
const mouse=(type,x,y)=>send('Input.dispatchMouseEvent',{type,x,y,button:'left',buttons:type==='mouseReleased'?0:1,clickCount:1});
async function drag(from,to){await mouse('mousePressed',from.x,from.y);try{for(let n=1;n<=32;n++){await mouse('mouseMoved',from.x+(to.x-from.x)*n/32,from.y);await pause(12);if(n%4===0)report.samples.push(await snapshot())}}finally{await mouse('mouseReleased',to.x,from.y)}await pause(150)}
try{
 await evaluate(shell,`(()=>{if(globalThis.__browserLayoutProbe)throw Error('Probe already installed');const frame=document.querySelector('iframe'),probe={slots:new Set(),updates:[]};probe.listen=e=>{if(e.source!==frame.contentWindow||e.data?.type!=='dsh-work/browser')return;const m=e.data;if(typeof m.slot==='string')probe.slots.add(m.slot);if(m.action==='update')probe.updates.push({at:performance.now(),slot:m.slot,visible:m.visible,rect:m.rect})};addEventListener('message',probe.listen);globalThis.__browserLayoutProbe=probe;return true})()`);installed=true;
 report.before=await until('existing slot polling',async()=>{const value=await snapshot();return value.rows.length?value:undefined});const ready=report.before.rows.filter(r=>r.state?.state==='ready'&&r.state.visible);assert.equal(ready.length,1,'Exactly one isolated ready browser slot required');report.identity=ready[0];
 original=await evaluate(dsh,`(()=>{const panel=[...document.querySelectorAll('[data-sidebar-right-panel][data-sidebar-right-open]')].find(e=>e.getClientRects().length),handle=document.querySelector('[data-side="rightbar"]');if(!panel||!handle)throw Error('Expanded dock resize control required');const r=handle.getBoundingClientRect();return{width:panel.getBoundingClientRect().width,x:r.x+r.width/2,y:r.y+Math.min(100,r.height/2)}})()`);
 const offset=await evaluate(shell,`(()=>{const r=document.querySelector('iframe').getBoundingClientRect();return{x:r.left,y:r.top}})()`);
 const from={x:original.x+offset.x,y:original.y+offset.y},to={x:from.x+96,y:from.y};
 await drag(from,to);report.resized=await snapshot();
 const widths=new Set(report.samples.flatMap(s=>s.rows.filter(r=>r.slot===report.identity.slot&&r.state?.visible).map(r=>r.state.bounds?.Width??r.state.bounds?.width)));
 widths.delete(undefined);assert(widths.size>=3,'Native ACK bounds must change during drag, not just after release');
 await drag(to,from);
 const clicked=await evaluate(dsh,`(()=>{const b=[...document.querySelectorAll('[data-sidebar-right-toggle]')].find(e=>e.getClientRects().length&&!e.disabled);if(!b)return false;b.click();return performance.now()})()`);assert(clicked,'Collapse action required');
 report.hidden=await until('native Hide ACK',async()=>{const s=await snapshot();return s.rows.find(r=>r.slot===report.identity.slot&&r.state&&!r.state.visible)?s:undefined});
 await pause(500);report.hiddenSettled=await snapshot();assert.equal(report.hiddenSettled.rows.find(r=>r.slot===report.identity.slot)?.state.visible,false,'Late update revived collapsed native view');
 await evaluate(dsh,`(()=>{const b=document.querySelector('[data-sidebar-right-expand]');if(!b||b.disabled)throw Error('Expand action required');b.click();return true})()`);
 report.reopened=await until('same page visible',async()=>{const s=await snapshot();return s.rows.find(r=>r.slot===report.identity.slot&&r.state?.visible)?s:undefined});
 const after=report.reopened.rows.find(r=>r.slot===report.identity.slot).state;
 for(const field of ['target','document','lease','brokerGeneration'])if(report.identity.state[field]!==undefined)assert.equal(after[field],report.identity.state[field],field+' changed on collapse/reopen');
 report.passed=true;
}catch(error){report.error=String(error);process.exitCode=1}
finally{
 try{if(original){await evaluate(dsh,`(()=>{const b=document.querySelector('[data-sidebar-right-expand]');if(b&&!b.disabled&&b.getClientRects().length)b.click();return true})()`);await pause(400);const current=await evaluate(dsh,`(()=>{const p=[...document.querySelectorAll('[data-sidebar-right-panel][data-sidebar-right-open]')].find(e=>e.getClientRects().length),h=document.querySelector('[data-side="rightbar"]'),r=h?.getBoundingClientRect();return{width:p?.getBoundingClientRect().width,x:r?.x+r?.width/2,y:r?.y+Math.min(100,r?.height/2)}})()`);if(Math.abs(current.width-original.width)>2){const offset=await evaluate(shell,`(()=>{const r=document.querySelector('iframe').getBoundingClientRect();return{x:r.x,y:r.y}})()`),x=current.x+offset.x,y=current.y+offset.y,end=x+current.width-original.width;await mouse('mousePressed',x,y);try{for(let n=1;n<=16;n++){await mouse('mouseMoved',x+(end-x)*n/16,y);await pause(16)}}finally{await mouse('mouseReleased',end,y)}await pause(200)}report.restoredWidth=await evaluate(dsh,`(()=>{const p=[...document.querySelectorAll('[data-sidebar-right-panel][data-sidebar-right-open]')].find(e=>e.getClientRects().length);return p?.getBoundingClientRect().width})()`);if(Math.abs(report.restoredWidth-original.width)>2)throw Error('Sidebar width was not restored')}}catch(error){report.restoreError=String(error);process.exitCode=1}
 if(installed)await evaluate(shell,`(()=>{removeEventListener('message',__browserLayoutProbe.listen);delete globalThis.__browserLayoutProbe;return true})()`);
 writeFileSync(output,JSON.stringify(report,null,2));ws.close();console.log(JSON.stringify({output,passed:report.passed,error:report.error,restoreError:report.restoreError}));
}


