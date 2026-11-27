import test from 'node:test';
import assert from 'node:assert/strict';
import vm from 'node:vm';
import {readFileSync} from 'node:fs';
import {createRequire} from 'node:module';
import './origin.js';
const require=createRequire(new URL('../../frontend/package.json',import.meta.url));
const ts=require('typescript');
const tick=()=>new Promise(resolve=>setImmediate(resolve));
const deferred=()=>{let resolve;const promise=new Promise(r=>resolve=r);return{promise,resolve}};

test('ancestor clipping hides retained pages and late Present cannot revive an unmounted slot',async()=>{
 const requests=[],listeners=new Map();let id=0,collapsed=false,width=200;const mutations=[],resizes=[];
 const element={isConnected:true,getBoundingClientRect:()=>({left:0,top:0,right:width,bottom:100}),getClientRects:()=>[1],closest:()=>({closest:()=>({hasAttribute:()=>!collapsed})})};
 const parent={postMessage:m=>requests.push(m)};
 const intersections=[],timers=new Map();
 const context=vm.createContext({console,URL,decodeURIComponent,location:{ancestorOrigins:['http://shell'],hash:'#__dsh_work_doc=doc'},parent,window:{},innerWidth:500,innerHeight:500,crypto:{randomUUID:()=>String(++id)},setTimeout:(fn,ms)=>{const n=++id;if(ms===25)timers.set(n,fn);return n},clearTimeout:n=>timers.delete(n),setInterval:()=>++id,clearInterval(){},ResizeObserver:class{constructor(fn){resizes.push(fn)}observe(){}disconnect(){}},IntersectionObserver:class{constructor(fn){this.fn=fn;intersections.push(this)}observe(target){this.target=target;this.emit(true)}emit(visible){this.fn([{target:this.target,isIntersecting:visible,intersectionRatio:visible?1:0}])}disconnect(){}},MutationObserver:class{constructor(fn){mutations.push(fn)}observe(){}},getComputedStyle:()=>({visibility:'visible'}),document:{head:{append(){}},documentElement:{},createElement:()=>({}),querySelectorAll:()=>[],getElementById:()=>element},addEventListener:(type,fn)=>listeners.set(type,[...(listeners.get(type)||[]),fn]),removeEventListener(){}});
 vm.runInContext(readFileSync(new URL('./embedded_browser.js',import.meta.url),'utf8'),context);
 const respond=(m,value)=>{for(const fn of listeners.get('message')||[])fn({source:parent,origin:'http://shell',data:{version:1,type:'dsh-work/browser-result',id:m.id,value}})};
 for(const fn of listeners.get('DOMContentLoaded')||[])fn();await tick();
 assert.equal(requests[0].action,'modal-overlay');assert.equal(requests[0].blocked,false);respond(requests.shift(),true);await tick();
 const page=context.__DSH_WORK_BROWSER_PROVIDER__.createPage('session',{key:'tab',persist(){}});
 const unmount=page.presentation.mount('viewport');await tick();
 assert.equal(requests[0].address,'');respond(requests.shift(),{state:'empty'});await tick();
 page.frame.loadUrl({url:'https://example.test'});await tick();
 assert.equal(requests[0].visible,true);const first=requests.shift();
 // The slot's own bounds do not change when an ancestor clips/translates it.
 respond(first,{state:'ready',url:'https://example.test',visible:true});await tick();
 collapsed=true;mutations[0]();await tick();
 assert.equal(requests[0].visible,false,'retained dock collapse hides before CSS transition or intersection changes');respond(requests.shift(),{state:'ready',url:'https://example.test',visible:false});await tick();
 collapsed=false;mutations[0]();for(const [key,fn] of timers){timers.delete(key);fn()}await tick();
 assert.equal(requests[0].visible,true);respond(requests.shift(),{state:'ready',url:'https://example.test',visible:true});await tick();
 width=210;resizes[0]();width=220;resizes[0]();width=230;resizes[0]();
 assert.equal(timers.size,0,'layout must not wait on background-throttled timers');
 for(const [key,fn] of timers){timers.delete(key);fn()}await tick();
 assert.equal(requests[0].rect.width,230,'resize dispatch reads the newest geometry');respond(requests.shift(),{state:'ready',url:'https://example.test',visible:true});await tick();

 intersections[0].emit(false);for(const [key,fn] of timers){timers.delete(key);fn()}await tick();
 assert.equal(requests[0].visible,false,'ancestor clip must produce a native Hide without ResizeObserver');respond(requests.shift(),{state:'ready',url:'https://example.test',visible:false});await tick();
 intersections[0].emit(true);for(const [key,fn] of timers){timers.delete(key);fn()}await tick();
 assert.equal(requests[0].visible,true,'same retained document may show after full intersection');const shown=requests.shift();
 unmount();intersections[0].emit(true);respond(shown,{state:'ready',url:'https://example.test',visible:true});await tick();
 assert.equal(requests[0].visible,false,'inflight first Present lacked post-unmount hide');respond(requests.shift(),{state:'ready',visible:false});await tick();
 assert.equal(requests[0].visible,false,'unmount must enqueue cleanup independently of registered');respond(requests.shift(),{state:'controlPaused',url:'https://example.test',visible:false,canGoBack:true,canGoForward:true});await tick();
 assert.equal(page.frame.getSnapshot().error.description,'Browser control is paused.');assert.equal(page.frame.getSnapshot().error.code,-32099);assert.equal(page.frame.getSnapshot().canGoBack,false);assert.equal(page.frame.getSnapshot().canGoForward,false);
 page.frame.reload();page.frame.goBack();page.frame.goForward();page.frame.loadUrl({url:'https://example.test/new'});await tick();assert.equal(requests.length,0,'paused controls must not reload or navigate retained page');
 const disposed=page.frame.dispose();await tick();assert.equal(requests[0].action,'detach');respond(requests.shift(),true);await disposed;
 // Also retain the first-create race: no successful registration existed
 // when the first visible native Present was sent and then unmounted.
 const fresh=context.__DSH_WORK_BROWSER_PROVIDER__.createPage('session',{key:'fresh',persist(){}});
 const unmountFresh=fresh.presentation.mount('viewport');await tick();respond(requests.shift(),{state:'empty'});await tick();
 fresh.frame.loadUrl({url:'https://example.test'});await tick();const pending=requests.shift();assert.equal(pending.visible,true);
 unmountFresh();respond(pending,{state:'ready',url:'https://example.test',visible:true});await tick();
 assert.equal(requests[0].visible,false);respond(requests.shift(),{state:'ready',visible:false});await tick();
 assert.equal(requests[0].visible,false);respond(requests.shift(),{state:'ready',visible:false});await tick();
 const closing=fresh.frame.dispose();await tick();respond(requests.shift(),true);await closing;
 // A daemon descriptor may arrive before this new WebView has any DSH page
 // checkpoint. Publish saved metadata, not a fictional live frame or load error.
 const saved=[],changes=[];
 const restored=context.__DSH_WORK_BROWSER_PROVIDER__.createPage('session',{key:'restore',persist:s=>saved.push(s)});
 restored.frame.subscribe(()=>changes.push({saved:saved.length,frame:restored.frame.getSnapshot()}));
 restored.presentation.mount('viewport');await tick();
 respond(requests.shift(),{state:'NeedsReload',url:'https://example.test/saved',title:'Saved page',visible:false});await tick();
 assert.equal(requests.length,0,'descriptor arrival must not navigate or recover automatically');
 assert.equal(saved.length,1);assert.equal(saved[0].entries[0].url,'https://example.test/saved');assert.equal(saved[0].entries[0].title,'Saved page');
 assert.equal(changes.length,1,'an initially empty snapshot still needs a checkpoint notification');assert.equal(changes[0].saved,1);
 assert.equal(restored.frame.getSnapshot().target,undefined,'saved address is not a live document');assert.equal(restored.frame.getSnapshot().error,undefined,'standard restore state is not a load failure');
 restored.frame.loadUrl({url:'https://example.test/saved'});await tick();assert.equal(requests[0].action,'update');
 respond(requests.shift(),{state:'NeedsReload',url:'https://example.test/saved'});await tick();assert.equal(requests[0].action,'recover','only explicit restore requests a new document');
 respond(requests.shift(),{state:'empty'});await tick();assert.equal(requests[0].action,'update');
 respond(requests.shift(),{state:'ready',url:'https://example.test/saved',title:'Saved page',visible:true});await tick();assert.equal(restored.frame.getSnapshot().target.url,'https://example.test/saved');
 const finish=restored.frame.dispose();await tick();respond(requests.shift(),true);await finish;
});

test('shell document revision rejects an old update delayed in Capability',async()=>{
 const source=readFileSync(new URL('../../frontend/src/embedded-browser.ts',import.meta.url),'utf8');
 const capability=deferred();let updates=0;
 const bindings={BeginDocument:async()=>true,Capability:()=>capability.promise,Update:async()=>{updates++}};
 const exports={};const context=vm.createContext({exports,require:()=>({EmbeddedBrowserService:bindings,bridgeOrigin:globalThis.__DSH_WORK_ORIGINS__.origin}),URL,Promise,Error,Number,ResizeObserver:class{observe(){}}});
 vm.runInContext(ts.transpileModule(source,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText,context);
 const frame={getAttribute:()=> 'http://dsh/#__dsh_work_doc=old',contentWindow:{postMessage(){}},getBoundingClientRect:()=>({left:0,top:0,right:300,bottom:300,width:300,height:300}),clientWidth:300,clientHeight:300};
 const bridge=exports.embeddedBrowserBridge(frame,async()=>{});
 await bridge.retireDocument('old');bridge.activateDocument('old');
 bridge.handle({data:{version:1,type:'dsh-work/browser',id:'request',action:'update',document:'old',slot:'s',address:'https://example.test',visible:true,rect:{x:0,y:0,width:100,height:100}}});
 const retired=bridge.retireDocument('new');capability.resolve(true);await retired;bridge.activateDocument('new');await tick();
 assert.equal(updates,0);assert.equal(bridge.acceptsDocument('old'),false);assert.equal(bridge.acceptsDocument('new'),true);
});

test('frame fallback/reload waits for native retirement ACK and drops superseded navigation',async()=>{
 const source=readFileSync(new URL('../../frontend/src/shell.ts',import.meta.url),'utf8');
 const slice=source.slice(source.indexOf('  let desiredWorkerURL='),source.indexOf('  Events.On("worker-url"'));
 const pending=[],events=[];let frameRevision=0;
 const context=vm.createContext({crypto:{randomUUID:()=>String(pending.length+1)},location:{href:'http://shell'},URL,frameRevision,embeddedBrowser:{retireDocument:token=>{events.push('retire');const d=deferred();pending.push(d);return d.promise},activateDocument:()=>events.push('activate')},clearDshModal:()=>events.push('clear-intent'),ShellService:{ResetBuiltinComponents:async()=>{},BeginBuiltinComponents:async()=>{}},report:()=>e=>{throw e},setMenu(){},document:{documentElement:{removeAttribute(){},setAttribute(){}}},frame:{hidden:false,src:'old',removeAttribute(){events.push('remove-src')}},hostSurface:{removeAttribute(){events.push('host-visible')},setAttribute(){}}});
 vm.runInContext(ts.transpileModule(slice+'\nglobalThis.show=show;globalThis.reloadFrame=reloadFrame;',{compilerOptions:{target:ts.ScriptTarget.ES2022}}).outputText,context);
 context.show('http://dsh/');assert.equal(context.frame.src,'old');context.show('');assert.ok(!events.includes('host-visible'));
 pending[0].resolve(true);await tick();assert.equal(context.frame.src,'old','superseded doc became visible');
 pending[1].resolve(true);await tick();assert.ok(events.includes('host-visible'));assert.ok(events.includes('clear-intent'));
});

test('selection forwards current document and Worker generation as intent only',async()=>{
 const source=readFileSync(new URL('../../frontend/src/embedded-browser.ts',import.meta.url),'utf8');
 const calls=[];const bindings={BeginDocument:async()=>true,Capability:async()=>true,SelectControl:async(...args)=>{calls.push(args);return{state:'waiting-for-turn'}}};
 const exports={};const context=vm.createContext({exports,require:()=>({EmbeddedBrowserService:bindings,bridgeOrigin:globalThis.__DSH_WORK_ORIGINS__.origin}),URL,Promise,Error,Number,ResizeObserver:class{observe(){}}});
 vm.runInContext(ts.transpileModule(source,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText,context);
 const frame={src:'http://dsh/?generation=actual-worker',getAttribute:()=> 'http://dsh/?generation=actual-worker',contentWindow:{postMessage(){}}};
 const bridge=exports.embeddedBrowserBridge(frame,async()=>{});await bridge.retireDocument('current-document');bridge.activateDocument('current-document');
 const message={version:1,type:'dsh-work/browser',id:'select',action:'select-control',document:'current-document',session:'intent-session',slot:'intent-session/tab',lease:'view'};
 bridge.handle({data:{...message,document:'retired-document'}});await tick();assert.equal(calls.length,0);
 bridge.handle({data:message});await tick();assert.deepEqual(Array.from(calls[0]),['current-document','actual-worker','intent-session','intent-session/tab','view']);
});


test('shell preserves visible then hidden intent order across delayed Capability',async()=>{
 const source=readFileSync(new URL('../../frontend/src/embedded-browser.ts',import.meta.url),'utf8');
 const capability=deferred(),calls=[];let checks=0;
 const bindings={BeginDocument:async()=>true,Capability:()=>++checks===1?capability.promise:Promise.resolve(true),Update:async(...args)=>{calls.push(args);return{visible:args[4]}}};
 const exports={};const context=vm.createContext({exports,require:()=>({EmbeddedBrowserService:bindings,bridgeOrigin:globalThis.__DSH_WORK_ORIGINS__.origin}),URL,Promise,Error,Number,ResizeObserver:class{observe(){}}});
 vm.runInContext(ts.transpileModule(source,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText,context);
 const frame={getAttribute:()=> 'http://dsh/',contentWindow:{postMessage(){}},getBoundingClientRect:()=>({left:0,top:40,right:300,bottom:340,width:300,height:300}),clientWidth:300,clientHeight:300};
 const bridge=exports.embeddedBrowserBridge(frame,async()=>{});await bridge.retireDocument('document');bridge.activateDocument('document');
 const message={version:1,type:'dsh-work/browser',action:'update',document:'document',slot:'s',address:'',rect:{x:0,y:0,width:100,height:100}};
 bridge.handle({data:{...message,id:'show',visible:true}});await tick();
 bridge.handle({data:{...message,id:'hide',visible:false}});await tick();assert.equal(checks,1,'hide must not overtake the earlier pending capability');
 capability.resolve(true);await tick();await tick();
 assert.deepEqual(calls.map(call=>call[4]),[true,false],'the final native operation must remain Hide');
});
