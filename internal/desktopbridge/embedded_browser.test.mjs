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

test('shell resamples the last resize after native bounds settle and cancels it on document retirement', async()=>{
 const messages=[], timers=new Map();
 let resize, sequence=0;
 const exports={};
 const context=vm.createContext({
  exports,
  require:()=>({subscribeLocale:()=>{}, EmbeddedBrowserService:{BeginDocument:async()=>true}, bridgeOrigin:()=> 'https://worker.test'}),
  ResizeObserver:class{constructor(callback){resize=callback} observe(){}},
  setTimeout:callback=>{timers.set(++sequence,callback);return sequence},
  clearTimeout:id=>timers.delete(id)
 });
 const source=readFileSync(new URL('../../frontend/src/embedded-browser.ts',import.meta.url),'utf8');
 vm.runInContext(ts.transpileModule(source,{compilerOptions:{module:ts.ModuleKind.CommonJS}}).outputText,context);
 const bridge=exports.embeddedBrowserBridge({getAttribute:()=> 'https://worker.test', contentWindow:{postMessage:message=>messages.push(message)}});
 await bridge.retireDocument('current');
 bridge.activateDocument('current');
 resize();
 resize();
 assert.deepEqual(messages.map(message=>message.remeasure),[false,false]);
 assert.equal(timers.size,1);
 const [id,callback]=timers.entries().next().value;
 timers.delete(id);
 callback();
 assert.equal(messages.at(-1).remeasure,true);
 assert.equal(messages.at(-1).document,'current');
 resize();
 await bridge.retireDocument('replacement');
 bridge.activateDocument('replacement');
 assert.equal(timers.size,0,'a retired document must not remeasure its replacement');
});

test('ancestor clipping hides retained pages and late Present cannot revive an unmounted slot',async()=>{
 const requests=[],listeners=new Map();let id=0,collapsed=false,width=200,left=0,visibility='visible';const mutations=[],resizes=[];
 const element={isConnected:true,getBoundingClientRect:()=>({left,top:0,right:left+width,bottom:100}),getClientRects:()=>[1],closest:()=>({closest:()=>({hasAttribute:()=>!collapsed})})};
 const parent={postMessage:m=>requests.push(m)};
 const intersections=[],timers=new Map();
 class IntersectionObserver {
  constructor(fn){this.fn=fn;intersections.push(this)}
  observe(target){this.target=target;if(this.nextSample)this.fn([this.nextSample]);else this.emit(true)}
  emit(visible){this.fn([{target:this.target,isIntersecting:visible,intersectionRatio:visible?1:0}])}
  takeRecords(){return []}
  unobserve(){}
  disconnect(){}
 }
 const context=vm.createContext({console,URL,decodeURIComponent,location:{ancestorOrigins:['wails://localhost'],hash:'#__dsh_work_doc=doc'},parent,window:{},innerWidth:500,innerHeight:500,crypto:{randomUUID:()=>String(++id)},setTimeout:(fn,ms)=>{const n=++id;if(ms===25)timers.set(n,fn);return n},clearTimeout:n=>timers.delete(n),setInterval:()=>++id,clearInterval(){},ResizeObserver:class{constructor(fn){resizes.push(fn)}observe(){}disconnect(){}},IntersectionObserver,MutationObserver:class{constructor(fn){mutations.push(fn)}observe(){}},getComputedStyle:()=>({visibility}),document:{head:{append(){}},documentElement:{},createElement:()=>({}),querySelectorAll:()=>[],getElementById:()=>element},addEventListener:(type,fn)=>listeners.set(type,[...(listeners.get(type)||[]),fn]),removeEventListener(){}});
 vm.runInContext(readFileSync(new URL('./embedded_browser.js',import.meta.url),'utf8'),context);
 const respond=(m,value)=>{for(const fn of listeners.get('message')||[])fn({source:parent,origin:'wails://localhost',data:{version:1,type:'dsh-work/browser-result',id:m.id,value}})};
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
 collapsed=false;visibility='hidden';mutations[0]();await tick();
 assert.equal(requests.length,0,'an opening dock stays hidden while its computed visibility is hidden');
 visibility='visible';
 for(const fn of listeners.get('transitionend')||[])fn({propertyName:'opacity'});await tick();
 assert.equal(requests.length,0,'unrelated CSS transitions do not change native visibility');
 for(const fn of listeners.get('transitionend')||[])fn({propertyName:'visibility'});await tick();
 assert.equal(requests.length,1,'the visibility transition can end without another resize or intersection');
 assert.equal(requests[0].visible,true);respond(requests.shift(),{state:'ready',url:'https://example.test',visible:true});await tick();
 visibility='hidden';
 for(const fn of listeners.get('transitioncancel')||[])fn({propertyName:'visibility'});await tick();
 assert.equal(requests[0].visible,false,'canceling a visibility transition rechecks the current computed style');
 respond(requests.shift(),{state:'ready',url:'https://example.test',visible:false});await tick();
 visibility='visible';mutations[0]();await tick();
 respond(requests.shift(),{state:'ready',url:'https://example.test',visible:true});await tick();
 width=210;resizes[0]();width=220;resizes[0]();width=230;resizes[0]();
 assert.equal(timers.size,0,'layout must not wait on background-throttled timers');
 for(const [key,fn] of timers){timers.delete(key);fn()}await tick();
 assert.equal(requests[0].rect.width,230,'resize dispatch reads the newest geometry');respond(requests.shift(),{state:'ready',url:'https://example.test',visible:true});await tick();

 left=0.5;width=499.5;
 const bounds=element.getBoundingClientRect();
 const sample=left=>({target:element,isIntersecting:true,intersectionRatio:(500-left)/width,boundingClientRect:bounds,intersectionRect:{...bounds,left}});
 const partial=left=>intersections[0].fn([sample(left)]);
 partial(1);await tick();
 assert.equal(requests[0].visible,true,'WebKit edge rounding must preserve the same whole CSS pixels');
 assert.equal(requests[0].rect.x,1);
 assert.equal(requests[0].rect.width,499,'native bounds must exclude the fractional clipped edge');
 respond(requests.shift(),{state:'ready',url:'https://example.test',visible:true});await tick();
 partial(1.6);await tick();
 assert.equal(requests[0].visible,false,'clipping a whole pixel must still hide the native view');
 respond(requests.shift(),{state:'ready',url:'https://example.test',visible:false});await tick();
 // Both ratios stay between the same WebKit observer thresholds.
 intersections[0].nextSample=sample(1.4);
 const endTransform=target=>{for(const fn of listeners.get('transitionend')||[])fn({propertyName:'transform',target})};
 endTransform({contains:()=>false});await tick();
 assert.equal(requests.length,0,'an unrelated transform cannot refresh this viewport');
 const ancestor={contains:node=>node===element};
 endTransform(ancestor);await tick();
 assert.equal(requests[0]?.visible,true,'the final ancestor transform requires a fresh intersection sample');
 respond(requests.shift(),{state:'ready',visible:true});await tick();
 intersections[0].nextSample=sample(1.6);
 endTransform(ancestor);await tick();
 assert.equal(requests[0]?.visible,false,'a final whole-pixel clip hides the native view');
 respond(requests.shift(),{state:'ready',visible:false});await tick();
 intersections[0].emit(true);await tick();
 assert.equal(requests[0].visible,true);respond(requests.shift(),{state:'ready',visible:true});await tick();

 const setBlocked=(blocked,layoutRevision)=>{for(const fn of listeners.get('message')||[])fn({source:parent,origin:'wails://localhost',data:{version:1,type:'dsh-work/browser-blocked',document:'doc',blocked,layoutRevision}})};
 setBlocked(true,0);await tick();
 assert.equal(requests.length,0,'native occlusion does not change the provider layout intent');
 width=228;resizes[0]();await tick();
 assert.equal(requests[0].visible,true,'covered layout still reports the active slot');
 assert.equal(requests[0].layoutRevision,0);
 respond(requests.shift(),{state:'ready',visible:false});await tick();
 width=231;
 setBlocked(false,1);await tick();
 assert.equal(requests[0].rect.width,231,'resume measures the live layout without waiting for a resize event');
 assert.equal(requests[0].layoutRevision,1);
 await assert.rejects(context.__DSH_WORK_BROWSER_PROVIDER__.selectPage('tab'),/Visible browser page required/);
 respond(requests.shift(),{state:'ready',visible:true,lease:'restored-lease'});await tick();
 const selected=context.__DSH_WORK_BROWSER_PROVIDER__.selectPage('tab');await tick();
 assert.equal(requests[0].action,'select-control');respond(requests.shift(),{});await selected;
 setBlocked(true,0);await tick();
 assert.equal(requests.length,0,'a previous layout revision cannot block the restored pane');
 const notifyLayout=document=>{for(const fn of listeners.get('message')||[])fn({source:parent,origin:'wails://localhost',data:{version:1,type:'dsh-work/browser-layout',document,remeasure:true}})};
 notifyLayout('retired');await tick();assert.equal(requests.length,0);
 notifyLayout('doc');await tick();assert.equal(requests[0].rect.width,231,'window zoom remeasures even when the CSS rect is unchanged');
 respond(requests.shift(),{state:'ready',visible:true});await tick();
 width=232;resizes[0]();await tick();const pendingLayout=requests.shift();
 notifyLayout('doc');await tick();assert.equal(requests.length,0,'window remeasurement waits behind active layout');
 for(const fn of listeners.get('message')||[])fn({source:parent,origin:'wails://localhost',data:{version:1,type:'dsh-work/browser-result',id:pendingLayout.id,error:'CEF_FIXTURE_PRESENT_FAILED'}});
 await tick();assert.equal(requests[0]?.rect.width,232,'failure of the active layout does not lose a newer window refresh');
 respond(requests.shift(),{state:'ready',visible:true});await tick();

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
 // A replacement mount owns the slot even if the earlier mount's cleanup
 // and in-flight acknowledgement both arrive after the replacement.
 const remounted=context.__DSH_WORK_BROWSER_PROVIDER__.createPage('session',{key:'remount',persist(){}});
 const oldUnmount=remounted.presentation.mount('viewport');await tick();respond(requests.shift(),{state:'ready',visible:true});await tick();
 remounted.frame.loadUrl({url:'https://example.test/remount'});await tick();const oldPresent=requests.shift();
 const newUnmount=remounted.presentation.mount('viewport');oldUnmount();
 respond(oldPresent,{state:'ready',visible:true});await tick();
 assert.ok(requests[0]?.visible,'an old mount cannot send Hide over the replacement');
 respond(requests.shift(),{state:'ready',visible:true});await tick();
 assert.equal(requests.length,0);
 newUnmount();await tick();assert.equal(requests[0]?.visible,false,'the current mount still hides when unmounted');
 respond(requests.shift(),{state:'ready',visible:false});await tick();
 const remountedClosed=remounted.frame.dispose();await tick();respond(requests.shift(),true);await remountedClosed;
 // A daemon descriptor may arrive before this new WebView has any DSH page
 // checkpoint. Publish saved metadata, not a fictional live frame or load error.
 const saved=[],changes=[];
 const restored=context.__DSH_WORK_BROWSER_PROVIDER__.createPage('session',{key:'restore',persist:s=>saved.push(s)});
 restored.frame.subscribe(()=>changes.push({saved:saved.length,frame:restored.frame.getSnapshot()}));
 const unmountSaved=restored.presentation.mount('viewport');await tick();
 respond(requests.shift(),{state:'NeedsReload',url:'https://example.test/saved',title:'Saved page',visible:false});await tick();
 assert.equal(requests.length,0,'descriptor arrival must not navigate or recover automatically');
 assert.equal(saved.length,1);assert.equal(saved[0].entries[0].url,'https://example.test/saved');assert.equal(saved[0].entries[0].title,'Saved page');
 assert.equal(changes.length,1,'an initially empty snapshot still needs a checkpoint notification');assert.equal(changes[0].saved,1);
 assert.equal(restored.frame.getSnapshot().target,undefined,'saved address is not a live document');assert.equal(restored.frame.getSnapshot().error,undefined,'standard restore state is not a load failure');
 restored.frame.loadUrl({url:'https://example.test/saved'});await tick();assert.equal(requests[0].action,'update');
 const pendingRestore=requests.shift();
 unmountSaved();restored.presentation.mount('viewport');intersections.at(-1).emit(true);
 respond(pendingRestore,{state:'NeedsReload',url:'https://example.test/saved'});await tick();
 assert.equal(requests[0].action,'recover','an explicit restore survives a replacement viewport for the same tab');
 respond(requests.shift(),{state:'empty'});await tick();
 assert.equal(requests[0].action,'update');assert.equal(requests[0].visible,true,'restore uses the replacement viewport');
 respond(requests.shift(),{state:'ready',url:'https://example.test/saved',title:'Saved page',visible:true});await tick();assert.equal(restored.frame.getSnapshot().target.url,'https://example.test/saved');
 if(requests.length){
  assert.deepEqual(requests.map(request=>[request.action,request.visible]),[['update',true]]);
  respond(requests.shift(),{state:'ready',url:'https://example.test/saved',visible:true});await tick();
 }
 const finish=restored.frame.dispose();await tick();respond(requests.shift(),true);await finish;

 const retired=context.__DSH_WORK_BROWSER_PROVIDER__.createPage('session',{key:'retired-restore',persist(){}});
 retired.presentation.mount('viewport');await tick();
 respond(requests.shift(),{state:'NeedsReload',visible:false});await tick();
 retired.frame.loadUrl({url:'https://example.test/saved'});await tick();
 const retiredUpdate=requests.shift();
 const retire=retired.frame.dispose();
 respond(retiredUpdate,{state:'NeedsReload',visible:false});await tick();
 assert.deepEqual(requests.map(request=>request.action),['detach'],'a disposed tab cannot be restored by a late response');
 respond(requests.shift(),true);await retire;
});

test('shell document revision rejects an old update delayed in Capability',async()=>{
 const source=readFileSync(new URL('../../frontend/src/embedded-browser.ts',import.meta.url),'utf8');
 const capability=deferred();let updates=0;
 const bindings={BeginDocument:async()=>true,Availability:async()=>({state:'ready',code:'CEF_READY'}),Capability:()=>capability.promise,Update:async()=>{updates++}};
 const exports={};const context=vm.createContext({exports,require:()=>({t:key=>key,subscribeLocale:()=>()=>{},EmbeddedBrowserService:bindings,bridgeOrigin:globalThis.__DSH_WORK_ORIGINS__.origin}),URL,Promise,Error,Number,ResizeObserver:class{observe(){}}});
 vm.runInContext(ts.transpileModule(source,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText,context);
 const frame={getAttribute:()=> 'http://dsh/#__dsh_work_doc=old',contentWindow:{postMessage(){}},getBoundingClientRect:()=>({left:0,top:0,right:300,bottom:300,width:300,height:300}),clientWidth:300,clientHeight:300};
 const bridge=exports.embeddedBrowserBridge(frame,async()=>{});
 await bridge.retireDocument('old');bridge.activateDocument('old');
 bridge.handle({data:{version:1,type:'dsh-work/browser',id:'request',action:'update',document:'old',slot:'s',address:'https://example.test',visible:true,rect:{x:0,y:0,width:100,height:100}}});
 const retired=bridge.retireDocument('new');capability.resolve(true);await retired;bridge.activateDocument('new');await tick();
 assert.equal(updates,0);assert.equal(bridge.acceptsDocument('old'),false);assert.equal(bridge.acceptsDocument('new'),true);
});

test('overlapping occlusion sources only resume after the last source clears',async()=>{
 const source=readFileSync(new URL('../../frontend/src/embedded-browser.ts',import.meta.url),'utf8');
 const occlusions=[],messages=[],updates=[],cover=deferred(),picker=deferred();
 let pickerStarted=false;
 const bindings={BeginDocument:async()=>true,Availability:async()=>({state:'ready'}),Capability:async()=>true,
  Update:async(_document,_slot,address,bounds,visible)=>{updates.push({address,x:bounds.X,width:bounds.Width,visible});return{visible}},OpenArtifact:async()=>{pickerStarted=true;return picker.promise},SetOcclusion:async(document,blocked)=>{
   occlusions.push({document,blocked});return blocked?cover.promise:null;
  }};
 const exports={};
 const context=vm.createContext({exports,require:()=>({t:key=>key,subscribeLocale:()=>()=>{},EmbeddedBrowserService:bindings,bridgeOrigin:globalThis.__DSH_WORK_ORIGINS__.origin}),URL,Promise,Error,Number,ResizeObserver:class{observe(){}}});
 vm.runInContext(ts.transpileModule(source,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText,context);
 const frame={getAttribute:()=> 'http://dsh/#__dsh_work_doc=doc',contentWindow:{postMessage:m=>messages.push(m)},getBoundingClientRect:()=>({left:0,top:0,right:300,bottom:300,width:300,height:300}),clientWidth:300,clientHeight:300};
 const bridge=exports.embeddedBrowserBridge(frame);
 await bridge.retireDocument('doc');bridge.activateDocument('doc');
 bridge.handle({data:{version:1,type:'dsh-work/browser',id:'request',action:'update',document:'doc',slot:'s',address:'https://example.test',visible:true,rect:{x:0,y:0,width:100,height:100}}});
 await tick();
 assert.ok(messages.some(message=>message.type==='dsh-work/browser-result'&&message.value.visible));
 const settings=bridge.setOcclusion('settings',true);
 await tick();assert.deepEqual(occlusions,[{document:'doc',blocked:true}]);
 const menu=bridge.setOcclusion('shell-menu',true);
 const settingsClosed=bridge.setOcclusion('settings',false);
 cover.resolve([]);
 await Promise.all([settings,menu,settingsClosed]);
 assert.deepEqual(occlusions,[{document:'doc',blocked:true}]);
 // Releasing the previous last source and adding a new one in the same turn
 // must not send an intermediate native resume.
 await Promise.all([bridge.setOcclusion('shell-menu',false),bridge.setOcclusion('dsh-modal',true)]);
 assert.deepEqual(occlusions,[{document:'doc',blocked:true}]);
 await bridge.setOcclusion('dsh-modal',false);
 assert.deepEqual(occlusions,[{document:'doc',blocked:true},{document:'doc',blocked:false}]);
 const resumed=messages.filter(message=>message.type==='dsh-work/browser-blocked').at(-1);
 assert.equal(resumed.blocked,false);assert.equal(resumed.layoutRevision,1);
 const update=(id,layoutRevision,visible,width,address='')=>bridge.handle({data:{version:1,type:'dsh-work/browser',id,action:'update',document:'doc',slot:'s',address,visible,rect:{x:0,y:0,width,height:100},layoutRevision}});
 update('old-before',0,true,100);update('current',1,true,130);update('old-after',0,false,100);await tick();
 assert.deepEqual(updates.map(({visible,width})=>[visible,width]),[[true,100],[true,130]],'stale updates cannot show, hide, or reposition the slot');
 for(const id of ['old-before','old-after'])assert.equal(messages.find(message=>message.id===id)?.errorCode,'CEF_PRESENTATION_LAYOUT_STALE');
 update('navigation',0,true,170,'https://example.test/next');await tick();
 assert.deepEqual(updates.at(-1),{address:'https://example.test/next',x:0,width:130,visible:true},'late navigation uses the current rectangle');
 bridge.handle({data:{version:1,type:'dsh-work/browser',id:'open',action:'artifact-open',document:'doc',slot:'s'}});
 await tick();assert.equal(pickerStarted,true);
 await bridge.setOcclusion('settings',true);
 assert.deepEqual(occlusions.at(-1),{document:'doc',blocked:true},'a pending picker does not hold the presentation queue');
 update('covered-navigation',0,true,170,'https://example.test/covered');await tick();
 assert.deepEqual(updates.at(-1),{address:'https://example.test/covered',x:0,width:0,visible:false});
 assert.ok(bridge.overlaps({left:120,top:0,right:140,bottom:20}),'stale navigation cannot erase the latest covered slot');
 picker.resolve({});await tick();
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
 const calls=[];const bindings={BeginDocument:async()=>true,Availability:async()=>({state:'ready',code:'CEF_READY'}),Capability:async()=>true,SelectControl:async(...args)=>{calls.push(args);return{state:'waiting-for-turn'}}};
 const exports={};const context=vm.createContext({exports,require:()=>({t:key=>key,subscribeLocale:()=>()=>{},EmbeddedBrowserService:bindings,bridgeOrigin:globalThis.__DSH_WORK_ORIGINS__.origin}),URL,Promise,Error,Number,ResizeObserver:class{observe(){}}});
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
 const bindings={BeginDocument:async()=>true,Availability:async()=>({state:'ready',code:'CEF_READY'}),Capability:()=>++checks===1?capability.promise:Promise.resolve(true),Update:async(...args)=>{calls.push(args);return{visible:args[4]}}};
 const exports={};const context=vm.createContext({exports,require:()=>({t:key=>key,subscribeLocale:()=>()=>{},EmbeddedBrowserService:bindings,bridgeOrigin:globalThis.__DSH_WORK_ORIGINS__.origin}),URL,Promise,Error,Number,ResizeObserver:class{observe(){}}});
 vm.runInContext(ts.transpileModule(source,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText,context);
 const frame={getAttribute:()=> 'http://dsh/',contentWindow:{postMessage(){}},getBoundingClientRect:()=>({left:0,top:40,right:300,bottom:340,width:300,height:300}),clientWidth:300,clientHeight:300};
 const bridge=exports.embeddedBrowserBridge(frame,async()=>{});await bridge.retireDocument('document');bridge.activateDocument('document');
 const message={version:1,type:'dsh-work/browser',action:'update',document:'document',slot:'s',address:'',rect:{x:0,y:0,width:100,height:100}};
 bridge.handle({data:{...message,id:'show',visible:true}});await tick();
 bridge.handle({data:{...message,id:'hide',visible:false}});await tick();assert.equal(checks,1,'hide must not overtake the earlier pending capability');
 capability.resolve(true);await tick();await tick();
 assert.deepEqual(calls.map(call=>call[4]),[true,false],'the final native operation must remain Hide');
});

test('menu close ACK cannot resume the document after Settings has blocked it',async()=>{
 const source=readFileSync(new URL('../../frontend/src/embedded-browser.ts',import.meta.url),'utf8');
 const resumed=deferred(),messages=[],calls=[];
 const bindings={BeginDocument:async()=>true,Availability:async()=>({state:'ready',code:'CEF_READY'}),Capability:async()=>true,State:async()=>({}),SetOcclusion:async(_document,blocked)=>{calls.push(blocked);return blocked?[]:resumed.promise}};
 const exports={};const context=vm.createContext({exports,require:()=>({t:key=>key,subscribeLocale:()=>()=>{},EmbeddedBrowserService:bindings,bridgeOrigin:globalThis.__DSH_WORK_ORIGINS__.origin}),URL,Promise,Error,Number,ResizeObserver:class{observe(){}}});
 vm.runInContext(ts.transpileModule(source,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText,context);
 const frame={getAttribute:()=> 'http://dsh/',contentWindow:{postMessage:m=>messages.push(m)}};
 const bridge=exports.embeddedBrowserBridge(frame);await bridge.retireDocument('doc');bridge.activateDocument('doc');
 bridge.handle({data:{version:1,type:'dsh-work/browser',id:'state',action:'state',document:'doc',slot:'s'}});await tick();
 await bridge.setOcclusion('shell-menu',true);messages.length=0;
 const menuClosed=bridge.setOcclusion('shell-menu',false);await tick();
 const settingsOpened=bridge.setOcclusion('settings',true);
 resumed.resolve();await Promise.all([menuClosed,settingsOpened]);
 assert.equal(messages.at(-1).blocked,true,'Settings publishes the blocked state after the native Hide barrier');
 assert.deepEqual(calls,[true,false,true],'native operations retain intent order');
 assert.equal(messages.some(m=>m.type==='dsh-work/browser-blocked'&&!m.blocked),false,'an older resume ACK must not undo the latest block');
 await bridge.setOcclusion('settings',false);
 assert.equal(messages.at(-1).blocked,false,'closing Settings resumes the retained document');
 await bridge.setOcclusion('shell-menu',true);
 await bridge.setOcclusion('settings',true);
 const before=calls.length;
 await bridge.setOcclusion('shell-menu',false);
 assert.equal(calls.length,before,'closing one overlapping surface cannot release the other blocker');
 await bridge.setOcclusion('settings',false);
});

test('menu overlap follows the admitted slot and preview waits for native Cover',async()=>{
 const source=readFileSync(new URL('../../frontend/src/embedded-browser.ts',import.meta.url),'utf8');
 const covered=deferred(),calls=[],messages=[],updates=[],sequence=[];
 let hidden=false,updating;
 const bindings={BeginDocument:async()=>{hidden=false;return true},Availability:async()=>({state:'ready',code:'CEF_READY'}),Capability:async()=>true,Update:async(_document,_slot,_address,bounds,visible)=>{sequence.push('update');updates.push({bounds:{...bounds},visible});await updating?.promise;return{state:'ready',visible:visible&&!hidden}},SetOcclusion:async(_document,blocked)=>{calls.push(blocked?'cover':false);sequence.push(blocked?'cover':'resume');if(blocked)await covered.promise;hidden=blocked;return blocked?covered.promise:null}};
 const exports={};const context=vm.createContext({exports,require:()=>({t:key=>key,subscribeLocale:()=>()=>{},EmbeddedBrowserService:bindings,bridgeOrigin:globalThis.__DSH_WORK_ORIGINS__.origin}),URL,Promise,Error,Number,ResizeObserver:class{observe(){}}});
 vm.runInContext(ts.transpileModule(source,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText,context);
 const frame={hidden:false,getAttribute:()=> 'http://dsh/',contentWindow:{postMessage:m=>messages.push(m)},getBoundingClientRect:()=>({left:100,top:40,right:500,bottom:340,width:400,height:300}),clientWidth:200,clientHeight:150};
 const bridge=exports.embeddedBrowserBridge(frame);await bridge.retireDocument('doc');bridge.activateDocument('doc');
 const update=async(id,slot,rect,visible=true)=>{
  bridge.handle({data:{version:1,type:'dsh-work/browser',id,action:'update',document:'doc',slot,address:'',visible,rect}});await tick();
 };
 await update('clipped','session/clipped',{x:-10,y:-5,width:25,height:15});
 assert.deepEqual(updates.at(-1),{bounds:{X:100,Y:40,Width:30,Height:20},visible:true});
 assert.equal(bridge.overlaps({left:131,right:148,top:45,bottom:55}),false,'left clipping cannot extend the opposite edge');
 assert.equal(bridge.overlaps({left:103,right:110,top:61,bottom:69}),false,'top clipping cannot extend the opposite edge');
 await update('outside','session/clipped',{x:-40,y:10,width:30,height:10});
 assert.deepEqual(updates.at(-1),{bounds:{X:100,Y:60,Width:0,Height:20},visible:false});
 await update('show','session/tab',{x:25,y:5,width:50,height:40});
 assert.equal(bridge.overlaps({left:0,right:100,top:50,bottom:120}),false);
 assert.equal(bridge.overlaps({left:180,right:210,top:50,bottom:120}),true);
 await update('other','session/other',{x:150,y:5,width:50,height:40});
 await update('other-hide','session/other',{x:150,y:5,width:50,height:40},false);
 assert.equal(bridge.overlaps({left:180,right:210,top:50,bottom:120}),true,'another slot cannot erase the first visible slot');
 messages.length=0;
 const hiding=bridge.menuOverlaps({left:180,right:210,top:50,bottom:120});await tick();assert.deepEqual(calls,['cover']);assert.equal(messages.some(m=>m.type==='dsh-work/browser-blocked'),false);
 covered.resolve([{slot:'session/tab',data:'AAAA',lease:'v',document:'d',frame:'f',url:'https://example.test'}]);await hiding;
 assert.deepEqual(messages.filter(m=>m.type.startsWith('dsh-work/browser-')).slice(-2).map(m=>m.type),['dsh-work/browser-previews','dsh-work/browser-blocked']);
 bridge.handle({data:{version:1,type:'dsh-work/browser',id:'detach',action:'detach',document:'doc',slot:'session/tab'}});await tick();
 assert.equal(bridge.overlaps({left:180,right:210,top:50,bottom:120}),false,'retired slot cannot occlude a later menu');
 await bridge.retireDocument('next');assert.equal(bridge.overlaps({left:180,right:210,top:50,bottom:120}),false);
 await bridge.setOcclusion('shell-menu',false);assert.deepEqual(calls,['cover'],'document retirement already cleared the old native gate');
 bridge.activateDocument('next');
 const move=async(id,x)=>{
  bridge.handle({data:{version:1,type:'dsh-work/browser',id,action:'update',document:'next',slot:'session/tab',address:'',visible:true,rect:{x,y:5,width:20,height:40}}});
  await tick();
 };
 await move('clear',125);
 await bridge.menuOverlaps({left:180,right:210,top:50,bottom:120});
 sequence.length=0;
 await move('under-menu',40);
 assert.deepEqual(sequence,['cover','update'],'a layout moving under an open menu must wait for native occlusion before Update');
 assert.equal(messages.find(m=>m.id==='under-menu')?.value?.visible,false);
 await bridge.closeMenu();
 assert.equal(sequence.at(-1),'resume');
 updating=deferred();
 sequence.length=0;
 bridge.handle({data:{version:1,type:'dsh-work/browser',id:'moving',action:'update',document:'next',slot:'session/tab',address:'',visible:true,layoutRevision:1,rect:{x:40,y:5,width:20,height:40}}});
 await tick();
 let open=false;
 const opening=bridge.menuOverlaps({left:180,right:210,top:50,bottom:120}).then(()=>{open=true});
 await tick();
 assert.equal(open,false,'a menu must wait for an in-flight layout when previous visible bounds do not overlap');
 updating.resolve();
 await opening;
 assert.deepEqual(sequence,['update','cover']);
});

test('a blocker set before capability hides the first admitted update and retired Cover cannot publish an old preview',async()=>{
 const source=readFileSync(new URL('../../frontend/src/embedded-browser.ts',import.meta.url),'utf8');
 const cover=deferred(),calls=[],messages=[];
 const bindings={BeginDocument:async token=>{calls.push('begin:'+token);return true},Availability:async()=>({state:'ready',code:'CEF_READY'}),Capability:async()=>true,State:async()=>({state:'empty'}),Update:async(...args)=>{calls.push('update:'+args[4]);return{state:'ready',visible:args[4]}},SetOcclusion:async(document,blocked)=>{calls.push(blocked?'cover:'+document:'resume:'+document);return blocked?(document==='old'?cover.promise:[]):null}};
 const exports={};const context=vm.createContext({exports,require:()=>({t:key=>key,subscribeLocale:()=>()=>{},EmbeddedBrowserService:bindings,bridgeOrigin:globalThis.__DSH_WORK_ORIGINS__.origin}),URL,Promise,Error,Number,ResizeObserver:class{observe(){}}});
 vm.runInContext(ts.transpileModule(source,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText,context);
 const frame={hidden:false,getAttribute:()=> 'http://dsh/',contentWindow:{postMessage:m=>messages.push(m)},getBoundingClientRect:()=>({left:0,top:0,right:300,bottom:300,width:300,height:300}),clientWidth:300,clientHeight:300};
 const bridge=exports.embeddedBrowserBridge(frame);await bridge.retireDocument('old');bridge.activateDocument('old');
 await bridge.setOcclusion('settings',true);
 bridge.handle({data:{version:1,type:'dsh-work/browser',id:'show',action:'update',document:'old',slot:'s',address:'',visible:true,rect:{x:0,y:0,width:100,height:100}}});await tick();
 assert.deepEqual(calls,['begin:old','cover:old'],'first update waits behind the native occlusion barrier');
 const retired=bridge.retireDocument('new');cover.resolve([{slot:'s',data:'OLDPNG',lease:'old',document:'old',frame:'old',url:'https://example.test'}]);await retired;bridge.activateDocument('new');await tick();
 assert.equal(calls.includes('update:true'),false,'retired document cannot present after its pending Cover');
 assert.equal(messages.some(m=>m.type==='dsh-work/browser-previews'),false,'old image never reaches new document');
 await bridge.setOcclusion('dsh-modal',true);
 assert.equal(messages.some(m=>m.type==='dsh-work/browser-previews'),false,'an added new-document blocker cannot republish old Cover data');
 bridge.handle({data:{version:1,type:'dsh-work/browser',id:'state',action:'state',document:'new',slot:'s'}});await tick();
 assert.equal(messages.find(m=>m.type==='dsh-work/browser-blocked')?.blocked,true,'new document learns the retained Settings barrier');
 await bridge.setOcclusion('settings',false);
 await bridge.setOcclusion('dsh-modal',false);
 assert.deepEqual(calls,['begin:old','cover:old','begin:new','cover:new','resume:new']);
});

test('failed cover retains native-blocked uncertainty until resume acknowledges recovery',async()=>{
 const source=readFileSync(new URL('../../frontend/src/embedded-browser.ts',import.meta.url),'utf8');
 const calls=[];let resumeFails=true;
 const bindings={BeginDocument:async()=>true,Availability:async()=>({state:'ready',code:'CEF_READY'}),Capability:async()=>true,State:async()=>({state:'empty'}),SetOcclusion:async(_document,blocked)=>{if(blocked){calls.push('cover');throw Error('native Cover failed after gate blocked')}calls.push('resume');if(resumeFails){resumeFails=false;throw Error('native resume failed')}return null}};
 const exports={};const context=vm.createContext({exports,require:()=>({t:key=>key,subscribeLocale:()=>()=>{},EmbeddedBrowserService:bindings,bridgeOrigin:globalThis.__DSH_WORK_ORIGINS__.origin}),URL,Promise,Error,Number,ResizeObserver:class{observe(){}}});
 vm.runInContext(ts.transpileModule(source,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText,context);
 const frame={getAttribute:()=> 'http://dsh/',contentWindow:{postMessage(){}}};
 const bridge=exports.embeddedBrowserBridge(frame);await bridge.retireDocument('doc');bridge.activateDocument('doc');
 bridge.handle({data:{version:1,type:'dsh-work/browser',id:'state',action:'state',document:'doc',slot:'s'}});await tick();
 await assert.rejects(bridge.setOcclusion('settings',true),/native Cover failed/);
 await assert.rejects(bridge.setOcclusion('settings',false),/native resume failed/);
 assert.deepEqual(calls,['cover','resume'],'clearing the source must attempt a real native resume even after Cover failed');
 await bridge.setOcclusion('settings',false);
 assert.deepEqual(calls,['cover','resume','resume'],'a failed resume remains retryable without toggling the source');
});

test('Availability is checked once and returns fixed recovery codes without entering Capability',async()=>{
 const source=readFileSync(new URL('../../frontend/src/embedded-browser.ts',import.meta.url),'utf8');
 for(const [state,code,copy] of [['missing','CEF_INSTALL_MISSING','install'],['corrupt','CEF_INSTALL_CORRUPT','install'],['incompatible','CEF_INSTALL_INCOMPATIBLE','update'],['unsupported-runtime','CEF_DSH_RUNTIME_UNSUPPORTED','runtime'],['unsupported-platform','CEF_PLATFORM_UNSUPPORTED','platform']]){
  let checks=0,capabilities=0;const messages=[];
  const bindings={BeginDocument:async()=>true,Availability:async()=>{checks++;return{state,code}},Capability:async()=>{capabilities++;return true}};
  const exports={};const context=vm.createContext({exports,require:()=>({t:key=>key,subscribeLocale:()=>()=>{},EmbeddedBrowserService:bindings,bridgeOrigin:globalThis.__DSH_WORK_ORIGINS__.origin}),URL,Promise,Error,Number,ResizeObserver:class{observe(){}}});
  vm.runInContext(ts.transpileModule(source,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText,context);
  const frame={getAttribute:()=> 'http://dsh/',contentWindow:{postMessage:m=>messages.push(m)}};
  const bridge=exports.embeddedBrowserBridge(frame);await bridge.retireDocument('doc');bridge.activateDocument('doc');
  for(const id of ['one','two'])bridge.handle({data:{version:1,type:'dsh-work/browser',id,action:'state',document:'doc',slot:'s'}});
  await tick();await tick();
  assert.equal(checks,1,state+' should not repeat a slow availability check');assert.equal(capabilities,0);
  assert.equal(messages.filter(m=>m.type==='dsh-work/browser-result').length,2);
  assert.equal(messages.at(-1).errorCode,code);assert.equal(messages.at(-1).error,'browser.availability.'+copy);
 }
});

test('retiring the DSH document refreshes runtime Availability without polling geometry',async()=>{
 const source=readFileSync(new URL('../../frontend/src/embedded-browser.ts',import.meta.url),'utf8');
 let runtime='unsupported-runtime',checks=0,capabilities=0;const messages=[];
 const bindings={BeginDocument:async()=>true,Availability:async()=>{checks++;return runtime==='ready'?{state:'ready',code:'CEF_READY'}:{state:runtime,code:'CEF_DSH_RUNTIME_UNSUPPORTED'}},Capability:async()=>{capabilities++;return true},State:async()=>({state:'empty'})};
 const exports={};const context=vm.createContext({exports,require:()=>({t:key=>key,subscribeLocale:()=>()=>{},EmbeddedBrowserService:bindings,bridgeOrigin:globalThis.__DSH_WORK_ORIGINS__.origin}),URL,Promise,Error,Number,ResizeObserver:class{observe(){}}});
 vm.runInContext(ts.transpileModule(source,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText,context);
 const frame={getAttribute:()=> 'http://dsh/',contentWindow:{postMessage:m=>messages.push(m)}};
 const bridge=exports.embeddedBrowserBridge(frame);
 const request=async token=>{bridge.handle({data:{version:1,type:'dsh-work/browser',id:token,action:'state',document:token,slot:'s'}});await tick();await tick()};
 await bridge.retireDocument('old');bridge.activateDocument('old');await request('old');
 assert.equal(messages.at(-1).errorCode,'CEF_DSH_RUNTIME_UNSUPPORTED');assert.equal(checks,1);assert.equal(capabilities,0);
 runtime='ready';await bridge.retireDocument('new');bridge.activateDocument('new');await request('new');await request('new');
 assert.equal(messages.at(-1).error,'');assert.equal(checks,2,'one check for the new Run context');assert.equal(capabilities,1);
 runtime='unsupported-runtime';await bridge.retireDocument('third');bridge.activateDocument('third');await request('third');
 assert.equal(messages.at(-1).errorCode,'CEF_DSH_RUNTIME_UNSUPPORTED');assert.equal(checks,3);assert.equal(capabilities,1,'a prior ready result cannot qualify a later Run context');
});

test('provider shows availability and navigation failures with their recovery actions',async()=>{
 const requests=[],listeners=new Map();let id=0;
 const element={isConnected:true,style:{},getBoundingClientRect:()=>({left:0,top:0,right:200,bottom:100}),getClientRects:()=>[1],closest:()=>null,prepend(){}};
 const parent={postMessage:m=>requests.push(m)};
 const context=vm.createContext({console,URL,decodeURIComponent,location:{ancestorOrigins:['http://shell'],hash:'#__dsh_work_doc=doc'},parent,window:{},innerWidth:500,innerHeight:500,crypto:{randomUUID:()=>String(++id)},setTimeout:()=>++id,clearTimeout(){},setInterval:()=>++id,clearInterval(){},ResizeObserver:class{observe(){}disconnect(){}},IntersectionObserver:class{constructor(fn){this.fn=fn}observe(target){this.fn([{target,isIntersecting:true,intersectionRatio:1}])}disconnect(){}},MutationObserver:class{observe(){}},getComputedStyle:()=>({display:'block',visibility:'visible',position:'relative'}),document:{head:{append(){}},documentElement:{},createElement:()=>({style:{},setAttribute(){},remove(){}}),querySelectorAll:()=>[],getElementById:()=>element},addEventListener:(type,fn)=>listeners.set(type,[...(listeners.get(type)||[]),fn]),removeEventListener(){}});
 vm.runInContext(readFileSync(new URL('./embedded_browser.js',import.meta.url),'utf8'),context);
 const respond=(error,errorCode,value)=>{const request=requests.shift();for(const fn of listeners.get('message')||[])fn({source:parent,origin:'http://shell',data:{version:1,type:'dsh-work/browser-result',id:request.id,error,errorCode,value}})};
 for(const fn of listeners.get('DOMContentLoaded')||[])fn();await tick();respond();await tick();
 const typed=context.__DSH_WORK_BROWSER_PROVIDER__.createPage('s',{key:'typed',persist(){}});typed.presentation.mount('viewport');await tick();respond('Reinstall DSH Work.','CEF_INSTALL_MISSING');await tick();
 assert.equal(typed.frame.getSnapshot().error.description,'Reinstall DSH Work.');
 const generic=context.__DSH_WORK_BROWSER_PROVIDER__.createPage('s',{key:'generic',persist(){}});generic.presentation.mount('viewport');await tick();respond('unexpected failure');await tick();
 assert.equal(generic.frame.getSnapshot().error.description,'The page is unavailable. Reload to continue.');
 const persisted=[],url='https://fixture.test/';
 const navigation=context.__DSH_WORK_BROWSER_PROVIDER__.createPage('s',{key:'navigation',persist:value=>persisted.push(value)});
 navigation.presentation.mount('viewport');await tick();respond(undefined,undefined,{state:'empty'});await tick();
 navigation.frame.loadUrl({url});await tick();
 respond(undefined,undefined,{state:'navigationFailed',failure:'CEF_NAVIGATION_FAILED',url,lease:'owned'});await tick();
 assert.equal(navigation.frame.getSnapshot().loading,false);
 assert.equal(navigation.frame.getSnapshot().error.description,'Reload to continue.');
 assert.equal(persisted.length,0);
 navigation.frame.reload();await tick();
 assert.equal(requests[0]?.action,'action');assert.equal(requests[0]?.command,'reload');
 respond();await tick();
 assert.equal(requests[0]?.action,'state');
 respond(undefined,undefined,{state:'ready',url,lease:'owned',document:'2',frame:'current',visible:true});await tick();
 assert.equal(navigation.frame.getSnapshot().error,undefined);
 assert.equal(persisted.length,1);
});

test('a hidden page drops an old preview when its native document changes',async()=>{
 const requests=[],listeners=new Map(),layers=[];let id=0,poll;
 let trayHeight=128;
 const el=tag=>({style:{},get offsetHeight(){return tag==='section'?trayHeight:0},setAttribute(){},removeAttribute(){},remove(){this.removed=true},append(){}});
 const container={isConnected:true,style:{},append(){},prepend:layer=>layers.push(layer),getBoundingClientRect:()=>({left:0,top:0,right:200,bottom:100}),getClientRects:()=>[1],closest:()=>null};
 const parent={postMessage:m=>requests.push(m)};
 const context=vm.createContext({console,URL,decodeURIComponent,location:{ancestorOrigins:['http://shell'],hash:'#__dsh_work_doc=doc'},parent,window:{},innerWidth:500,innerHeight:500,crypto:{randomUUID:()=>String(++id)},setTimeout:()=>++id,clearTimeout(){},setInterval:fn=>{poll=fn;return ++id},clearInterval(){},ResizeObserver:class{observe(){}disconnect(){}},IntersectionObserver:class{constructor(fn){this.fn=fn}observe(target){this.fn([{target,isIntersecting:true,intersectionRatio:1}])}disconnect(){}},MutationObserver:class{observe(){}},getComputedStyle:()=>({display:'block',visibility:'visible',position:'relative'}),document:{head:{append(){}},documentElement:{},createElement:el,querySelectorAll:()=>[],getElementById:()=>container},addEventListener:(type,fn)=>listeners.set(type,[...(listeners.get(type)||[]),fn]),removeEventListener(){}});
 vm.runInContext(readFileSync(new URL('./embedded_browser.js',import.meta.url),'utf8'),context);
 const emit=data=>{for(const fn of listeners.get('message')||[])fn({source:parent,origin:'http://shell',data:{version:1,...data}})};
 const respond=value=>emit({type:'dsh-work/browser-result',id:requests.shift().id,value});
 for(const fn of listeners.get('DOMContentLoaded')||[])fn();await tick();respond(true);await tick();
 const page=context.__DSH_WORK_BROWSER_PROVIDER__.createPage('s',{key:'tab',persist(){}});page.presentation.mount('viewport');await tick();respond({state:'empty'});await tick();
 page.frame.loadUrl({url:'https://example.test'});await tick();respond({state:'ready',url:'https://example.test',lease:'v',document:'1',frame:'f',visible:true});await tick();
 emit({type:'dsh-work/browser-previews',document:'doc',value:[{slot:'s/tab',data:'AAAA',lease:'v',document:'1',frame:'f',url:'https://example.test'}]});
 assert.match(layers.at(-1).style.backgroundImage,/data:image\/png;base64,AAAA/);
 emit({type:'dsh-work/browser-blocked',document:'doc',blocked:true});await tick();
 assert.equal(requests.length,0,'the covering acknowledgement does not issue a second native hide');
 poll();await tick();assert.equal(requests.at(0)?.action,'state');
 respond({state:'ready',url:'https://example.test/next',lease:'v',document:'2',frame:'next',visible:false,downloads:[{id:'d',state:'complete',filename:'file',received:0,total:0}]});await tick();
 assert.equal(layers.at(-2).removed,true);assert.equal(layers.at(-1).textContent,'页面暂时隐藏');assert.equal(layers.at(-1).style.bottom,'128px','preview stops above the actual download tray');
 assert.equal(requests.at(0)?.action,'update');trayHeight=64;respond({state:'ready',url:'https://example.test/next',lease:'v',document:'2',frame:'next',visible:false,downloads:[{id:'d',state:'complete',filename:'file',received:0,total:0}]});await tick();
 assert.equal(layers.at(-1).style.bottom,'64px','preview follows a resized download tray');
 assert.equal(requests.at(0)?.action,'update');respond({state:'ready',url:'https://example.test/next',lease:'v',document:'2',frame:'next',visible:false,downloads:[]});await tick();
 assert.equal(layers.at(-1).style.bottom,'0px','removing the download tray restores the full viewport');
});

test('artifact copy uses the trusted Host locale and updates existing documents',async()=>{
 const source=readFileSync(new URL('../../frontend/src/embedded-browser.ts',import.meta.url),'utf8');
 let locale='en',changed;const messages=[];
 const bindings={BeginDocument:async()=>true,Capability:async()=>{throw Error("CEF unavailable")}};
 const exports={};const context=vm.createContext({exports,require:()=>({EmbeddedBrowserService:bindings,bridgeOrigin:globalThis.__DSH_WORK_ORIGINS__.origin,t:key=>locale+':'+key,subscribeLocale:fn=>{changed=fn;return()=>{}}}),URL,Promise,Error,Number,ResizeObserver:class{observe(){}}});
 vm.runInContext(ts.transpileModule(source,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText,context);
 const frame={getAttribute:()=> 'http://dsh/',contentWindow:{postMessage:m=>messages.push(m)}};
 const bridge=exports.embeddedBrowserBridge(frame,async()=>{});await bridge.retireDocument('doc');bridge.activateDocument('doc');
 bridge.handle({data:{version:1,type:'dsh-work/browser',id:'labels',action:'artifact-labels',document:'doc',slot:'artifact',locale:'zh-CN',title:'untrusted title'}});await tick();
 assert.equal(messages.at(-1).value.choose,'en:browser.artifact.choose');
 locale='ja-JP';changed();
 assert.equal(messages.at(-1).type,'dsh-work/browser-artifact-labels');
 assert.equal(messages.at(-1).document,'doc');
 assert.equal(messages.at(-1).value.scope,'ja-JP:browser.artifact.scope');
});
