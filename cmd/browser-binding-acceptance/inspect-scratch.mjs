// Acceptance only. Reads the selected scratch extension's recorded tab IDs.
// Each invocation disconnects DevTools before returning; never run during idle.
import fs from 'node:fs';import path from 'node:path';import assert from 'node:assert/strict';
const [root,instance,output,expectedFile,mode]=process.argv.slice(2);
assert(path.isAbsolute(root)&&/^dsh-binding-gate-[a-f0-9]{32}$/.test(path.basename(root)));
assert.equal(fs.readFileSync(path.join(root,'binding-gate-owner.txt'),'utf8').trim(),'dsh-work-binding-acceptance-v1');
const data=path.join(root,'browser-B');assert(!fs.lstatSync(data).isSymbolicLink());
const [port,endpoint]=fs.readFileSync(path.join(data,'DevToolsActivePort'),'utf8').trim().split(/\r?\n/);
assert(/^\d+$/.test(port)&&Number(port)>0&&Number(port)<=65535&&/^\/devtools\/browser\/[a-f0-9-]+$/i.test(endpoint));
const ws=new WebSocket(`ws://127.0.0.1:${port}${endpoint}`),pending=new Map();let id=0,session;
const timer=setTimeout(()=>{ws.close();process.exitCode=1;for(const p of pending.values())p.reject(Error('scratch inspection timeout'))},12000);
ws.onmessage=e=>{const m=JSON.parse(e.data);if(m.id){const p=pending.get(m.id);pending.delete(m.id);if(p)m.error?p.reject(Error(JSON.stringify(m.error))):p.resolve(m.result)}};
const send=(method,params={},sessionId)=>new Promise((resolve,reject)=>{const n=++id;pending.set(n,{resolve,reject});ws.send(JSON.stringify({id:n,method,params,...(sessionId?{sessionId}:{})}))});
await new Promise((resolve,reject)=>{ws.onopen=resolve;ws.onerror=reject});
try{
 const targets=await send('Target.getTargets');
 const workers=targets.targetInfos.filter(t=>t.type==='service_worker'&&t.url==='chrome-extension://fpemabjfldbpdmjfdofdnfdaccafobod/acceptance-worker.js');
 assert.equal(workers.length,1,'expected exactly one extension worker in selected scratch browser');
 session=(await send('Target.attachToTarget',{targetId:workers[0].targetId,flatten:true})).sessionId;
 const expected=expectedFile?JSON.parse(fs.readFileSync(expectedFile,'utf8')):undefined;
 const expectedIds=expected?.tabs.map(t=>t.id)??[];
 const expression=`(async()=>{
  const local=await chrome.storage.local.get('instance'),saved=await chrome.storage.session.get('ownedGroups');
  const rows=saved.ownedGroups??[], ids=[...new Set([...rows.flatMap(r=>r.tabs??[]),...${JSON.stringify(expectedIds)}])];
  const targets=await chrome.debugger.getTargets(),tabs=[];for(const id of ids){try{const t=await chrome.tabs.get(id);tabs.push({id:t.id,groupId:t.groupId,url:t.url,attached:targets.some(v=>v.tabId===id&&v.attached)})}catch{tabs.push({id,missing:true})}}
  const groups=[];for(const id of [...new Set(rows.map(r=>r.group).filter(id=>id>=0))])try{const g=await chrome.tabGroups.get(id);groups.push({id:g.id,title:g.title,color:g.color})}catch{}
  return {instance:local.instance,groups,tabs};
 })()`;
 let observed;
 for(let attempt=0;attempt<40;attempt++){
  const r=await send('Runtime.evaluate',{expression,awaitPromise:true,returnByValue:true},session);assert(!r.exceptionDetails,JSON.stringify(r.exceptionDetails));observed=r.result.value;assert.equal(observed.instance,instance,'inspected a different profile instance');
  if(mode==='stopped-a'){
   const aGroup=expected.groups.find(g=>g.title.includes('rel-A-'))?.id;
   if(expected.tabs.every(old=>{const t=observed.tabs.find(t=>t.id===old.id);return t&&!t.missing&&(old.groupId===aGroup?t.groupId===-1&&!t.attached:t.groupId===old.groupId&&t.attached)}))break;
  }else if(mode==='capture'){
   if(observed.groups.length===2&&observed.tabs.length===2&&observed.tabs.every(t=>!t.missing&&t.groupId>=0&&t.attached))break;
  }else if(expectedIds.every(id=>{const t=observed.tabs.find(t=>t.id===id);return t&&!t.missing&&t.groupId===-1&&!t.attached}))break;
  await new Promise(r=>setTimeout(r,100));
 }
 fs.writeFileSync(output,JSON.stringify(observed,null,2)+'\n');
 if(mode==='capture')assert(observed.groups.length===2&&observed.tabs.length===2&&observed.tabs.every(t=>!t.missing&&t.groupId>=0&&t.attached),'two owned tab groups did not become active');
 if(mode==='stopped-a'){const aGroup=expected.groups.find(g=>g.title.includes('rel-A-'))?.id;assert(expected.tabs.every(old=>{const t=observed.tabs.find(t=>t.id===old.id);return t&&!t.missing&&(old.groupId===aGroup?t.groupId===-1&&!t.attached:t.groupId===old.groupId&&t.attached)}),'failed/stopped A did not release independently of B');}
 if(mode==='released')assert(expectedIds.every(id=>{const t=observed.tabs.find(t=>t.id===id);return t&&!t.missing&&t.groupId===-1&&!t.attached}),'owned tabs were not all preserved, detached and ungrouped');
}finally{if(session)try{await send('Target.detachFromTarget',{sessionId:session})}catch{}clearTimeout(timer);ws.close()}
