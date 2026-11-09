// Fixed, one-shot verification of the original Linux Wails/DSH UI over its
// standard WebKit inspector. No model/permission calls or product test endpoint.
import {readFileSync,writeFileSync,existsSync,mkdirSync} from 'node:fs';
import {resolve,join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {runLinuxBrowserAgent} from './linux-browser-agent-fixture.mjs';
const [base,fixture,directory,mode]=process.argv.slice(2);
if(mode&&!['--boot-diagnostic','--dual-peer','--dual-reopen'].includes(mode))throw Error('Unknown fixed probe mode');
if(process.platform!=='linux'||!/^http:\/\/127\.0\.0\.1:\d+$/.test(base??'')||!/^http:\/\/127\.0\.0\.1:\d+\/$/.test(fixture??'')||!directory)throw Error('Expected isolated Linux inspector, local fixture and output directory');
const out=resolve(directory);mkdirSync(out,{recursive:true});
const repo=fileURLToPath(new URL('../',import.meta.url));
const id=(file,name)=>{const source=readFileSync(join(repo,'frontend/bindings/github.com/local/dsh-work/internal/desktopclient',file+'.ts'),'utf8');const match=source.match(new RegExp('export function '+name+'\\([^]*?\\$Call\\.ByID\\((\\d+)'));if(!match)throw Error('binding missing '+name);return Number(match[1]);};
const ids={cap:id('embeddedbrowserservice','Capability'),state:id('embeddedbrowserservice','State'),exit:id('embeddedbrowserservice','ExitUI'),admission:id('embeddedbrowserservice','RegisterWindow'),quit:id('hostservice','Quit')};
const delay=ms=>new Promise(r=>setTimeout(r,ms));
async function until(label,fn,ms=30000){const deadline=Date.now()+ms;let last;while(Date.now()<deadline){try{const v=await fn();if(v)return v}catch(e){last=e}await delay(100)}throw Error(label+': '+(last?.message??'bounded deadline'));}
function save(name,value){writeFileSync(join(out,name+'.json'),JSON.stringify(value,null,2).replace(/([?&](?:token|auth)=)[^&#"\s]+/gi,'$1[REDACTED]')+'\n',{mode:0o600});}
class Inspector {
 constructor(url){this.pending=new Map();this.targets=new Map();this.parents=new Map();this.contexts=new Map();this.trace=[];this.messages=[];this.seq=0;this.ws=new WebSocket(url);this.ws.onmessage=e=>this.receive(JSON.parse(e.data));this.ws.onclose=()=>{for(const p of this.pending.values()){clearTimeout(p.timer);p.reject(Error('WebKit inspector disconnected'))}this.pending.clear()};}
 receive(m,target=''){
  this.trace.push({method:m.method??'reply',target,replyTo:m.id?this.pending.get(target+':'+m.id)?.method:undefined,resultKeys:m.result?Object.keys(m.result):undefined,error:m.error?.message});if(this.trace.length>150)this.trace.shift();
  if(m.method==='Console.messageAdded'){const c=m.params.message;this.messages.push({source:c.source,level:c.level,text:String(c.text).slice(0,1000)});if(this.messages.length>50)this.messages.shift();}
  if(m.method==='Target.dispatchMessageFromTarget'){this.receive(JSON.parse(m.params.message),m.params.targetId);return}
  if(m.method==='Target.targetCreated'){const t=m.params.targetInfo;if(!t.isProvisional&&['page','frame'].includes(t.type)){this.targets.set(t.targetId,t);this.parents.set(t.targetId,target);void this.initialize(t.targetId).catch(e=>this.trace.push({target:t.targetId,initializationError:String(e)}));}return}
  if(m.method==='Target.targetDestroyed'){this.targets.delete(m.params.targetId);for(const [key,c] of this.contexts)if(c.target===m.params.targetId)this.contexts.delete(key);return}
  if(m.method==='Runtime.executionContextCreated'){const c=m.params.context;if(c.isPageContext!==false)this.contexts.set(target+':'+c.id,{target,contextId:c.id});}
  if(m.id){const p=this.pending.get(target+':'+m.id);if(p){this.pending.delete(target+':'+m.id);clearTimeout(p.timer);m.error?p.reject(Error(m.error.message)):p.resolve(m.result);}}
 }
 async initialize(target){
  // Follow WebInspector's Inspector-first/initialized lifecycle. Some older
  // WebKit builds announce placeholder frame targets without Runtime agents.
  await this.send('Inspector.enable',{},target).catch(()=>{});
  await this.send('Page.enable',{},target).catch(()=>{});
  await this.send('Runtime.enable',{},target);
  // Context 0 is the documented default inspected-page context, not a claimed
  // frame identity. Every consumer still evaluates the actual document.
  this.contexts.set(target+':0',{target,contextId:0});
  await this.send('Console.enable',{},target).catch(()=>{});
  await this.send('Inspector.initialized',{},target).catch(()=>{});
 }
 async open(){await new Promise((yes,no)=>{this.ws.onopen=yes;this.ws.onerror=no});await this.send('Target.setPauseOnStart',{pauseOnStart:false}).catch(()=>{});}
 send(method,params={},target=''){
  const id=++this.seq,key=target+':'+id;
  return new Promise((resolve,reject)=>{const timer=setTimeout(()=>{this.pending.delete(key);reject(Error('Inspector timeout '+method))},12000);this.pending.set(key,{resolve,reject,timer,method});const value={id,method,params};if(target){const parent=this.parents.get(target)??'';if(parent===target){clearTimeout(timer);this.pending.delete(key);reject(Error('Cyclic inspector target'));return}void this.send('Target.sendMessageToTarget',{targetId:target,message:JSON.stringify(value)},parent).catch(e=>{const p=this.pending.get(key);if(p){clearTimeout(p.timer);this.pending.delete(key);p.reject(e)}})}else this.ws.send(JSON.stringify(value));});
 }
 async evaluate(where,expression){
  let r=await this.send('Runtime.evaluate',{expression:`Promise.resolve((${expression})).then(v=>JSON.stringify(v))`,contextId:where.contextId,returnByValue:false},where.target);
  if(r.wasThrown)throw Error(r.result?.description??'evaluate failed');
  r=await this.send('Runtime.awaitPromise',{promiseObjectId:r.result.objectId,returnByValue:true},where.target);
  if(r.wasThrown)throw Error(r.result?.description??'async evaluate failed');
  return r.result.value===undefined?undefined:JSON.parse(r.result.value);
 }
 async find(which){for(const c of this.contexts.values()){try{const value=await this.evaluate(c,`({shell:parent===window&&!!document.querySelector('iframe'),dsh:globalThis.__DSH_WORK_BROWSER_PROVIDER__?.protocolVersion===1})`);if(value?.[which])return c}catch{}}}
 close(){this.ws.close();}
}
let inspector,lastSlotStates;
try {
 if(mode==='--boot-diagnostic'){
  // One channel attempt at the checkpoint. If WebKit cannot list/connect its
  // own target, record that fact instead of looping on an unavailable channel.
  const html=await(await fetch(base,{signal:AbortSignal.timeout(3000)})).text();
  const path=html.match(/\/socket\/\d+\/\d+\/WebPage/)?.[0];
  save('boot-inspector-list',{hasWebPage:!!path,preview:html.slice(0,2000)});
  if(path){
   inspector=new Inspector(base.replace('http:','ws:')+path);await inspector.open();
   await until('boot execution context',async()=>inspector.contexts.size>0,3000);
   for(const target of inspector.targets.keys())await inspector.send('Console.enable',{},target).catch(()=>{});
   const pages=[];
   for(const where of inspector.contexts.values()){
    try{pages.push(await inspector.evaluate(where,`(()=>{const safe=u=>{try{const p=new URL(u,location.href);return p.protocol+'//'+p.host+p.pathname}catch{return String(u).slice(0,100)}};return{url:safe(location.href),origin:location.origin,top:parent===window,ready:document.readyState,title:document.title,body:document.body?.innerText.slice(0,2000),frames:[...document.querySelectorAll('iframe')].map(f=>({src:safe(f.src),origin:new URL(f.src).origin,documentPresent:!!f.contentDocument})),generation:globalThis.__WORK_GENERATION__,provider:globalThis.__DSH_WORK_BROWSER_PROVIDER__?.protocolVersion,embeddedVersion:globalThis.__DSH_WORK_EMBEDDED_VERSION__,boot:document.querySelector('[data-dsh-boot]')?.textContent.slice(0,1000),resources:performance.getEntriesByType('resource').slice(-30).map(r=>({url:safe(r.name),duration:r.duration,bytes:r.transferSize}))}})()`))}catch(error){pages.push({contextError:String(error)})}
   }
   save('boot-webview',{pages,console:inspector.messages,protocol:inspector.trace});
   const opaque=pages.find(p=>typeof p.url==='string'&&p.url.startsWith('wails://')&&p.origin==='null');
   if(opaque)save('boot-origin-unsafe',{reason:'Registered WebView document has an opaque browser origin',url:opaque.url,origin:opaque.origin});
  }
 }else{
 const path=await until('Wails inspectable page',async()=>{const html=await(await fetch(base,{signal:AbortSignal.timeout(2000)})).text();return html.match(/\/socket\/\d+\/\d+\/WebPage/)?.[0]});
 inspector=new Inspector(base.replace('http:','ws:')+path);await inspector.open();
 const shell=await until('shell execution context',()=>inspector.find('shell'));
 await inspector.evaluate(shell,`(()=>{globalThis.__e67Slots=new Set();addEventListener('message',e=>{if(e.source!==document.querySelector('iframe')?.contentWindow)return;const m=e.data;if(m?.version===1&&m.type==='dsh-work/browser'&&typeof m.slot==='string')__e67Slots.add(m.slot)});return true})()`);
 const dsh=await until('real DSH provider',()=>inspector.find('dsh'),60000);
 const shellOrigin=await inspector.evaluate(shell,`(()=>{const frame=document.querySelector('iframe');if(!frame)throw Error('Worker frame missing');let denied=false,error;try{void frame.contentWindow.document.body}catch(e){error=e.name;denied=e.name==='SecurityError'}return{origin:location.origin,denied,error}})()`);
 const workerOrigin=await inspector.evaluate(dsh,`(async()=>{let denied=false,error;try{void parent.document.body}catch(e){error=e.name;denied=e.name==='SecurityError'}const r=await fetch('/wails/runtime?object=0&method=0',{headers:{'x-wails-window-name':'settings','x-wails-window-id':'0'}});return{origin:location.origin,ancestor:location.ancestorOrigins?.[0],denied,error,privilegedRuntimeStatus:r.status}})()`);
 save('origin-isolation',{shell:shellOrigin,worker:workerOrigin});
 if(shellOrigin.origin!=='wails://localhost'||workerOrigin.origin!=='wails://wails.localhost:48217'||workerOrigin.ancestor!==shellOrigin.origin||!shellOrigin.denied||!workerOrigin.denied||workerOrigin.privilegedRuntimeStatus!==403)throw Error('Actual WebView origin isolation/privileged-route gate rejected; no browser actions permitted');
 const initial=await inspector.evaluate(shell,`(async()=>{const {Call}=await import('/wails/runtime.js');return{capability:await Call.ByID(${ids.cap})}})()`);
 if(!initial.capability)throw Error('formal embedded provider capability unavailable');
 save('provider-ready',initial);
 // The fresh profile has no workspace. DSH's New Session action then only
 // clears the current view; it cannot mount a session-scoped right sidebar.
 // Adopt only our own scratch directory through the existing browse picker.
 // Never send a prompt or manufacture a session/turn event to get a sidebar.
 const click=pattern=>inspector.evaluate(dsh,`(()=>{const r=new RegExp(${JSON.stringify(pattern)},'i');const b=[...document.querySelectorAll('button,[role="button"]')].find(e=>!e.disabled&&e.getClientRects().length&&r.test(e.getAttribute('aria-label')||e.getAttribute('title')||e.textContent.trim()));if(b){b.click();return true}return false})()`);
 const workspace=join(out,'workspace');mkdirSync(workspace,{recursive:true});
 const readSessionSelection=()=>inspector.evaluate(dsh,`(()=>{const rows=[...document.querySelectorAll('[role="treeitem"][data-row-key^="session:"]')].filter(e=>e.getClientRects().length).map(e=>({key:e.getAttribute('data-row-key'),selected:e.getAttribute('aria-selected')==='true',label:e.textContent.trim(),blankLabel:/^(New Session|新会话)$/.test(e.textContent.trim())}));const selected=rows.filter(e=>e.selected);return selected.length===1?{...selected[0],rows}:undefined})()`);
 // A new WebView can mount the provider before the shared Host workspace
 // projection hydrates. For peers the workspace is already known from B's
 // actual picker readback; absence in one DOM tick never means a fresh Host.
 if(mode?.startsWith('--dual-')){
  const expected=JSON.parse(readFileSync(join(out,'session.json'),'utf8'));
  if(mode==='--dual-peer'){
   const prepared=await until('shared own workspace row and real new-session action',()=>inspector.evaluate(dsh,`(()=>{const row=[...document.querySelectorAll('[role="treeitem"][data-row-key]')].find(e=>e.getAttribute('data-row-key')===${JSON.stringify(expected.workspace)}&&e.getClientRects().length);if(!row)return;const buttons=[...row.querySelectorAll('button')].filter(e=>!e.disabled&&/^(New session in |在“.*”中新建会话$)/.test(e.getAttribute('aria-label')||''));if(buttons.length!==1)return;const result={workspace:row.getAttribute('data-row-key'),label:row.textContent.trim(),action:buttons[0].getAttribute('aria-label')};buttons[0].click();return result})()`));
   save('workspace-reused',prepared);
  }else{
   // Existing trusted shell navigation refreshes and opens an existing real
   // Session. It neither creates a Session nor appends a turn/tool event.
   if(typeof expected.key!=='string'||!expected.key.startsWith('session:session-'))throw Error('Exact original A session missing');
   const opened=await inspector.evaluate(shell,`(()=>new Promise((resolve,reject)=>{const frame=document.querySelector('iframe'),origin=globalThis.__DSH_WORK_ORIGINS__?.origin(frame.src),id=crypto.randomUUID();if(!origin||origin==='null'||origin!==${JSON.stringify(workerOrigin.origin)})throw Error('Unsafe Worker origin');const timer=setTimeout(()=>{removeEventListener('message',receive);reject(Error('Existing session navigation acknowledgment deadline'))},5000);function receive(e){if(e.source!==frame.contentWindow||e.origin!==origin||e.data?.type!=='dsh-work/open-session-result'||e.data?.version!==1||e.data?.id!==id)return;clearTimeout(timer);removeEventListener('message',receive);resolve(e.data)}addEventListener('message',receive);frame.contentWindow.postMessage({version:1,type:'dsh-work/open-session',id,sessionId:${JSON.stringify(expected.key.slice(8))}},origin)}))()`);
   save('session-opened',opened);if(opened.error)throw Error('Original A session unavailable: '+opened.error);
  }
  const selected=await until('unique actual selected fixture session',async()=>{const value=await readSessionSelection();return mode!=='--dual-reopen'||value?.key===expected.key?value:undefined});
  save('session-selected',selected);
  if(mode==='--dual-reopen'&&selected.key!==expected.key)throw Error('Reopen selected a different session from original A');
 }else{

 await until('Choose workspace action',()=>click('^(Choose workspace|选择工作区)$'));
 await until('directory browse path editor',()=>click('^(Edit path|编辑路径)$'));
 await inspector.evaluate(dsh,`(()=>{const e=document.querySelector('input[aria-label="Edit path"],input[aria-label="编辑路径"]');if(!e)throw Error('Directory path input missing');Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(e,${JSON.stringify(workspace)});e.dispatchEvent(new Event('input',{bubbles:true}));return true})()`);
 await inspector.evaluate(dsh,`(()=>{const e=document.querySelector('input[aria-label="Edit path"],input[aria-label="编辑路径"]');e.dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',bubbles:true}));return true})()`);
 await until('directory navigation settled',()=>inspector.evaluate(dsh,`(()=>{const e=[...document.querySelectorAll('[role="dialog"] button')].find(e=>/^(Open|打开)$/.test(e.textContent.trim()));return !!e&&!e.disabled&&!e.closest('[role="dialog"]').querySelector('[role="alert"]')})()`));
 // Reopen the editor without changing it: its initial value is taken from
 // selected.path ?? parent.path, the same target as Open, not our typed draft.
 await until('read back landed directory',()=>click('^(Edit path|编辑路径)$'));
 const landing=await inspector.evaluate(dsh,`(()=>{const e=document.querySelector('input[aria-label="Edit path"],input[aria-label="编辑路径"]');return{path:e?.value,error:e?.closest('[role="dialog"]')?.querySelector('[role="alert"]')?.textContent||''}})()`);
 save('workspace-directory-landed',landing);
 if(landing.path?.replace(/\/$/,'')!==workspace||landing.error)throw Error('Directory picker did not land on the own workspace');
 await inspector.evaluate(dsh,`(()=>{document.querySelector('input[aria-label="Edit path"],input[aria-label="编辑路径"]').dispatchEvent(new KeyboardEvent('keydown',{key:'Escape',bubbles:true}));return true})()`);
 await until('Open own workspace directory',()=>click('^(Open|打开)$'));
 const workspaceRow=await until('actual workspace row',()=>inspector.evaluate(dsh,`(()=>{const rows=[...document.querySelectorAll('[role="treeitem"][data-row-key^="workspace:"]')].filter(e=>e.getClientRects().length);return rows.length===1&&rows[0].textContent.includes('workspace')?{row:rows[0].getAttribute('data-row-key'),label:rows[0].textContent.trim()}:undefined})()`));
 save('workspace-selected',{landedPath:landing.path,...workspaceRow,method:'existing directory picker UI'});
 }
 const sidebar=await until('real session Browser sidebar surface',()=>inspector.evaluate(dsh,`(()=>{const address=document.querySelector('input[aria-label="Enter an HTTP(S) address"],input[aria-label="输入 HTTP(S) 地址"]');if(address?.getClientRects().length)return 'page';const guide=[...document.querySelectorAll('button[data-sidebar-right-guide-entry="browser"]')].filter(e=>!e.disabled&&e.getClientRects().length);if(guide.length===1)return 'guide';const expand=document.querySelector('button[data-sidebar-right-expand]');if(expand&&!expand.disabled&&expand.getClientRects().length)expand.click();return false})()`));
 // Guide cards include a description. Only click the exact original Browser
 // card; an already restored Browser surface needs no new tab/action.
 if(sidebar==='guide')await until('Browser UI action',()=>inspector.evaluate(dsh,`(()=>{const rows=[...document.querySelectorAll('button[data-sidebar-right-guide-entry="browser"]')].filter(e=>!e.disabled&&e.getClientRects().length);if(rows.length!==1)return false;rows[0].click();return true})()`));
 await until('real address toolbar',()=>inspector.evaluate(dsh,`!!document.querySelector('input[aria-label="Enter an HTTP(S) address"],input[aria-label="输入 HTTP(S) 地址"]')`));
 const state=async()=>lastSlotStates=await inspector.evaluate(shell,`(async()=>{const frame=document.querySelector('iframe');const doc=decodeURIComponent(new URL(frame.src).hash.slice(1).match(/(?:^|&)__dsh_work_doc=([^&]+)/)?.[1]||'');const {Call}=await import('/wails/runtime.js');const rows=[];for(const slot of __e67Slots){try{rows.push({slot,state:await Call.ByID(${ids.state},doc,slot)})}catch(error){rows.push({slot,error:String(error)})}}return rows})()`);
 if(mode==='--dual-reopen'){
  const pending=await until('explicit NeedsReload after instance reopen',async()=>{const rows=await state();return rows.find(r=>r.state?.state==='NeedsReload'&&r.state.url===fixture&&!r.state.document&&!r.state.target)});
  const savedChrome=await until('visible safe descriptor and explicit restore action',()=>inspector.evaluate(dsh,`(()=>{const address=document.querySelector('input[aria-label="Enter an HTTP(S) address"],input[aria-label="输入 HTTP(S) 地址"]'),panel=document.querySelector('section[aria-label="Previously opened"],section[aria-label="上次打开"]');const tabs=[...document.querySelectorAll('[role="tab"],[data-dockkit-float-title]')].filter(e=>e.getClientRects().length).map(e=>e.textContent.trim());const action=panel&&[...panel.querySelectorAll('button')].find(e=>!e.disabled&&e.getClientRects().length&&/^(Restore page|恢复页面)$/.test(e.textContent.trim()));return address?.value===${JSON.stringify(fixture)}&&panel?.getClientRects().length&&panel.textContent.includes(${JSON.stringify(pending.state.title)})&&panel.textContent.includes(${JSON.stringify(fixture)})&&tabs.some(t=>t.includes(${JSON.stringify(pending.state.title)}))&&action?{address:address.value,tabs,restoreText:panel.textContent.trim(),action:action.textContent.trim()}:undefined})()`));
  save('needs-reload-chrome',savedChrome);
  save('needs-reload',pending);
  await until('explicit reload barrier',()=>existsSync(join(out,'reload.request')));
  await until('original explicit Restore page action',()=>click('^(Restore page|恢复页面)$'));
 }else{
 const requested=mode==='--dual-peer'?await inspector.evaluate(dsh,`document.querySelector('input[aria-label="Enter an HTTP(S) address"],input[aria-label="输入 HTTP(S) 地址"]')?.value`):undefined;
 if(requested!==fixture){
 await inspector.evaluate(dsh,`(()=>{const e=document.querySelector('input[aria-label="Enter an HTTP(S) address"],input[aria-label="输入 HTTP(S) 地址"]');Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(e,${JSON.stringify(fixture)});e.dispatchEvent(new Event('input',{bubbles:true}));e.dispatchEvent(new Event('change',{bubbles:true}));return true})()`);
 await inspector.evaluate(dsh,`(()=>{const e=document.querySelector('input[aria-label="Enter an HTTP(S) address"],input[aria-label="输入 HTTP(S) 地址"]');e.form.requestSubmit();return true})()`);
 }
 }
 const visible=await until('native slot ready',async()=>{const rows=await state();return rows.find(r=>r.state?.state==='ready'&&r.state.visible&&r.state.url===fixture)});
 if(mode?.startsWith('--dual-')){
  const chrome=await until('actual A toolbar title and URL synchronized',()=>inspector.evaluate(dsh,`(()=>{const address=document.querySelector('input[aria-label="Enter an HTTP(S) address"],input[aria-label="输入 HTTP(S) 地址"]'),tabs=[...document.querySelectorAll('[role="tab"],[data-dockkit-float-title]')].filter(e=>e.getClientRects().length).map(e=>e.textContent.trim());return address?.value===${JSON.stringify(fixture)}&&tabs.some(t=>t.includes(${JSON.stringify(visible.state.title)}))?{address:address.value,tabs}:undefined})()`));save('visible-chrome',chrome);
 }
 if(mode?.startsWith('--dual-')){const selected=await readSessionSelection();save('session-selected',selected??{});if(!selected||!visible.slot.startsWith(selected.key.slice(8)+'/'))throw Error('Native slot does not belong to actual selected A session')}
 save('visible',visible);
 save('window-admission',await inspector.evaluate(shell,`(async()=>{const {Call}=await import('/wails/runtime.js');return Call.ByID(${ids.admission})})()`));
 if(mode==='--dual-peer'||mode==='--dual-reopen'){
  await until('dual peer lifecycle barrier',()=>existsSync(join(out,'exit.request'))||existsSync(join(out,'finish.request')));
  if(existsSync(join(out,'exit.request'))){
   save('exit-requested',{method:'EmbeddedBrowserService.ExitUI'});
   await inspector.evaluate(shell,`(async()=>{const {Call}=await import('/wails/runtime.js');return Call.ByID(${ids.exit})})()`).catch(error=>save('exit-response',{error:String(error)}));
  }
 }else{

 await until('capture visible barrier',()=>existsSync(join(out,'hide.request')),30000);
 await until('collapse existing sidebar control',()=>click('^(Collapse right sidebar|收起右侧边栏)$'));
 const hidden=await until('native Hide ACK',async()=>{const rows=await state();return rows.find(r=>r.slot===visible.slot&&r.state&&!r.state.visible&&r.state.document===visible.state.document)});
 save('hidden',hidden);
 await until('capture hidden barrier',()=>existsSync(join(out,'resume.request')),30000);
 await until('expand retained sidebar',()=>inspector.evaluate(dsh,`(()=>{const rows=[...document.querySelectorAll('button[data-sidebar-right-expand]')].filter(e=>!e.disabled&&e.getClientRects().length);if(rows.length!==1)return false;rows[0].click();return true})()`));
 const restored=await until('same native slot visible again',async()=>{const rows=await state();return rows.find(r=>r.slot===visible.slot&&r.state?.state==='ready'&&r.state.visible&&r.state.document===visible.state.document&&r.state.lease===visible.state.lease&&r.state.target===visible.state.target)});
 const chrome=await until('restored toolbar URL and title synchronized',()=>inspector.evaluate(dsh,`(()=>{const e=document.querySelector('input[aria-label="Enter an HTTP(S) address"],input[aria-label="输入 HTTP(S) 地址"]');const title=${JSON.stringify(visible.state.title)};const tabs=[...document.querySelectorAll('[role="tab"],[data-dockkit-float-title]')].filter(e=>e.getClientRects().length).map(e=>e.textContent.trim());return e?.value===${JSON.stringify(fixture)}&&tabs.some(t=>t.includes(title))?{address:e.value,tabs}:undefined})()`));
 save('restored-chrome',chrome);
 save('restored',restored);
 if(process.env.DSH_WORK_LINUX_AGENT_TEST==='1'){
  const call=(name,...args)=>inspector.evaluate(shell,`(async()=>{const {Call}=await import('/wails/runtime.js');return Call.ByID(${id('automationservice',name)},...${JSON.stringify(args)})})()`);
  await runLinuxBrowserAgent({inspector,shell,dsh,visible:restored,fixture,out,save,call,click});
  const after=(await state()).find(r=>r.slot===restored.slot);
  save('agent-owned-after',after);
  if(after?.state?.state!=='ready'||after.state.document!==restored.state.document||after.state.target!==restored.state.target||after.state.lease!==restored.state.lease||after.state.brokerGeneration!==restored.state.brokerGeneration)throw Error('Read-only Agent or Stop replaced the original document');
 }
 if(process.env.DSH_WORK_LINUX_DUAL_TEST==='1'){
  await until('dual lifecycle completed',()=>existsSync(join(out,'dual-complete.request')),90000);
  const current=(await state()).find(r=>r.slot===restored.slot);
  save('dual-b-retained',current);
  if(current?.state?.state!=='ready'||current.state.document!==restored.state.document||current.state.target!==restored.state.target||current.state.lease!==restored.state.lease||current.state.brokerGeneration!==restored.state.brokerGeneration)throw Error('Dual instance lifetime changed B document');
 }
 await until('capture restored barrier',()=>existsSync(join(out,'quit.request')),30000);
 save('quit-requested',{method:'HostService.Quit'});
 await inspector.evaluate(shell,`(async()=>{const {Call}=await import('/wails/runtime.js');return await Call.ByID(${ids.quit})})()`).catch(e=>save('quit-response',{error:String(e)}));
 }
 }
} catch(error) {
 if(lastSlotStates)save('last-slot-states',lastSlotStates);
 if(inspector)save('inspector-protocol',{events:inspector.trace,targets:[...inspector.targets.values()],contextCount:inspector.contexts.size});
 if(inspector){const dsh=await inspector.find('dsh').catch(()=>{});if(dsh){save('failure-session-rows',await inspector.evaluate(dsh,`[...document.querySelectorAll('[role="treeitem"][data-row-key]')].filter(e=>e.getClientRects().length).map(e=>({key:e.getAttribute('data-row-key'),selected:e.getAttribute('aria-selected'),label:e.textContent.trim(),blankLabel:/^(New Session|新会话)$/.test(e.textContent.trim())}))`).catch(()=>[]));save('failure-controls',await inspector.evaluate(dsh,`[...document.querySelectorAll('button,[role="button"]')].filter(e=>e.getClientRects().length).slice(0,100).map(e=>({label:e.getAttribute('aria-label'),title:e.getAttribute('title'),text:e.textContent.trim().slice(0,80)}))`).catch(()=>[]));save('failure-browser-chrome',await inspector.evaluate(dsh,`({addresses:[...document.querySelectorAll('input[aria-label="Enter an HTTP(S) address"],input[aria-label="输入 HTTP(S) 地址"]')].map(e=>({value:e.value,visible:!!e.getClientRects().length})),tabs:[...document.querySelectorAll('[role="tab"],[data-dockkit-float-title]')].map(e=>({text:e.textContent.trim(),visible:!!e.getClientRects().length}))})`).catch(()=>({})));}}
 save(mode==='--boot-diagnostic'?'boot-inspector-unavailable':'failure',{error:String(error)});process.exitCode=1;
} finally {inspector?.close();}
