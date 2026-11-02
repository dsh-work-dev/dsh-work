import test from 'node:test';
import assert from 'node:assert/strict';
import vm from 'node:vm';
import {fileURLToPath} from 'node:url';
import {build} from '../../frontend/node_modules/esbuild/lib/main.js';
const source=fileURLToPath(new URL('../src/routes.ts',import.meta.url));
const bundle=await build({stdin:{contents:`import {createRoutes} from ${JSON.stringify(source)}; globalThis.makeRoutes=createRoutes;`,resolveDir:process.cwd()},bundle:true,format:'iife',write:false});
const tick=async()=>{for(let i=0;i<8;i++)await new Promise(r=>setImmediate(r))};
const A='s-'+ 'a'.repeat(32),B='s-'+'b'.repeat(32);
const attach=(routes,route,tabId)=>routes.receive({kind:'wire',route,payload:{id:tabId,method:'chrome.debugger.attach',params:[{tabId},'1.3']}});
function event(){const f=new Set();return{addListener:v=>f.add(v),removeListener:v=>f.delete(v)}}
function fixture(){
 const tabs=new Map(),groups=new Map(),attached=new Set(),sent=[];let next=0,gid=0,releaseCreate,delayCreate=false,releaseGroup,delayGroup=false;
 const chrome={storage:{session:{get:async()=>({ownedGroups:[]}),set:async()=>{}}},
 tabs:{onUpdated:event(),onCreated:event(),onRemoved:event(),get:async id=>tabs.get(id),
 create:async()=>{const tab={id:++next,url:'about:blank',groupId:-1};tabs.set(tab.id,tab);if(delayCreate)await new Promise(r=>releaseCreate=r);return tab},
 group:async({tabIds,groupId})=>{if(delayGroup)await new Promise(r=>releaseGroup=r);const id=groupId??++gid;for(const t of tabIds)tabs.get(t).groupId=id;return id},
 ungroup:async ids=>{for(const id of ids){groups.delete(tabs.get(id).groupId);tabs.get(id).groupId=-1}},
 remove:async()=>{throw Error('foreign tab remove must not run')}},
 tabGroups:{update:async(id,style)=>groups.set(id,{id,...style})},
 debugger:{onEvent:event(),onDetach:event(),attach:async({tabId})=>attached.add(tabId),detach:async({tabId})=>attached.delete(tabId),sendCommand:async()=>({})},
 action:{setBadgeText:async()=>{},setBadgeBackgroundColor:async()=>{},setTitle:async()=>{}}};
 const context=vm.createContext({chrome,WebSocket:{OPEN:1},TextEncoder,setTimeout,console:{log(){},error(){}}});vm.runInContext(bundle.outputFiles[0].text,context);
 const routes=context.makeRoutes(v=>sent.push(v));
 return{routes,tabs,groups,attached,sent,delayCreate(){delayCreate=true},releaseCreate:()=>releaseCreate(),delayGroup(){delayGroup=true},releaseGroup:()=>releaseGroup()};
}
test('real fork: two groups, foreign tab rejected, A release preserves B',async()=>{
 const f=fixture();
 await f.routes.receive({kind:'open',route:A,wireProtocol:2,title:'😀'.repeat(31)});await tick();
 await attach(f.routes,A,1);await tick();
 await f.routes.receive({kind:'open',route:B,wireProtocol:2,title:'B'});await tick();
 await attach(f.routes,B,2);await tick();
 assert.equal(f.groups.size,2);assert.equal(f.attached.size,2);
 const styles=[...f.groups.values()];assert.notEqual(styles[0].color,styles[1].color);assert.equal(styles[0].title,'DSH Work · '+'😀'.repeat(30)+'…');
 await f.routes.receive({kind:'wire',route:A,payload:{id:42,method:'chrome.debugger.sendCommand',params:[{tabId:2},'Runtime.evaluate',{}]}});await tick();
 assert(f.sent.some(m=>m.route===A&&m.payload?.id===42&&m.payload.error==='Tab outside this connection'));
 await f.routes.receive({kind:'close',route:A});await tick();assert.deepEqual([...f.attached],[2]);
 f.routes.disconnect();await tick();assert.equal(f.attached.size,0);assert.equal(f.groups.size,0);assert.equal(f.tabs.size,2);
});
test('close during tab creation does not attach or resurrect a route',async()=>{
 const f=fixture();f.delayCreate();const opening=f.routes.receive({kind:'open',route:A,wireProtocol:2,title:'A'});await tick();
 await f.routes.receive({kind:'close',route:A});f.releaseCreate();await opening;await tick();
 assert.equal(f.attached.size,0);assert.equal(f.groups.size,0);assert.equal(f.tabs.size,1);f.routes.disconnect();
});
test('late grouping after release is undone while retaining the tab',async()=>{
 const f=fixture();f.delayGroup();await f.routes.receive({kind:'open',route:A,wireProtocol:2,title:'A'});await attach(f.routes,A,1);await tick();
 await f.routes.receive({kind:'close',route:A});f.releaseGroup();await tick();
 assert.equal(f.attached.size,0);assert.equal(f.groups.size,0);assert.equal([...f.tabs.values()][0].groupId,-1);f.routes.disconnect();
});
