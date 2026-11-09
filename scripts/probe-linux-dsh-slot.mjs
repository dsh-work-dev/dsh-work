// Fixed, one-shot verification of the original Linux Wails/DSH UI over its
// standard WebKit inspector. No model/permission calls or product test endpoint.
import {readFileSync,writeFileSync,existsSync,mkdirSync} from 'node:fs';
import {resolve,join} from 'node:path';
import {fileURLToPath} from 'node:url';
const [base,fixture,directory]=process.argv.slice(2);
if(process.platform!=='linux'||!/^http:\/\/127\.0\.0\.1:\d+$/.test(base??'')||!/^http:\/\/127\.0\.0\.1:\d+\/$/.test(fixture??'')||!directory)throw Error('Expected isolated Linux inspector, local fixture and output directory');
const out=resolve(directory);mkdirSync(out,{recursive:true});
const repo=fileURLToPath(new URL('../',import.meta.url));
const id=(file,name)=>{const source=readFileSync(join(repo,'frontend/bindings/github.com/local/dsh-work/internal/desktopclient',file+'.ts'),'utf8');const match=source.match(new RegExp('export function '+name+'\\([^]*?\\$Call\\.ByID\\((\\d+)'));if(!match)throw Error('binding missing '+name);return Number(match[1]);};
const ids={cap:id('embeddedbrowserservice','Capability'),state:id('embeddedbrowserservice','State'),quit:id('hostservice','Quit')};
const delay=ms=>new Promise(r=>setTimeout(r,ms));
async function until(label,fn,ms=30000){const deadline=Date.now()+ms;let last;while(Date.now()<deadline){try{const v=await fn();if(v)return v}catch(e){last=e}await delay(100)}throw Error(label+': '+(last?.message??'bounded deadline'));}
function save(name,value){writeFileSync(join(out,name+'.json'),JSON.stringify(value,null,2).replace(/([?&](?:token|auth)=)[^&#"\s]+/gi,'$1[REDACTED]')+'\n',{mode:0o600});}
class Inspector {
 constructor(url){this.pending=new Map();this.targets=new Map();this.contexts=new Map();this.trace=[];this.seq=0;this.ws=new WebSocket(url);this.ws.onmessage=e=>this.receive(JSON.parse(e.data));this.ws.onclose=()=>{for(const p of this.pending.values()){clearTimeout(p.timer);p.reject(Error('WebKit inspector disconnected'))}this.pending.clear()};}
 receive(m,target=''){
  this.trace.push({method:m.method??'reply',target,error:m.error?.message});if(this.trace.length>150)this.trace.shift();
  if(m.method==='Target.dispatchMessageFromTarget'){this.receive(JSON.parse(m.params.message),m.params.targetId);return}
  if(m.method==='Target.targetCreated'){const t=m.params.targetInfo;if(!t.isProvisional&&['page','frame'].includes(t.type)){this.targets.set(t.targetId,t);void this.send('Runtime.enable',{},t.targetId).catch(()=>{});}return}
  if(m.method==='Target.targetDestroyed'){this.targets.delete(m.params.targetId);for(const [key,c] of this.contexts)if(c.target===m.params.targetId)this.contexts.delete(key);return}
  if(m.method==='Runtime.executionContextCreated'){const c=m.params.context;if(c.isPageContext!==false)this.contexts.set(target+':'+c.id,{target,contextId:c.id});}
  if(m.id){const p=this.pending.get(target+':'+m.id);if(p){this.pending.delete(target+':'+m.id);clearTimeout(p.timer);m.error?p.reject(Error(m.error.message)):p.resolve(m.result);}}
 }
 async open(){await new Promise((yes,no)=>{this.ws.onopen=yes;this.ws.onerror=no});await this.send('Target.setPauseOnStart',{pauseOnStart:false}).catch(()=>{});}
 send(method,params={},target=''){
  const id=++this.seq,key=target+':'+id;
  return new Promise((resolve,reject)=>{const timer=setTimeout(()=>{this.pending.delete(key);reject(Error('Inspector timeout '+method))},12000);this.pending.set(key,{resolve,reject,timer});const value={id,method,params};if(target){const outer=++this.seq;this.ws.send(JSON.stringify({id:outer,method:'Target.sendMessageToTarget',params:{targetId:target,message:JSON.stringify(value)}}))}else this.ws.send(JSON.stringify(value));});
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
let inspector;
try {
 const path=await until('Wails inspectable page',async()=>{const html=await(await fetch(base,{signal:AbortSignal.timeout(2000)})).text();return html.match(/\/socket\/\d+\/\d+\/WebPage/)?.[0]});
 inspector=new Inspector(base.replace('http:','ws:')+path);await inspector.open();
 const shell=await until('shell execution context',()=>inspector.find('shell'));
 await inspector.evaluate(shell,`(()=>{globalThis.__e67Slots=new Set();addEventListener('message',e=>{if(e.source!==document.querySelector('iframe')?.contentWindow)return;const m=e.data;if(m?.version===1&&m.type==='dsh-work/browser'&&typeof m.slot==='string')__e67Slots.add(m.slot)});return true})()`);
 const dsh=await until('real DSH provider',()=>inspector.find('dsh'),60000);
 const initial=await inspector.evaluate(shell,`(async()=>{const {Call}=await import('/wails/runtime.js');return{capability:await Call.ByID(${ids.cap})}})()`);
 if(!initial.capability)throw Error('formal embedded provider capability unavailable');
 save('provider-ready',initial);
 // Use existing UI controls, not a synthetic BrowserPage or session event.
 const click=pattern=>inspector.evaluate(dsh,`(()=>{const r=new RegExp(${JSON.stringify(pattern)},'i');const b=[...document.querySelectorAll('button,[role="button"]')].find(e=>e.getClientRects().length&&r.test(e.getAttribute('aria-label')||e.getAttribute('title')||e.textContent.trim()));if(b){b.click();return true}return false})()`);
 await click('^(New Session|New session|新会话|新建会话)$');
 await until('Browser UI action',()=>click('^(Browser|浏览器)$'));
 await until('real address toolbar',()=>inspector.evaluate(dsh,`!!document.querySelector('input[aria-label="Enter an HTTP(S) address"],input[aria-label="输入 HTTP(S) 地址"]')`));
 await inspector.evaluate(dsh,`(()=>{const e=document.querySelector('input[aria-label="Enter an HTTP(S) address"],input[aria-label="输入 HTTP(S) 地址"]');Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(e,${JSON.stringify(fixture)});e.dispatchEvent(new Event('input',{bubbles:true}));e.dispatchEvent(new Event('change',{bubbles:true}));return true})()`);
 await inspector.evaluate(dsh,`(()=>{const e=document.querySelector('input[aria-label="Enter an HTTP(S) address"],input[aria-label="输入 HTTP(S) 地址"]');e.form.requestSubmit();return true})()`);
 const state=()=>inspector.evaluate(shell,`(async()=>{const frame=document.querySelector('iframe');const doc=decodeURIComponent(new URL(frame.src).hash.slice(1).match(/(?:^|&)__dsh_work_doc=([^&]+)/)?.[1]||'');const {Call}=await import('/wails/runtime.js');const rows=[];for(const slot of __e67Slots)rows.push({slot,state:await Call.ByID(${ids.state},doc,slot)});return rows})()`);
 const visible=await until('native slot ready',async()=>{const rows=await state();return rows.find(r=>r.state.state==='ready'&&r.state.visible&&r.state.url===fixture)});
 save('visible',visible);
 await until('capture visible barrier',()=>existsSync(join(out,'hide.request')),30000);
 await until('collapse existing sidebar control',()=>click('^(Collapse right sidebar|收起右侧边栏)$'));
 const hidden=await until('native Hide ACK',async()=>{const rows=await state();return rows.find(r=>r.slot===visible.slot&&!r.state.visible&&r.state.document===visible.state.document)});
 save('hidden',hidden);
 await until('capture hidden barrier',()=>existsSync(join(out,'quit.request')),30000);
 save('quit-requested',{method:'HostService.Quit'});
 await inspector.evaluate(shell,`(async()=>{const {Call}=await import('/wails/runtime.js');return await Call.ByID(${ids.quit})})()`).catch(e=>save('quit-response',{error:String(e)}));
} catch(error) {
 if(inspector)save('inspector-protocol',{events:inspector.trace,targets:[...inspector.targets.values()],contextCount:inspector.contexts.size});
 if(inspector){const dsh=await inspector.find('dsh').catch(()=>{});if(dsh)save('failure-controls',await inspector.evaluate(dsh,`[...document.querySelectorAll('button,[role="button"]')].filter(e=>e.getClientRects().length).slice(0,100).map(e=>({label:e.getAttribute('aria-label'),title:e.getAttribute('title'),text:e.textContent.trim().slice(0,80)}))`).catch(()=>[]));}
 save('failure',{error:String(error)});process.exitCode=1;
} finally {inspector?.close();}
