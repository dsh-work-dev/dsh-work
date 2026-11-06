import {batchOperations} from './probes.mjs';
// Shared sole-owner CEF adapter. Policy/permits live in the real Go Service.
import http from 'node:http';
import {randomUUID,timingSafeEqual} from 'node:crypto';
import {readFileSync,writeFileSync} from 'node:fs';
import {fileURLToPath} from 'node:url';
import {attach} from '../../lib/attach.mjs';
const root=process.env.BROWSER_LAB_ROOT.replaceAll(String.fromCharCode(92),'/')+'/';
const session=JSON.parse(readFileSync(root+'session.json','utf8'));
const token=process.env.BROWSER_LAB_TOKEN;if(!token||token.length<24)throw Error('Missing lab token');
const headers={'X-Browser-Lab-Token':token};
let browser,connected=false,connectionEpoch=0,controlEpoch=0,controlReady=true,faultError=null,faults=Promise.resolve();
const tabs=new Map(),refs=new Map(),released=new Set(),pending=new Map();
const audit={dispatches:0,mutations:0,denied:0,releases:0,events:[]};
const record=(event,data={})=>{audit.events.push({time:Date.now(),event,...data});writeFileSync(root+'evidence/backend-audit.json',JSON.stringify(audit,null,2))};
async function native(action,args={}){const r=await fetch(session.control+'/command?'+new URLSearchParams({action,...args}),{method:'POST',headers,signal:AbortSignal.timeout(40000)});const text=await r.text();if(!r.ok)throw Error('Native '+action+': '+text);return text}
function invalidate(reason){
 controlReady=false;controlEpoch++;const ownEpoch=controlEpoch;record('fault-detected',{reason,controlEpoch});
 for(const controllers of pending.values())for(const c of controllers)c.abort();
 faults=faults.then(async()=>{
  const r=await fetch('http://127.0.0.1:19482/generation',{method:'POST',headers:{...headers,'Content-Type':'application/json'},body:JSON.stringify({generation:'browser-lab-fault-'+randomUUID()}),signal:AbortSignal.timeout(15000)});const text=await r.text();if(!r.ok)throw Error('Gateway invalidation failed: '+text);
  const state=JSON.parse(text);record('gateway-invalidated',{reason,generation:state.generation,epoch:state.browser.epoch});if(ownEpoch===controlEpoch)controlReady=true;
 }).catch(e=>{faultError=String(e.message||e);controlReady=false;record('fault-invalidation-failed',{error:faultError})});
 return faults;
}
async function connectTargets(){
 await waitUntil(async()=>{try{const rows=await(await fetch('http://127.0.0.1:19479/json/list')).json();return ['A','B'].every(name=>rows.some(p=>p.type==='page'&&new URL(p.url).searchParams.get('tab')===name))}catch{return false}},'named CEF targets');
 browser=await attach(19479);const own=browser;connectionEpoch++;connected=false;tabs.clear();
 browser.on('disconnected',()=>{if(browser!==own)return;connected=false;invalidate('broker-disconnected')});
 for(const page of browser.contexts().flatMap(c=>c.pages())){
  const name=new URL(page.url()).searchParams.get('tab');if(!['A','B'].includes(name))throw Error('Unknown initial CEF target');
  const cdp=await page.context().newCDPSession(page);await cdp.send('Page.enable');const info=(await cdp.send('Target.getTargetInfo')).targetInfo;
  const tab={name,page,cdp,targetId:info.targetId,navEpoch:0,faultEpoch:0,crashed:false};
  cdp.on('Page.frameNavigated',e=>{if(!e.frame.parentId){tab.navEpoch++;record('navigation',{tab:name,targetId:tab.targetId,navEpoch:tab.navEpoch,url:e.frame.url})}});
  cdp.on('Page.navigatedWithinDocument',()=>{tab.navEpoch++});
  page.on('crash',()=>{tab.crashed=true;tab.faultEpoch++;record('renderer-crashed',{tab:name,targetId:tab.targetId});native('renderer-fault',{tab:name}).catch(e=>record('native-fault-report-error',{error:e.message}));invalidate('renderer-crashed-'+name)});
  tabs.set(name,tab);
 }
 if(tabs.size!==2)throw Error('Expected two CEF tabs');connected=true;record('targets-attached',{connectionEpoch,targets:[...tabs.values()].map(t=>({name:t.name,targetId:t.targetId}))});
}
await connectTargets();
function requireControl(tab){if(faultError)throw Error('CEF_FAULT_INVALIDATION_FAILED');if(!connected)throw Error('CEF_BROKER_DISCONNECTED');if(!controlReady)throw Error('CEF_CONTROL_INVALIDATING');if(!tab||tab.crashed)throw Error('CEF_RENDERER_CRASHED')}
async function faultState(){return {connected,connectionEpoch,controlEpoch,controlReady,faultError,tabs:[...tabs.values()].map(t=>({name:t.name,targetId:t.targetId,faultEpoch:t.faultEpoch,crashed:t.crashed})),native:await hostState()}}
async function waitUntil(check,label,timeout=15000){const end=Date.now()+timeout;while(Date.now()<end){if(await check())return;await new Promise(r=>setTimeout(r,100))}throw Error('Timeout: '+label)}
// Chromium's browser-side trace metadata reports OS PIDs. A renderer's own
// getpid() may be namespace-local on Linux and must never select a kill target.
async function killOwnedLinuxRenderer(tab){
 const client=await browser.newBrowserCDPSession(),events=[];let complete=false,started=false;
 const collect=e=>events.push(...e.value);
 client.on('Tracing.dataCollected',collect);client.on('Tracing.tracingComplete',()=>{complete=true});
 try{
  await client.send('Tracing.start',{categories:'-*,disabled-by-default-devtools.timeline',transferMode:'ReportEvents'});started=true;
  await new Promise(r=>setTimeout(r,250));await client.send('Tracing.end');started=false;
  await waitUntil(()=>complete,'bounded renderer trace completed');
  const frames=events.filter(e=>e.name==='TracingStartedInBrowser').flatMap(e=>e.args?.data?.frames||[]);
  const normalize=id=>String(id).replaceAll('-','').toLowerCase();
  const identities=await Promise.all([...tabs.values()].map(async t=>({name:t.name,...await identity(t)})));
  const rows=identities.map(t=>({...t,pid:Number(frames.find(f=>normalize(f.frame)===normalize(t.frameId))?.processId)}));
  const selected=rows.find(t=>t.name===tab.name),peer=rows.find(t=>t.name!==tab.name);
  const inventory=(await client.send('SystemInfo.getProcessInfo')).processInfo;
  if(!Number.isSafeInteger(selected?.pid)||selected.pid<=1||selected.pid===peer?.pid||!inventory.some(p=>p.type==='renderer'&&Number(p.id)===selected.pid))throw Error('No unique owned renderer OS PID: '+JSON.stringify({rows,frames}));
  record('renderer-os-pid-binding',{rows,source:'browser-side TracingStartedInBrowser, cross-checked SystemInfo renderer inventory'});
  process.kill(selected.pid,'SIGKILL');
 }finally{if(started)await client.send('Tracing.end').catch(()=>{});await client.detach()}
}
async function crashRenderer(body){const tab=tabs.get(body.tab);requireControl(tab);record('inject-renderer-crash',{tab:body.tab,method:process.platform==='linux'?'SIGKILL browser-reported renderer OS PID':'Page.crash'});if(process.platform==='linux')await killOwnedLinuxRenderer(tab);else tab.cdp.send('Page.crash').catch(e=>record('crash-command-ended',{error:e.message}));await waitUntil(()=>tab.crashed,'renderer crash event');await faults;await waitUntil(async()=>(await hostState()).tabs.find(t=>t.name===body.tab)?.fault,'native renderer fault');if(faultError)throw Error(faultError);return faultState()}

async function recoverRenderer(body){const tab=tabs.get(body.tab);if(!connected||!tab?.crashed)throw Error('No crashed renderer to recover');await faults;if(faultError)throw Error(faultError);await tab.cdp.send('Page.reload');const previous=browser;browser=null;connected=false;await previous.close();await connectTargets();const repaired=tabs.get(body.tab);await repaired.page.waitForFunction(()=>!!window.lab?.instance,{},{timeout:10000});repaired.faultEpoch=tab.faultEpoch+1;await native('renderer-recovered',{tab:body.tab});record('renderer-recovered',{tab:body.tab,targetId:repaired.targetId,previousTargetId:tab.targetId,faultEpoch:repaired.faultEpoch});return faultState()}
async function crashBroker(){if(!connected)throw Error('Broker not connected');record('inject-broker-crash');await native('crash-broker');await waitUntil(()=>!connected,'broker disconnect');await faults;await waitUntil(async()=>!(await hostState()).guestRunning,'broker wait completed');if(faultError)throw Error(faultError);return faultState()}
async function recoverBroker(){if(connected)throw Error('Broker still connected');await faults;if(faultError)throw Error(faultError);await native('restart-broker');await waitUntil(async()=>(await hostState()).ready,'native broker restart');await connectTargets();record('broker-recovered',{connectionEpoch});return faultState()}
async function hostState(){const r=await fetch(session.control+'/state',{headers,signal:AbortSignal.timeout(5000)});if(!r.ok)throw Error('Host state unavailable');return r.json()}
async function identity(tab){const frame=(await tab.cdp.send('Page.getFrameTree')).frameTree.frame;return {targetId:tab.targetId,loaderId:frame.loaderId,frameId:frame.id,url:frame.url,navEpoch:tab.navEpoch}}
async function observe(selector){const h=await hostState(),tab=tabs.get(h.selected);if(!h.interactable||!h.tabs.find(t=>t.name===h.selected)?.visible)throw Error('CEF_VIEW_NOT_INTERACTABLE');requireControl(tab);const id=await identity(tab);const handle=await tab.page.locator(selector).elementHandle();if(!handle)throw Error('No observed element');const ref=randomUUID();const native=h.tabs.find(t=>t.name===h.selected);const binding={connectionEpoch,controlEpoch,faultEpoch:tab.faultEpoch,presentationEpoch:h.presentationEpoch,viewGeneration:h.viewGeneration,selectionEpoch:h.selectionEpoch,tab:h.selected,hwnd:native.hwnd,...id};refs.set(ref,{ref,binding,handle,tab});return {ref,binding}}
async function valid(ref){const v=refs.get(ref);if(!v)throw Error('CEF_TARGET_REF_UNKNOWN');requireControl(v.tab);if(v.binding.connectionEpoch!==connectionEpoch||v.binding.controlEpoch!==controlEpoch||v.binding.faultEpoch!==v.tab.faultEpoch)throw Error('CEF_FAULT_TARGET_EXPIRED');const h=await hostState();if(!h.interactable||!h.tabs.find(t=>t.name===h.selected)?.visible)throw Error('CEF_VIEW_NOT_INTERACTABLE');if(h.presentationEpoch!==v.binding.presentationEpoch)throw Error('CEF_PRESENTATION_CHANGED');if(h.viewGeneration!==v.binding.viewGeneration)throw Error('CEF_VIEW_GENERATION_CHANGED');if(h.selectionEpoch!==v.binding.selectionEpoch||h.selected!==v.binding.tab)throw Error('CEF_TAB_SELECTION_CHANGED');if(h.tabs.find(t=>t.name===h.selected)?.hwnd!==v.binding.hwnd)throw Error('CEF_NATIVE_VIEW_CHANGED');const id=await identity(v.tab);if(id.loaderId!==v.binding.loaderId||id.frameId!==v.binding.frameId||id.navEpoch!==v.binding.navEpoch||id.url!==v.binding.url)throw Error('CEF_DOCUMENT_CHANGED');if(!await v.handle.evaluate(el=>el.isConnected))throw Error('CEF_ELEMENT_DETACHED');return v}
async function elementMetadata(handle){return handle.evaluate(el=>({name:el.getAttribute('aria-label')||el.labels?.[0]?.textContent?.trim()||el.textContent?.trim()||el.id,tag:el.tagName.toLowerCase(),role:el.getAttribute('role')||'',type:el.getAttribute('type')||'',autocomplete:el.autocomplete||'',multiline:el.tagName==='TEXTAREA',inForm:!!el.closest('form'),submit:!!el.closest('form')&&el.tagName==='BUTTON'&&el.type==='submit',submitName:''}))}
async function inspect(args){const targets=args.targets||[];if(!targets.length){const h=await hostState();requireControl(tabs.get(h.selected));return {url:tabs.get(h.selected).page.url(),elements:[],tabs:[...tabs.values()].map((t,index)=>({index,url:t.page.url()}))}}
 const items=await Promise.all(targets.map(valid));return {url:items[0].tab.page.url(),elements:await Promise.all(items.map(v=>elementMetadata(v.handle))),tabs:[]}}
function pageResult(url,value){return {url,value,content:[{type:'text',text:'### Page\n- Page URL: '+url+'\n\n'+JSON.stringify(value)}]}}
async function execute(body,res){const {tool,arguments:args={},resourceId}=body;if(!resourceId)throw Error('Missing resource id');if(released.has(resourceId))throw Error('CEF_CONTROL_RESOURCE_RELEASED');
 const cancel=new AbortController();if(!pending.has(resourceId))pending.set(resourceId,new Set());pending.get(resourceId).add(cancel);res.on('close',()=>{if(!res.writableEnded)cancel.abort()});
 try{
  if(tool==='__dsh_inspect')return await inspect(args);
  if(args._labHoldMs){record('held-before-dispatch',{resourceId,tool});await new Promise((resolve,reject)=>{const timer=setTimeout(resolve,Math.min(Number(args._labHoldMs),10000));cancel.signal.addEventListener('abort',()=>{clearTimeout(timer);reject(Error('CEF_CONTROL_CANCELED'))},{once:true})})}
  if(cancel.signal.aborted||released.has(resourceId))throw Error('CEF_CONTROL_CANCELED');
  const v=await valid(args.target);if(cancel.signal.aborted||released.has(resourceId))throw Error('CEF_CONTROL_CANCELED');
  // Stable ElementHandle is bound to the observed document; do not re-resolve a
  // locator into a new document if navigation races this final dispatch.
  if(tool==='browser_snapshot'){const value=await v.tab.page.locator('body').innerText();return pageResult(v.tab.page.url(),{text:value,ref:v.ref,binding:v.binding})}
  if(!['browser_type','browser_click'].includes(tool))throw Error('Unsupported bounded lab tool');
  audit.dispatches++;record('dispatch',{resourceId,tool,ref:v.ref,binding:v.binding});
  if(tool==='browser_type')await v.handle.fill(String(args.text||''),{timeout:2500});else await v.handle.click({timeout:2500});
  audit.mutations++;record('mutated',{resourceId,tool});return pageResult(v.tab.page.url(),{ok:true,ref:v.ref})
 }finally{pending.get(resourceId)?.delete(cancel)}
}
async function cover(){const h=await hostState();if(h.covered)throw Error('ALREADY_COVERED');const tab=tabs.get(h.selected);await tab.page.screenshot({path:root+'evidence/placeholder.png'});const r=await fetch(session.control+'/command?action=cover',{method:'POST',headers});if(!r.ok)throw Error('Cover failed: '+await r.text());record('covered',{tab:h.selected});return hostState()}
async function display(body){if(!['A','B'].includes(body.tab))throw Error('Unknown display tab');const u=new URL(body.url);if(!['http:','https:'].includes(u.protocol)||u.username||u.password)throw Error('DISPLAY_URL_SCHEME_BLOCKED');const tab=tabs.get(body.tab);let security=null;const response=await tab.page.goto(u.href,{waitUntil:'domcontentloaded',timeout:30000});if(response)security=await response.securityDetails();const state=await identity(tab);record('display',{tab:body.tab,url:tab.page.url(),status:response?.status(),security});return {displayed:true,...state,status:response?.status(),security}}
async function refresh(){const old=browser;browser=null;connected=false;await old.close();await connectTargets()}
const batchProbe=batchOperations({getBrowser:()=>browser,tabs,hostState,root,session,refresh});
const server=http.createServer(async(req,res)=>{
 res.setHeader('Content-Type','application/json');const supplied=Buffer.from(req.headers['x-browser-lab-token']||'');const expected=Buffer.from(token);
 if(req.headers.origin||supplied.length!==expected.length||!timingSafeEqual(supplied,expected)){audit.denied++;record('unauthenticated-request',{path:req.url,originPresent:!!req.headers.origin});res.writeHead(403);res.end(JSON.stringify({error:'LAB_AUTH_REQUIRED'}));return}
 try{let body={};if(req.method==='POST'){let text='';for await(const part of req){text+=part;if(text.length>65536)throw Error('Body too large')};body=JSON.parse(text||'{}')}
 let value;
 if(['/batch-start','/batch-finish','/popup-clean','/download-mode','/permissions','/permission-result','/storage-get','/storage-set'].includes(req.url))value=await batchProbe(req.url,body);
 else if(req.url==='/health')value={ready:connected&&controlReady&&!faultError,targets:tabs.size};
 else if(req.url==='/profile-close'){for(const tab of [...tabs.values()])await tab.page.close();await waitUntil(()=>!connected,'profile graceful disconnect');await faults;await waitUntil(async()=>!(await hostState()).guestRunning,'profile broker exit');value=await faultState()}
 else if(req.url==='/page-text')value=await tabs.get('A').page.evaluate(()=>({text:document.body?.innerText,url:location.href,title:document.title}));
 else if(req.url==='/manual-reset'){for(const tab of tabs.values())await tab.page.evaluate(()=>{lab.events=[];if(lab.manualRecorderInstalled)return;lab.manualRecorderInstalled=true;for(const type of ['focusin','focusout','keydown','keyup','compositionstart','compositionupdate','compositionend','beforeinput','input'])document.addEventListener(type,e=>{if(e.target.id!=='entry')return;lab.events.push({time:Date.now(),type,target:e.target.id,data:e.data,trusted:e.isTrusted,value:e.target.value});if(lab.events.length>3000)lab.events.shift()},true)});value={recording:true}}
 else if(req.url==='/manual-events'){value={};for(const [name,tab]of tabs)value[name]=await tab.page.evaluate(()=>({value:document.querySelector('#entry').value,events:lab.events}))}
 else if(req.url==='/artifact-state')value=await tabs.get('A').page.evaluate(()=>({css:getComputedStyle(document.body).backgroundColor,js:document.querySelector('#relative-js')?.textContent,imageLoaded:document.querySelector('#asset-image')?.complete&&document.querySelector('#asset-image')?.naturalWidth>0}));
 else if(req.url==='/guest-isolation')value=await tabs.get('A').page.evaluate(()=>({parentSelf:parent===self,openerNull:opener===null,wails:typeof window._wails,webview:typeof window.chrome?.webview,hostObjects:typeof window.chrome?.webview?.hostObjects}));
 else if(req.url==='/version')value={browser:browser.version(),node:process.version};
 else if(req.url==='/processes'){const cdp=await browser.newBrowserCDPSession();try{value=await cdp.send('SystemInfo.getProcessInfo')}finally{await cdp.detach()}}
 else if(req.url==='/host-rebind'){await faults;faultError=null;await connectTargets();value=await faultState()}
 else if(req.url==='/fault-state')value=await faultState();
 else if(req.url==='/crash-renderer')value=await crashRenderer(body);
 else if(req.url==='/renderer')value=await recoverRenderer(body);
 else if(req.url==='/crash-broker')value=await crashBroker();
 else if(req.url==='/broker')value=await recoverBroker();
 else if(req.url==='/documents'){value={};for(const [name,tab]of tabs){try{value[name]=await tab.page.evaluate(()=>({instance:window.lab?.instance||window.artifactInstance,value:document.querySelector('#entry')?.value,result:document.querySelector('#result')?.textContent,clicks:window.businessClickCount,url:location.href,dpr:devicePixelRatio,viewport:{width:innerWidth,height:innerHeight},activeElement:document.activeElement?.id}))}catch(e){value[name]={unavailable:String(e.message)}}}}
 else if(req.url==='/stats')value=audit;
 else if(req.url==='/observe')value=await observe(body.selector||'#entry');
 else if(req.url==='/execute')value=await execute(body,res);
 else if(req.url==='/release'){released.add(body.resourceId);for(const c of pending.get(body.resourceId)||[])c.abort();audit.releases++;record('released',{resourceId:body.resourceId});value={released:true}}
 else if(req.url==='/cover')value=await cover();
 else if(req.url==='/display')value=await display(body);
 else if(req.url==='/shutdown'){res.end(JSON.stringify({shuttingDown:true}));server.close(()=>process.exit(0));server.closeIdleConnections();return}
 else if(req.url==='/identity'){value={};for(const [name,tab]of tabs)value[name]=await identity(tab)}
 else throw Error('Unknown lab endpoint');res.end(JSON.stringify(value));
 }catch(error){record('refused',{path:req.url,error:String(error.message||error)});if(!res.writableEnded){res.writeHead(409);res.end(JSON.stringify({error:String(error.message||error)}))}}
});server.listen(19483,'127.0.0.1',()=>record('backend-ready',{targets:2}));
