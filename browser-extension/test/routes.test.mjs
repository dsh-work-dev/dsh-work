import test from 'node:test';
import assert from 'node:assert/strict';
import vm from 'node:vm';
import {fileURLToPath} from 'node:url';
import {build} from '../../frontend/node_modules/esbuild/lib/main.js';
const source=fileURLToPath(new URL('../src/routes.ts',import.meta.url));
const bundle=await build({stdin:{contents:`import {createRoutes} from ${JSON.stringify(source)}; globalThis.makeRoutes=createRoutes;`,resolveDir:process.cwd()},bundle:true,format:'iife',write:false,loader:{'.png':'dataurl','.svg':'text'}});
const tick=async()=>{for(let i=0;i<8;i++)await new Promise(r=>setImmediate(r))};
const A='s-'+ 'a'.repeat(32),B='s-'+'b'.repeat(32);
const attach=(routes,route,tabId)=>routes.receive({kind:'wire',route,payload:{id:tabId,method:'chrome.debugger.attach',params:[{tabId},'1.3']}});
function event(){const f=new Set();return{addListener:v=>f.add(v),removeListener:v=>f.delete(v),emit:(...args)=>{for(const v of f)v(...args)}}}
const activeRoutes=[];test.afterEach(()=>{for(const r of activeRoutes.splice(0))r.disconnect()});
function fixture(){
 const tabs=new Map(),groups=new Map(),attached=new Set(),sent=[];let next=0,gid=0,releaseCreate,delayCreate=false,releaseGroup,delayGroup=false,delayIndicator=false,releaseIndicator,stallCleanup=false,stallRendering=false,holdClick=false,releaseClick,unavailable=false;const physicalInputs=[],evaluations=[],installers=[];
 const chrome={storage:{session:{get:async()=>({ownedGroups:[]}),set:async()=>{}}},
 tabs:{onUpdated:event(),onCreated:event(),onRemoved:event(),get:async id=>tabs.get(id),
 create:async()=>{const tab={id:++next,url:'about:blank',groupId:-1};tabs.set(tab.id,tab);if(delayCreate)await new Promise(r=>releaseCreate=r);return tab},
 group:async({tabIds,groupId})=>{if(delayGroup)await new Promise(r=>releaseGroup=r);const id=groupId??++gid;for(const t of tabIds)tabs.get(t).groupId=id;return id},
 ungroup:async ids=>{for(const id of ids){groups.delete(tabs.get(id).groupId);tabs.get(id).groupId=-1}},
 remove:async()=>{throw Error('foreign tab remove must not run')}},
 tabGroups:{update:async(id,style)=>groups.set(id,{id,...style})},
 debugger:{onEvent:event(),onDetach:event(),attach:async({tabId})=>attached.add(tabId),detach:async({tabId})=>attached.delete(tabId),sendCommand:async(_target,method,params)=>{if(method==='Page.addScriptToEvaluateOnNewDocument')installers.push(params.source);if(method==='Runtime.evaluate')evaluations.push({tabId:_target.tabId,expression:params.expression});if(stallRendering&&method==='Page.getFrameTree')await new Promise(()=>{});if(stallCleanup&&method==='Page.removeScriptToEvaluateOnNewDocument')await new Promise(()=>{});if(delayIndicator&&method==='Page.addScriptToEvaluateOnNewDocument')await new Promise(r=>releaseIndicator=r);if(method==='Input.dispatchMouseEvent')physicalInputs.push(params.type);if(method==='Page.getFrameTree')return{frameTree:{frame:{id:'root'}}};if(method==='Page.createIsolatedWorld')return{executionContextId:1};if(method==='Runtime.evaluate'&&params?.expression?.includes('.prepareClick('))return{result:{value:{status:unavailable?'unavailable':'prepared',token:1}}};if(method==='Runtime.evaluate'&&params?.expression?.includes('.waitClick(')){const value=holdClick?await new Promise(r=>releaseClick=r):{status:'ready'};return{result:{value}}}return{}}},
 action:{setBadgeText:async()=>{},setBadgeBackgroundColor:async()=>{},setTitle:async()=>{}}};
 const context=vm.createContext({chrome,WebSocket:{OPEN:1},TextEncoder,setTimeout,clearTimeout,setInterval,clearInterval,console:{log(){},error(){}}});vm.runInContext(bundle.outputFiles[0].text,context);
 const routes=context.makeRoutes(v=>sent.push(v));activeRoutes.push(routes);
 return{routes,tabs,groups,attached,sent,physicalInputs,evaluations,installers,emitDebugger:(tabId,method,params)=>chrome.debugger.onEvent.emit({tabId},method,params),holdClick(){holdClick=true},finishClick:status=>releaseClick({status}),unavailable(){unavailable=true},stallRendering(){stallRendering=true},stallCleanup(){stallCleanup=true},delayIndicator(){delayIndicator=true},releaseIndicator:()=>releaseIndicator(),delayCreate(){delayCreate=true},releaseCreate:()=>releaseCreate(),delayGroup(){delayGroup=true},releaseGroup:()=>releaseGroup()};
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

test('close while indicator installs still releases the physical attachment',async()=>{
 const f=fixture();f.delayIndicator();
 await f.routes.receive({kind:'open',route:A,wireProtocol:2,title:'A'});await attach(f.routes,A,1);await tick();
 assert.equal(f.attached.size,1);await f.routes.receive({kind:'close',route:A});await tick();
 assert.equal(f.attached.size,0);f.releaseIndicator();await tick();assert.equal(f.attached.size,0);f.routes.disconnect();
});

test('unresponsive indicator cleanup cannot hold debugger release',async()=>{
 const f=fixture();await f.routes.receive({kind:'open',route:A,wireProtocol:2,title:'A'});await attach(f.routes,A,1);await tick();
 f.stallCleanup();await f.routes.receive({kind:'close',route:A});
 await new Promise(r=>setTimeout(r,350));assert.equal(f.attached.size,0);f.routes.disconnect();
});

test('stalled optional renderer does not delay a real Input command',async()=>{
 const f=fixture();f.stallRendering();await f.routes.receive({kind:'open',route:A,wireProtocol:2,title:'A'});await attach(f.routes,A,1);await tick();
 await f.routes.receive({kind:'wire',route:A,payload:{id:999,method:'chrome.debugger.sendCommand',params:[{tabId:1},'Input.dispatchMouseEvent',{type:'mousePressed',x:12,y:20}]}});await tick();
 assert(f.sent.some(m=>m.payload?.id===999&&!m.payload.error));f.routes.disconnect();
});

const input=(f,id,type)=>f.routes.receive({kind:'wire',route:A,payload:{id,method:'chrome.debugger.sendCommand',params:[{tabId:1},'Input.dispatchMouseEvent',{type,x:200,y:100,button:'left',clickCount:1}]}});
async function gatedFixture(){const f=fixture();await f.routes.receive({kind:'open',route:A,wireProtocol:2,title:'A'});await attach(f.routes,A,1);await tick();await f.routes.receive({kind:'wire',route:A,payload:{id:998,method:'dsh.indicator',params:{running:true,action:'browser_click'}}});await tick();return f}
test('click gate holds press and release in original order until arrival',async()=>{const f=await gatedFixture();f.holdClick();await input(f,1000,'mousePressed');await input(f,1001,'mouseReleased');await tick();assert.deepEqual(f.physicalInputs,[]);f.finishClick('ready');await tick();assert.deepEqual(f.physicalInputs,['mousePressed','mouseReleased']);f.routes.disconnect()});
test('route close while waiting prevents both physical press and release',async()=>{const f=await gatedFixture();f.holdClick();await input(f,1000,'mousePressed');await input(f,1001,'mouseReleased');await tick();await f.routes.receive({kind:'close',route:A});f.finishClick('ready');await tick();assert.deepEqual(f.physicalInputs,[]);f.routes.disconnect()});
test('invalidated target cannot dispatch a pending click',async()=>{const f=await gatedFixture();f.holdClick();await input(f,1000,'mousePressed');await input(f,1001,'mouseReleased');await tick();f.finishClick('cancelled');await tick();assert.deepEqual(f.physicalInputs,[]);assert(f.sent.some(m=>m.payload?.id===1000&&m.payload.error==='BROWSER_CLICK_INTERRUPTED'));f.routes.disconnect()});
test('known unavailable optional renderer falls back without blocking input',async()=>{const f=await gatedFixture();f.unavailable();await input(f,1000,'mousePressed');await input(f,1001,'mouseReleased');await tick();assert.deepEqual(f.physicalInputs,['mousePressed','mouseReleased']);f.routes.disconnect()});
test('unknown arrival is bounded and cancels instead of dispatching stale input',async()=>{const f=await gatedFixture();f.holdClick();await input(f,1000,'mousePressed');await input(f,1001,'mouseReleased');await new Promise(r=>setTimeout(r,450));assert.deepEqual(f.physicalInputs,[]);assert(f.sent.some(m=>m.payload?.id===1000&&m.payload.error==='INDICATOR_TIMEOUT'));f.routes.disconnect()});

const command=(f,id,tabId,method,p={})=>f.routes.receive({kind:'wire',route:A,payload:{id,method:'chrome.debugger.sendCommand',params:[{tabId},method,p]}});
const displayUpdates=f=>f.evaluations.flatMap(({tabId,expression})=>{const at=expression.lastIndexOf('?.update(');if(at<0||!expression.includes('?.resource()'))return[];return[{tabId,...JSON.parse(expression.slice(at+9).split(');')[0])}]});
async function addSecondTab(f){await f.routes.receive({kind:'wire',route:A,payload:{id:3000,method:'chrome.tabs.create',params:[{url:'about:blank'}]}});await tick();await attach(f.routes,A,2);await tick()}
test('attachment and tool status cannot select an Agent display tab',async()=>{const f=fixture();await f.routes.receive({kind:'open',route:A,wireProtocol:2,title:'A'});await attach(f.routes,A,1);await tick();await f.routes.receive({kind:'wire',route:A,payload:{id:3001,method:'dsh.indicator',params:{running:true,action:'browser_snapshot'}}});await tick();assert(displayUpdates(f).every(v=>v.active===false));await command(f,3002,1,'Page.bringToFront');await tick();await addSecondTab(f);assert(displayUpdates(f).filter(v=>v.tabId===2).every(v=>v.active===false));await command(f,3003,2,'Page.bringToFront');await tick();assert.equal(displayUpdates(f).filter(v=>v.tabId===1).at(-1).active,false);assert.equal(displayUpdates(f).filter(v=>v.tabId===2).at(-1).active,true)});
test('navigation invalidates a waiting click even if renderer later reports ready',async()=>{const f=await gatedFixture();f.holdClick();await input(f,3100,'mousePressed');await input(f,3101,'mouseReleased');await tick();f.emitDebugger(1,'Page.frameNavigated',{});f.finishClick('ready');await tick();assert.deepEqual(f.physicalInputs,[]);assert(f.evaluations.some(v=>v.expression==='globalThis.__dshIndicator?.reset()'))});
test('Agent tab switch cancels the previous pending press and release',async()=>{const f=await gatedFixture();await addSecondTab(f);f.holdClick();await input(f,3200,'mousePressed');await input(f,3201,'mouseReleased');await tick();await command(f,3202,2,'Page.bringToFront');f.finishClick('ready');await tick();assert.deepEqual(f.physicalInputs,[]);assert(f.evaluations.some(v=>v.tabId===1&&v.expression==='globalThis.__dshIndicator?.suspend()'))});

test('serialized installer retires legacy instances but reuses its current version',async()=>{const f=fixture();await f.routes.receive({kind:'open',route:A,wireProtocol:2,title:'A'});await attach(f.routes,A,1);await tick();const source=f.installers[0];assert(source);let disposed=0,deferred=0;const window={};window.top=window;const sandbox={window,document:{documentElement:null,addEventListener:()=>deferred++},__dshIndicator:{dispose:()=>disposed++}};vm.runInNewContext(source,sandbox);assert.equal(disposed,1);assert.equal(deferred,1);sandbox.__dshIndicator.version='document-position-v2';vm.runInNewContext(source,sandbox);assert.equal(disposed,1);assert.equal(deferred,1)});
