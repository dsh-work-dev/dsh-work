// Architectural research, separate from the 34-case product qualification.
// Only disposable lab processes and synthetic fixture contents are targeted.
import assert from 'node:assert/strict';
import {mkdtempSync,mkdirSync,writeFileSync,readFileSync,readlinkSync,readdirSync,cpSync,existsSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join,resolve} from 'node:path';
import {fileURLToPath} from 'node:url';
import {randomUUID} from 'node:crypto';
import {execFileSync,spawn} from 'node:child_process';
import {attach} from './lib/attach.mjs';
import {sleep,waitFor} from './lib/context.mjs';
const root=fileURLToPath(new URL('./',import.meta.url));
const runId='architecture-'+new Date().toISOString().replaceAll(/[:.]/g,'-');
const output=join(root,'results',runId);mkdirSync(output,{recursive:true});
const bundle=resolve(process.env.BROWSER_LAB_BUNDLE||join(root,process.platform==='win32'?'.work/bundle':'.work/posix/bundle'));
const report={schemaVersion:1,kind:'architecture-research',runId,platform:process.platform,arch:process.arch,
 sourceCommit:execFileSync('git',['rev-parse','HEAD'],{encoding:'utf8'}).trim(),results:[],
 scope:'Native lab engine/presenter evidence, not daemon multi-workbench admission or production provider qualification'};
function save(){writeFileSync(join(output,'report.json'),JSON.stringify(report,null,2))}
async function run(name,mode,body){
 console.log('Architecture:',name);const runRoot=mkdtempSync(join(tmpdir(),'browser-lab-arch-')),evidenceRoot=join(runRoot,'evidence');mkdirSync(evidenceRoot);
 const options={runId:runId+'-'+name,runRoot,evidenceRoot,bundle,token:randomUUID()+randomUUID(),fullNative:true};
 process.env.BROWSER_LAB_ARCH_PROBE='1';process.env.BROWSER_LAB_ARCH_NONCE=randomUUID().replaceAll('-','');
 if(mode==='artifact')process.env.BROWSER_LAB_ARCH_ARTIFACT='1';else delete process.env.BROWSER_LAB_ARCH_ARTIFACT;
 if(mode==='shared')process.env.BROWSER_LAB_ARCH_SHARED='1';else delete process.env.BROWSER_LAB_ARCH_SHARED;
 if(name==='renderer-oopif')process.env.BROWSER_LAB_ARCH_OOPIF='1';else delete process.env.BROWSER_LAB_ARCH_OOPIF;
 let manager,browser,result={id:name,status:'fail'},faultInjected=false;
 try{
  manager=process.platform==='win32'?await(await import('./platforms/windows.mjs')).startWindows(options):await(await import('./platforms/posix-smoke.mjs')).startPosixSmoke(options);
  browser=await attach(19479);
  const created=await waitFor(()=>{const all=browser.contexts().flatMap(c=>c.pages());return all.length===2?all:false},'two existing CEF pages');
  const setup={initialUrls:created.map(p=>p.url()),rolesAssignedByProbe:mode==='shared'};
  // A shared-process fault fixture defines its own two test documents. Load
  // them serially in the existing targets, rather than assuming simultaneous
  // initial CreateBrowser navigations have completed. This is setup, before
  // identity/state observations, not a replacement page or crash recovery.
  if(mode==='shared')for(let i=0;i<created.length;i++)await created[i].goto('http://127.0.0.1:19478/interactions?tab='+['A','B'][i],{waitUntil:'domcontentloaded'});
  writeFileSync(join(evidenceRoot,'setup-targets.json'),JSON.stringify(setup,null,2));
  const pages=await waitFor(()=>{
   const all=browser.contexts().flatMap(c=>c.pages());
   if(all.length!==2)return false;
   const names=all.map(p=>{try{return new URL(p.url()).searchParams.get('tab')}catch{return null}});
   return names.includes('A')&&names.includes('B')?all:false;
  },'both named documents navigated');
  const A=pages.find(p=>new URL(p.url()).searchParams.get('tab')==='A'),B=pages.find(p=>new URL(p.url()).searchParams.get('tab')==='B');assert(A&&B);
  await Promise.all([A,B].map(p=>p.waitForFunction(()=>!!window.lab?.instance)));
  const control=async(action,args={})=>process.platform==='win32'?manager.command(action,args):manager.call('/command?'+new URLSearchParams({action,...args}),{});
  const state=async()=>process.platform==='win32'?manager.call('control','/state'):manager.call('/state');
  const evidence=await body({A,B,browser,options,manager,state,control,markFault(){faultInjected=true;options.architectureFault=true;options.architectureFaultPid=manager.child?.pid}});
  result={id:name,status:'pass',setup,evidence};
 }catch(e){result.error=e.stack;console.error(name,e.message)}
 finally{
  // Close known CEF targets while still connected, including an orphaned broker
  // after injected UI death. The normal app close is tested separately.
  if(browser)try{
   if(faultInjected)for(const p of browser.contexts().flatMap(c=>c.pages())){
    try{const s=await p.context().newCDPSession(p);const {targetInfo}=await s.send('Target.getTargetInfo');await fetch('http://127.0.0.1:19479/json/close/'+targetInfo.targetId)}catch{}
   }
   await browser.close();
  }catch{}
  if(manager)try{await manager.close()}catch(e){
   // A killed UI cannot produce its graceful-close marker. Keep this distinct
   // from a normal cleanup pass; require process/listener inventory below.
   result.status='fail';result.cleanupError=e.message;
  }
  if(process.platform==='win32'&&existsSync(join(evidenceRoot,'cleanup.json'))){
   const cleanup=JSON.parse(readFileSync(join(evidenceRoot,'cleanup.json'),'utf8').replace(/^\uFEFF/,''));
   if(cleanup.remainingProcesses.length||cleanup.remainingListeners.length){result.status='fail';result.cleanupError='Remaining scoped resources'}
  }
  cpSync(evidenceRoot,join(output,name),{recursive:true});report.results.push(result);save();
 }
}
async function storage(page,action,value){return page.evaluate(async({action,value})=>{
 const db=await new Promise((ok,no)=>{const r=indexedDB.open('same-name',1);r.onupgradeneeded=()=>r.result.createObjectStore('v');r.onsuccess=()=>ok(r.result);r.onerror=()=>no(r.error)});
 const idb=async(write)=>new Promise((ok,no)=>{const t=db.transaction('v',write?'readwrite':'readonly'),s=t.objectStore('v'),r=write?s.put(value,'key'):s.get('key');t.oncomplete=()=>ok(r.result??null);t.onerror=()=>no(t.error)});
 const c=await caches.open('same-name');
 if(action==='write'){document.cookie='same='+value+';path=/';localStorage.setItem('same',value);sessionStorage.setItem('same',value);await idb(true);await c.put('/cache-key',new Response(value))}
 const response=await c.match('/cache-key'),data={cookie:document.cookie,local:localStorage.getItem('same'),session:sessionStorage.getItem('same'),idb:await idb(false),cache:response?await response.text():null,controller:!!navigator.serviceWorker?.controller,secure:isSecureContext,relative:!!window.relativeAsset};db.close();return data;
 },{action,value})}
await run('artifact-partitions','artifact',async({A,B,browser,options,manager,control,state})=>{
 const origins=[new URL(A.url()).origin,new URL(B.url()).origin];assert.notEqual(...origins);
 const empty=await storage(B,'read');assert.equal(empty.local,null);assert.equal(empty.idb,null);assert.equal(empty.cache,null);assert(empty.secure&&empty.relative);
 await storage(A,'write','A_ONLY');const bBefore=await storage(B,'read');assert.equal(bBefore.local,null);assert.equal(bBefore.idb,null);assert.equal(bBefore.cache,null);assert.equal(bBefore.cookie,'');
 const a=await storage(A,'read');assert.equal(a.local,'A_ONLY');assert.equal(a.idb,'A_ONLY');assert.equal(a.cache,'A_ONLY');assert.equal(a.session,'A_ONLY');assert(a.cookie.includes('A_ONLY'));
 await storage(B,'write','B_ONLY');const b=await storage(B,'read');assert.equal(b.local,'B_ONLY');assert.equal((await storage(A,'read')).local,'A_ONLY');
 await A.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));await sleep(200);
 await manager.capture('artifact-partitions');
 const cross=await A.evaluate(async url=>{try{return {body:await(await fetch(url+'/secret')).text()}}catch(e){return {denied:e.name}}},origins[1]);assert(!cross.body?.includes('ROOT_B_ONLY'));
 const worker=await A.evaluate(async()=>{
  const violations=[];const onViolation=e=>violations.push({directive:e.effectiveDirective,blocked:e.blockedURI});document.addEventListener('securitypolicyviolation',onViolation);
  let sw;try{await navigator.serviceWorker.register('/sw.js');sw='allowed'}catch(e){sw=e.name}
  const worker=await new Promise(resolve=>{
   let w;const finish=value=>{clearTimeout(timer);w?.terminate();resolve(value)},timer=setTimeout(()=>finish('timeout'),5000);
   try{w=new Worker('/worker.js');w.onmessage=()=>finish('executed');w.onerror=e=>{e.preventDefault();finish('blocked-error')};w.postMessage('probe')}catch(e){finish(e.name)}
  });
  await new Promise(r=>setTimeout(r,50));document.removeEventListener('securitypolicyviolation',onViolation);
  return {sw,worker,violations,serviceWorkerAPI:typeof navigator.serviceWorker?.register==='function'};
 });
 assert(worker.serviceWorkerAPI);assert.notEqual(worker.sw,'allowed');assert.notEqual(worker.worker,'executed');assert.notEqual(worker.worker,'timeout');
 assert(worker.violations.some(v=>v.directive==='worker-src'),'Actual worker CSP violation required, not a generic network error');
 const cdp=await A.context().newCDPSession(A);
 await cdp.send('Page.navigate',{url:origins[1]+'/secret'});await sleep(350);
 const direct=await A.locator('body').innerText();assert.equal(direct,'LEASE_DENIED');
 const foreignPartition=await storage(A,'read');assert.equal(foreignPartition.local,null);assert.equal(foreignPartition.cookie,'');assert.equal(foreignPartition.idb,null);assert.equal(foreignPartition.cache,null);
 await B.evaluate(()=>{window.bcMessages=[];window.bcReader=new BroadcastChannel('lease-probe');window.bcReader.onmessage=e=>bcMessages.push(e.data);window.bcWriter=new BroadcastChannel('lease-probe');bcWriter.postMessage('B_POSITIVE')});
 await B.waitForFunction(()=>bcMessages.includes('B_POSITIVE'));
 await A.evaluate(()=>{window.bcWriter=new BroadcastChannel('lease-probe');bcWriter.postMessage('A_CROSS')});await sleep(300);
 const broadcast=await B.evaluate(()=>bcMessages);assert(!broadcast.includes('A_CROSS'));
 await A.goto(origins[0]+'/?tab=A');assert.equal((await storage(A,'read')).local,'A_ONLY');
 const dataRoot=process.platform==='win32'?manager.session.dataRoot:join(options.runRoot,'data');
 // A throttled real CEF resource read must stop after revocation. A completed
 // tiny response would not exercise cancellation of an in-flight resource.
 await A.evaluate(()=>{
  window.streamReceived=0;window.streamResult=(async()=>{
   try{const reader=(await fetch('/slow')).body.getReader();for(;;){const {value,done}=await reader.read();if(done)break;window.streamReceived+=value.byteLength}return {received:window.streamReceived,completed:true}}
   catch(e){return {received:window.streamReceived,error:e.name}}
  })();
 });
 await A.waitForFunction(()=>window.streamReceived>0);const receivedBeforeRevoke=await A.evaluate(()=>window.streamReceived);
 writeFileSync(join(dataRoot,'arch-revoke-A-'+process.env.BROWSER_LAB_ARCH_NONCE),'revoked');
 const stream=await A.evaluate(()=>window.streamResult);assert(stream.error&&stream.received>=receivedBeforeRevoke&&stream.received<262144);
 const revoked=await A.evaluate(async()=>{const r=await fetch('/secret');return {status:r.status,body:await r.text()}});assert.equal(revoked.status,403);
 await A.close();assert.equal((await storage(B,'read')).local,'B_ONLY');
 const session=await B.context().newCDPSession(B),{targetInfo}=await session.send('Target.getTargetInfo');
 await fetch('http://127.0.0.1:19479/json/close/'+targetInfo.targetId);
 await waitFor(async()=>{try{await fetch('http://127.0.0.1:19479/json/list',{signal:AbortSignal.timeout(500)});return false}catch{return true}},'old artifact broker exit');
 await waitFor(async()=>!(await state()).guestRunning,'old artifact broker reaped');
 // Same host/profile/cache directory, fresh lease and memory contexts.
 const nextNonce=randomUUID().replaceAll('-','');writeFileSync(join(dataRoot,'arch-current-lease'),nextNonce);
 await control('restart-broker');await waitFor(async()=>{try{return (await state()).ready}catch{return false}},'new artifact contexts');
 const reopened=await attach(19479),reopenedPages=reopened.contexts().flatMap(c=>c.pages());assert.equal(reopenedPages.length,2);
 const fresh=[];for(const page of reopenedPages){await page.waitForFunction(()=>!!window.lab?.instance);const v=await storage(page,'read');assert.equal(v.local,null);assert.equal(v.cookie,'');assert.equal(v.idb,null);assert.equal(v.cache,null);assert(!v.controller);fresh.push({origin:new URL(page.url()).origin,...v})}
 const page=reopenedPages.find(p=>new URL(p.url()).searchParams.get('tab')==='A');
 await page.goto(origins[0]+'/secret');const oldLease=await page.locator('body').innerText();assert.equal(oldLease,'LEASE_DENIED');
 await page.goto('http://a.'+nextNonce+'.localhost/?tab=A');await reopened.close();
 return {origins,positiveControls:{A:a,B:b},BafterAseed:bBefore,cross,worker,direct,foreignPartition,broadcast,receivedBeforeRevoke,stream,revoked,fresh,oldLease,
  limits:['Memory fixture, not integrated os.Root service or authenticated Go lease IPC','Resource read cancellation and A close are observed; production cross-process revoke acknowledgement remains to implement','Custom dsh-artifact scheme is not tested; this candidate uses standard HTTP localhost origins']};
});
const sameFrame=(a,b)=>String(a).replaceAll('-','').toLowerCase()===String(b).replaceAll('-','').toLowerCase();
async function mapping(browser,pages,evidenceRoot){
 const events=[];
 // CEF's page-target handler may scope initial metadata to its WebContents.
 // Collect each page's real frame tree instead of assuming one trace lists B.
 for(const page of pages){
  const trace=await page.context().newCDPSession(page);let complete=false,started=false;
  trace.on('Tracing.dataCollected',e=>events.push(...e.value));trace.on('Tracing.tracingComplete',()=>complete=true);
  try{
   await trace.send('Tracing.start',{categories:'-*,disabled-by-default-devtools.timeline',transferMode:'ReportEvents'});started=true;
   await sleep(300);await trace.send('Tracing.end');started=false;await waitFor(()=>complete,'browser trace');
  }finally{if(started)await trace.send('Tracing.end').catch(()=>{});await trace.detach()}
 }
 const frames=events.filter(e=>e.name==='TracingStartedInBrowser').flatMap(e=>e.args?.data?.frames||[]);
 const s=await browser.newBrowserCDPSession(),inventory=(await s.send('SystemInfo.getProcessInfo')).processInfo;await s.detach();
 const result={frames,inventory};writeFileSync(join(evidenceRoot,'renderer-mapping.json'),JSON.stringify(result,null,2));return result;
}
async function killRenderer(browser,pages,pid){
 const s=await browser.newBrowserCDPSession(),info=(await s.send('SystemInfo.getProcessInfo')).processInfo;await s.detach();
 assert(Number.isSafeInteger(pid)&&pid>1&&info.some(p=>p.type==='renderer'&&Number(p.id)===pid),'PID must be a current renderer of this private CEF instance');
 process.kill(pid,'SIGKILL');await sleep(1000);
 return Promise.all(pages.map(async(p)=>{try{return {url:p.url(),state:await p.evaluate(()=>({instance:window.lab?.instance,memory:window.memoryOnly,input:document.querySelector('#entry')?.value}))}}catch(e){return {url:p.url(),unavailable:e.message.split('\n')[0]}}}));
}
function linuxPipeOwners(host,broker){
 const rows=[];let input;try{input=readlinkSync('/proc/'+broker+'/fd/0')}catch(e){return {error:e.code}}
 const processes=[];for(const name of readdirSync('/proc').filter(n=>/^\d+$/.test(n)))try{
  const stat=readFileSync('/proc/'+name+'/stat','utf8').split(') ')[1].split(' ');processes.push({pid:Number(name),ppid:Number(stat[1]),group:Number(stat[2])});
 }catch{}
 const owned=new Set([host,broker]);let changed=true;while(changed){changed=false;for(const p of processes)if(owned.has(p.ppid)&&!owned.has(p.pid)){owned.add(p.pid);changed=true}}
 for(const p of processes.filter(p=>owned.has(p.pid)||p.group===host))try{for(const fd of readdirSync('/proc/'+p.pid+'/fd'))try{
  if(readlinkSync('/proc/'+p.pid+'/fd/'+fd)!==input)continue;
  const flags=readFileSync('/proc/'+p.pid+'/fdinfo/'+fd,'utf8').match(/^flags:\s+(\d+)/m)?.[1];
  rows.push({...p,comm:readFileSync('/proc/'+p.pid+'/comm','utf8').trim(),fd,flags});
 }catch{}}catch{}
 return {input,rows};
}
await run('renderer-shared','shared',async({A,B,browser,options})=>{
 await B.goto('http://127.0.0.1:19478/interactions?tab=B');
 const sA=await A.context().newCDPSession(A),sB=await B.context().newCDPSession(B);
 const roots=[(await sA.send('Page.getFrameTree')).frameTree.frame.id,(await sB.send('Page.getFrameTree')).frameTree.frame.id];
 const map=await mapping(browser,[A,B],options.evidenceRoot),pids=roots.map(id=>Number(map.frames.find(f=>sameFrame(f.frame,id))?.processId));
 assert(pids.every(Number.isSafeInteger),'Actual main-frame PID mapping required');assert.equal(pids[0],pids[1],'Shared renderer must really be constructed');
 const events=[];A.on('crash',()=>events.push('A'));B.on('crash',()=>events.push('B'));
 const after=await killRenderer(browser,[A,B],pids[0]);assert(after.every(p=>p.unavailable));
 return {configuration:'process-per-site for this fault fixture; SiteIsolation and sandbox remain enabled',roots,pids,mapping:map,after,crashEvents:events,limits:['Engine fault-set observation; no claim of automatic production control revocation']};
});
await run('renderer-oopif','default',async({A,B,browser,options})=>{
 await A.locator('#entry').fill('UNSUBMITTED_A');await B.locator('#entry').fill('UNSUBMITTED_B');
 const before=await Promise.all([A,B].map(p=>p.evaluate(()=>({instance:lab.instance,input:document.querySelector('#entry').value}))));
 await A.evaluate(()=>{const f=document.createElement('iframe');f.src='http://localhost:19478/interactions?tab=child';document.body.append(f)});
 await waitFor(()=>A.frames().length===2,'cross-site frame');await A.frames()[1].waitForFunction(()=>!!window.lab?.instance);
 const map=await mapping(browser,[A,B],options.evidenceRoot),session=await A.context().newCDPSession(A),tree=(await session.send('Page.getFrameTree')).frameTree;
 // Page.getFrameTree on the root target can omit remote OOPIF children. Use
 // the browser-side trace parent relation, cross-checked against the live
 // Playwright frame URL and the SystemInfo renderer inventory.
 const child=map.frames.find(f=>sameFrame(f.parent,tree.frame.id)&&f.url==='http://localhost:19478/interactions?tab=child');
 const rootPid=Number(map.frames.find(f=>sameFrame(f.frame,tree.frame.id))?.processId),childId=child?.frame,childPid=Number(child?.processId);
 assert(Number.isSafeInteger(childPid)&&childPid!==rootPid,'Actual out-of-process iframe required');
 const mainCrashEvents=[];A.on('crash',()=>mainCrashEvents.push('A'));B.on('crash',()=>mainCrashEvents.push('B'));
 const after=await killRenderer(browser,[A,B],childPid);assert(after.every(p=>!p.unavailable));
 for(let i=0;i<2;i++){assert.equal(after[i].state.instance,before[i].instance);assert.equal(after[i].state.input,before[i].input)}
 const childProbe=await Promise.race([
  Promise.resolve().then(()=>A.frames().find(f=>f.url()==='http://localhost:19478/interactions?tab=child').evaluate(()=>lab.instance)).then(()=>({status:'responded'}),e=>({status:'unavailable',error:e.message.split('\n')[0]})),
  sleep(3000).then(()=>({status:'no-execution-context-response'}))]);assert.notEqual(childProbe.status,'responded');
 return {configuration:'site-per-process for the OOPIF fixture',mapping:map,rootPid,childPid,childId,before,after,childProbe,mainCrashEvents,limits:['Main page crash event alone cannot establish iframe health','Actual production frame-fault observer and authorization barrier remain to be validated']};
});
for(const kind of ['normal','kill',...(process.platform==='win32'?['destroy-parent']:[])]){
 await run('ui-'+kind,'default',async({A,B,browser,manager,state,control,markFault,options})=>{
  await A.locator('#entry').fill('UNSUBMITTED_A');await B.locator('#entry').fill('UNSUBMITTED_B');
  const before=await Promise.all([A,B].map(p=>p.evaluate(()=>({instance:lab.instance,input:document.querySelector('#entry').value,memory:window.memoryOnly=crypto.randomUUID()}))));
  const nativeBefore=await state();
  if(process.platform==='linux'&&kind==='kill')writeFileSync(join(options.evidenceRoot,'ui-pipe-owners.json'),JSON.stringify(linuxPipeOwners(manager.child.pid,Number(nativeBefore.guestPID)),null,2));
  if(kind==='normal'){
   if(process.platform==='win32')await control('quit');else await manager.call('/close',{});
  }else{
   markFault();
   if(kind==='destroy-parent')await control('architecture-destroy-parent').catch(()=>{});
   else{
    const pid=process.platform==='win32'?manager.session.processes.host.pid:manager.child.pid;
    process.kill(pid,'SIGKILL');
   }
  }
  await sleep(1500);const survival=[];
  for(const page of [A,B])try{survival.push(await page.evaluate(()=>({instance:lab.instance,input:document.querySelector('#entry').value,memory:window.memoryOnly})))}catch(e){survival.push({unavailable:e.message.split('\n')[0]})}
  // Retain the pre-reopen observation even if shutdown/reopen itself fails.
  writeFileSync(join(options.evidenceRoot,'ui-survival.json'),JSON.stringify({kind,nativeBefore,before,survival},null,2));
  // Stop any remaining owned broker via its actual target identities, not by
  // guessing that parent destruction also closed the browser process.
  for(const p of browser.contexts().flatMap(c=>c.pages()))try{
   const s=await p.context().newCDPSession(p),{targetInfo}=await s.send('Target.getTargetInfo');
   await fetch('http://127.0.0.1:19479/json/close/'+targetInfo.targetId);
  }catch{}
  await browser.close();
  await waitFor(async()=>{try{await fetch('http://127.0.0.1:19479/json/list',{signal:AbortSignal.timeout(500)});return false}catch{return true}},'old UI broker endpoint gone',20000);
  // The debug listener can close before CEF releases its singleton/profile
  // lock. Wait for the actual original broker, not just for its TCP port.
  const oldBroker=Number(nativeBefore.guestPID);
  if(Number.isSafeInteger(oldBroker)&&oldBroker>1)await waitFor(()=>{
   try{process.kill(oldBroker,0);if(process.platform==='linux'&&readFileSync('/proc/'+oldBroker+'/stat','utf8').split(') ')[1]?.startsWith('Z'))return true;return false}catch(e){return e.code==='ESRCH'||e.code==='ENOENT'}
  },'old UI broker process exited',20000);
  if(process.platform==='win32'){
   // Close a surviving host after raw parent destruction; this is cleanup,
   // after document survival was measured, not a graceful-detach substitute.
   try{await control('quit')}catch{}
   await waitFor(async()=>{try{await fetch('http://127.0.0.1:19481/state',{signal:AbortSignal.timeout(500)});return false}catch{return true}},'old UI endpoint closed',15000);
   const session=manager.session,data=session.dataRoot;
   const host=spawn(join(data,'host.exe'),[],{cwd:data,windowsHide:true,stdio:'ignore',env:{...process.env,
    BROWSER_LAB_ROOT:options.runRoot,BROWSER_LAB_DATA_ROOT:data,BROWSER_LAB_TOKEN:session.token,
    BROWSER_LAB_CEF_EXE:join(data,'cef/cefsimple.exe'),BROWSER_LAB_CAPTURE_EXE:join(data,'capture.exe')}});
   let error;host.on('error',e=>error=e);
   await waitFor(async()=>{if(error)throw error;try{return (await state()).ready}catch{return false}},'reopened Windows UI',30000);
  }else await manager.restartHost();
  const reopened=await attach(19479);
  const afterReopen=await Promise.all(reopened.contexts().flatMap(c=>c.pages()).map(async p=>{
   await p.waitForFunction(()=>!!window.lab?.instance);
   return p.evaluate(()=>({instance:lab.instance,input:document.querySelector('#entry').value,memory:window.memoryOnly??null}));
  }));
  assert.equal(afterReopen.length,2);assert(afterReopen.every(s=>!before.some(old=>old.instance===s.instance)));
  assert(afterReopen.every(s=>s.input===''&&s.memory===null));
  await reopened.close();
  return {kind,nativeBefore,before,survival,afterReopen,liveRetained:survival.every((s,i)=>s.instance===before[i].instance&&s.memory===before[i].memory&&s.input===before[i].input),
   limits:['Current lab broker is UI-owned, unlike proposed daemon ownership','Reopen creates new documents and loses input; descriptor-only product UI and two independent UI processes sharing one broker are not yet implemented']};
 });
}
save();console.log('Architecture evidence:',output);
process.exitCode=report.results.some(r=>r.status!=='pass')?1:0;
