import assert from 'node:assert/strict';
import {randomUUID} from 'node:crypto';
import {basename} from 'node:path';
import {sleep,waitFor} from '../lib/context.mjs';
const view=(s,name)=>s.views.find(v=>v.name===name);
const doc=async(a,name='A')=>(await a.docs())[name];
const intact=(a,b)=>a.instance===b.instance&&a.value===b.value&&a.result===b.result;
const fixture=(a,name='A',host='127.0.0.1')=>`http://${host}:19478/interactions?tab=${name}`;
async function input(c,text){return c.action('browser_type','#entry',{text})}
async function picture(a,id,which){const path=await a.capture(id,which);return {artifact:basename(path),markers:a.visualMarkers(path)}}
async function policyState(a){return a.call('backend','/native-policy/state',{})}
async function policyRequest(c,selector,kind){
 const a=c.adapter,before=new Set((await policyState(a)).requests.map(p=>p.id));
 await c.action('browser_click',selector);
 return waitFor(async()=>{const s=await policyState(a);return s.requests.find(p=>!before.has(p.id)&&p.kind===kind)},'native '+kind+' request');
}
async function policyChoice(a,p,choice){
 await a.main.locator('[data-native-policy-id="'+p.id+'"] [data-choice="'+choice+'"]').click();
}
async function popup(c,selector){
 const a=c.adapter,initial=await a.state(),before=await doc(a),denied=await policyRequest(c,selector,'popup');
 await policyChoice(a,denied,'deny');await sleep(200);assert(intact(before,await doc(a)));
 const expired=await policyRequest(c,selector,'popup');await a.command('tab',{tab:'B'});await a.command('tab',{tab:'A'});
 await waitFor(async()=>(await policyState(a)).requests.find(p=>p.id===expired.id)?.status==='expired','selection expires popup');
 const stale=await a.api('backend','/native-policy/answer',{id:expired.id,choice:'allow'});assert(!stale.ok);
 const allowed=await policyRequest(c,selector,'popup');await policyChoice(a,allowed,'allow');
 await waitFor(async()=>(await a.state()).selected==='B'&&(await policyState(a)).requests.find(p=>p.id===allowed.id)?.status==='opened','managed popup destination');
 const documents=await a.docs(),state=await a.state();assert(intact(before,documents.A));assert(documents.B.url.includes('/popup'));
 assert.equal(state.host.id,initial.host.id);assert.equal(state.views.length,2);
 for(const name of ['A','B'])assert.equal(view(state,name).id,view(initial,name).id);
 const isolated=await a.call('backend','/policy-popup-state',{});assert.equal(isolated.targetCount,2);assert(isolated.openerNull&&isolated.wails==='undefined');
 return {evidence:{denied,expired,stale,allowed,documents,isolated,picture:await picture(a,c.caseId)}};
}
async function download(c,selector,name,cancel=false) {
 const a=c.adapter,start=await a.call('backend','/batch-start',{kind:'download'});
 const req=await c.permit(await c.request('browser_click',selector));const action=await a.api('gateway','/execute',req);
 const observed=await a.call('backend','/batch-finish',{id:start.id,name,cancel});
 assert(action.ok,'Download click failed');return {action,...observed};
}

export const nativeCases={
 async 'native.composition'(c){const a=c.adapter,s=await a.state();assert(s.ready&&!s.covered&&view(s,'A').visible);
 const raw=await a.call('control','/state');let liveFrame;
 if(raw.rendering==='cef-osr-shared-frame'){
  assert.equal(raw.cefImagesInHost,0);assert.equal(raw.cefInHost,false);assert.notEqual(raw.hostPID,raw.guestPID);
  const before=raw.tabs.find(t=>t.name==='A').presentedSequence;
  await input(c,'live-frame');await c.action('browser_click','#apply');
  liveFrame=await waitFor(async()=>{const r=await a.call('control','/state');return r.tabs.find(t=>t.name==='A').presentedSequence>before?r:false},'live OSR frame after real page mutation');
 }
 const p=await picture(a,c.caseId);assert(p.markers.shellPixels>=50&&p.markers.guestPixels>=50,'Native capture must contain both shell and live guest landmarks');return {evidence:{...p,host:s.host,view:view(s,'A'),liveFrame}}},
 async 'sandbox.renderer'(c){const evidence=await c.adapter.sandboxAssessment();assert(evidence.rendererSandboxed);return {evidence}},
 async 'content.html'(c){const a=c.adapter;await a.call('backend','/display',{tab:'A',url:a.session.endpoints.artifact+'/'});const before=await doc(a);await input(c,'HTML-'+c.options.runId);await c.action('browser_click','#apply');const after=await doc(a),assets=await a.call('backend','/artifact-state');assert.equal(after.instance,before.instance);assert.equal(after.result,'HTML-'+c.options.runId);assert(assets.imageLoaded&&assets.js.includes('已加载')&&assets.css==='rgb(18, 62, 80)');return {evidence:{assets,before,after,picture:await picture(a,c.caseId)}}},
 async 'content.localhost'(c){const a=c.adapter;await a.call('backend','/display',{tab:'A',url:fixture(a,'A','localhost')});await input(c,'localhost');await c.action('browser_click','#apply');const d=await doc(a);assert.equal(d.result,'localhost');return {evidence:d}},
 async 'content.https'(c){const a=c.adapter,display=await a.call('backend','/display',{tab:'A',url:'https://example.com/'});assert.equal(display.status,200);assert(display.security?.protocol);const actual=await a.call('backend','/page-text');assert(actual.text.length>0);const result=await c.action('browser_snapshot','body');assert.equal(result.value.text,actual.text);const invalid=await a.api('backend','/display',{tab:'B',url:'https://expired.badssl.com/'});assert(!invalid.ok&&/CERT_/.test(JSON.stringify(invalid.value)));return {evidence:{display,readMatchesActualDocument:true,invalid,picture:await picture(a,c.caseId)}}},
 async 'tabs.retention'(c){const a=c.adapter;await input(c,'keep-A');const beforeA=await doc(a);await a.command('tab',{tab:'B'});await input(c,'keep-B');const beforeB=await doc(a,'B'),initial=await a.state();await sleep(1200);await a.command('tab',{tab:'A'});const final=await a.state();assert(intact(beforeA,await doc(a))&&intact(beforeB,await doc(a,'B')));for(const n of ['A','B'])assert.equal(view(initial,n).id,view(final,n).id);assert(view(final,'A').visible&&!view(final,'B').visible);return {evidence:{beforeA,beforeB,views:final.views}}},
 async 'views.float-dock'(c){const a=c.adapter;await input(c,'retained');const before=await doc(a),initial=await a.state();await a.command('float');const floating=await a.state();assert.equal(view(floating,'A').id,view(initial,'A').id);assert.equal(view(floating,'A').parentId,floating.floatingHost.id);const shot=await picture(a,c.caseId,'float');await a.command('dock');const final=await a.state();assert.equal(view(final,'A').parentId,final.host.id);assert(intact(before,await doc(a)));return {evidence:{initial,floating,final,picture:shot}}},
 async 'dpi.current'(c){const s=await c.adapter.state(),d=await doc(c.adapter);assert.equal(s.host.dpi,view(s,'A').dpi);assert.equal(d.dpr,view(s,'A').dpi/96);return {evidence:{hostDpi:s.host.dpi,guestDpi:view(s,'A').dpi,dpr:d.dpr,monitorCount:s.monitors.length,scope:'current monitor only'}}},
 async 'views.resize'(c){const a=c.adapter;await input(c,'resize-retained');const before=await doc(a),initial=await a.state();try{await a.command('bounds',{window:'main',x:20,y:20,width:900,height:650});const after=await waitFor(async()=>{const d=await doc(a);return d.viewport.width!==before.viewport.width&&d.viewport.height!==before.viewport.height?d:false},'native resize propagates to guest');assert(intact(before,after));assert.equal(view(await a.state(),'A').id,view(initial,'A').id);return {evidence:{before,after,picture:await picture(a,c.caseId)}}}finally{await a.command('bounds',{window:'main',x:20,y:20,width:1150,height:800})}},
 async 'input.native-routing'(c){const a=c.adapter,results=[];await a.call('backend','/manual-reset',{});for(const tab of ['A','B']){await a.command('tab',{tab});if(tab==='B')await a.command('float');await c.action('browser_click','#entry');const route=await a.nativeInput('guest');const d=await waitFor(async()=>{const d=await doc(a,tab);return d.value==='r'?d:false},'native ASCII reaches selected '+tab).catch(async e=>{e.message+='; fixture='+JSON.stringify({documents:await a.docs(),events:await a.call('backend','/manual-events')});throw e});assert.equal(d.activeElement,'entry');const events=(await a.call('backend','/manual-events'))[tab].events;assert(events.some(e=>e.type==='input'&&e.trusted));results.push({tab,route,document:d,events})}await a.command('dock');await a.main.evaluate(()=>{const el=document.querySelector('#shell-input');el.value='';el.focus()});const route=await a.nativeInput('host');await a.main.waitForFunction(()=>document.querySelector('#shell-input').value==='r');assert.equal((await doc(a,'A')).value,'r');assert.equal((await doc(a,'B')).value,'r');return {evidence:{results,hostRoute:route,scope:'Scoped synthetic native ASCII event and native focus; physical keyboard and IME remain manual'}}},
 async 'authorization.display-only'(c){const before=await doc(c.adapter),req=await c.request('browser_type','#entry',{text:'UNAUTHORIZED'}),denied=await c.denied(req);assert(intact(before,await doc(c.adapter)));return {evidence:{denied,before}}},
 async 'authorization.exact-once'(c){const a=c.adapter,req=await c.permit(await c.request('browser_type','#entry',{text:'one'}));await c.execute(req);const replay=await c.denied(req);const exact=await c.permit(await c.request('browser_type','#entry',{text:'original'}));const changed={...exact,arguments:{...exact.arguments,text:'tampered'}};const altered=await c.denied(changed),consumed=await c.denied(exact);assert.equal((await doc(a)).value,'one');return {evidence:{replay,altered,consumed}}},
 async 'authorization.secret'(c){const req=await c.request('browser_type','#password',{text:'fixture-only'});const r=await c.adapter.api('gateway','/authorize',req);assert(!r.ok&&JSON.stringify(r.value).includes('SECRET_INPUT_BLOCKED'));return {evidence:r}},
 async 'identity.navigation'(c){const a=c.adapter,r=await c.permit(await c.request('browser_type','#entry',{text:'OLD'})),before=await doc(a);await a.call('backend','/display',{tab:'A',url:fixture(a)});const refused=await c.denied(r),after=await doc(a);assert.notEqual(before.instance,after.instance);assert.equal(after.value,'');return {evidence:{before,after,refused}}},
 async 'identity.tab-aba'(c){const a=c.adapter,r=await c.permit(await c.request('browser_type','#entry',{text:'OLD'})),before=await doc(a);await a.command('tab',{tab:'B'});await a.command('tab',{tab:'A'});const refused=await c.denied(r);assert(intact(before,await doc(a)));return {evidence:{refused}}},
 async 'authorization.revoke'(c){const a=c.adapter,r=await c.permit(await c.request('browser_type','#entry',{text:'REVOKED',_labHoldMs:2000})),before=await doc(a);const held=await c.hold(r);await c.revoke();const result=await held.operation;assert(!result.ok);await sleep(2100);assert(intact(before,await doc(a)));assert.equal((await a.stats()).dispatches,held.before.dispatches);return {evidence:{result,before,after:await doc(a)}}},
 async 'authorization.generation'(c){const a=c.adapter,r=await c.permit(await c.request('browser_type','#entry',{text:'OLD_GENERATION'})),before=await doc(a);await a.call('gateway','/generation',{generation:'test-'+randomUUID()});const refused=await c.denied(r);assert(intact(before,await doc(a)));return {evidence:{refused}}},
 async 'overlay.presentation'(c){const a=c.adapter;await input(c,'retained');const before=await doc(a),r=await c.permit(await c.request('browser_type','#entry',{text:'HIDDEN'})),restored=await c.permit(await c.request('browser_type','#entry',{text:'STALE'})),read=await c.permit(await c.request('browser_snapshot','body'));const cover=await c.cover(c.caseId);assert(cover.state.covered&&cover.state.views.every(v=>!v.visible));const hiddenWrite=await c.denied(r),hiddenRead=await c.denied(read);await c.uncover();const stale=await c.denied(restored);assert(intact(before,await doc(a)));await input(c,'fresh');assert.equal((await doc(a)).value,'fresh');return {evidence:{hiddenWrite,hiddenRead,stale,artifact:basename(cover.path)}}},
 async 'overlay.queued'(c){const a=c.adapter,before=await doc(a),r=await c.permit(await c.request('browser_type','#entry',{text:'HIDDEN',_labHoldMs:2000})),held=await c.hold(r);await c.cover(c.caseId);const result=await held.operation;assert(!result.ok&&JSON.stringify(result.value).includes('NOT_INTERACTABLE'));assert.equal((await a.stats()).dispatches,held.before.dispatches);await c.uncover();assert(intact(before,await doc(a)));return {evidence:{result}}},
 async 'recovery.renderer'(c){const a=c.adapter;await input(c,'A-before');await a.command('tab',{tab:'B'});await input(c,'B-survives');await a.command('tab',{tab:'A'});const before=await a.docs(),state=await a.state(),r=await c.permit(await c.request('browser_type','#entry',{text:'CRASHED',_labHoldMs:10000})),held=await c.hold(r);const crash=await a.call('backend','/crash-renderer',{tab:'A'});assert(!await held.operation.then(r=>r.ok));assert(crash.tabs.find(t=>t.name==='A').crashed&&!crash.tabs.find(t=>t.name==='B').crashed);assert(intact(before.B,await doc(a,'B')));await a.main.locator('#recover-renderer').waitFor({state:'visible'});await a.main.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));const failed=await picture(a,c.caseId+'-failed');await a.recover('renderer');assert.equal((await a.state()).host.id,state.host.id);assert.notEqual((await doc(a)).instance,before.A.instance);const old=await c.denied(r);await c.begin();await input(c,'A-recovered');return {evidence:{failed,beforeB:before.B,afterB:await doc(a,'B'),old,picture:await picture(a,c.caseId+'-recovered')}}},
 async 'recovery.broker'(c){const a=c.adapter;await input(c,'old');const before=await a.state(),r=await c.permit(await c.request('browser_type','#entry',{text:'CRASHED',_labHoldMs:10000})),held=await c.hold(r),crash=await a.call('backend','/crash-broker',{});assert(!crash.connected&&!crash.native.guestRunning);assert(!await held.operation.then(r=>r.ok));await a.main.locator('#recover-broker').waitFor({state:'visible'});await a.main.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));const failed=await picture(a,c.caseId+'-failed');await a.recover('broker');const after=await a.state();assert.equal(after.host.id,before.host.id);assert.notEqual(after.viewGeneration,before.viewGeneration);const old=await c.denied(r);await c.begin();await input(c,'new-broker');return {evidence:{before,after,old,failed,picture:await picture(a,c.caseId+'-recovered')}}},
 async 'isolation.guest'(c){const a=c.adapter,g=await a.call('backend','/guest-isolation');assert(g.parentSelf&&g.openerNull&&g.wails==='undefined'&&g.webview==='undefined'&&g.hostObjects==='undefined');const denied={};for(const [role,path,body] of [['control','/command?action=quit',{}],['gateway','/state',undefined],['backend','/health',undefined]]){const r=await a.api(role,path,body,{authenticate:false});assert([401,403].includes(r.status));denied[role]=r.status;}for(const role of ['gateway','backend']){const r=await a.api(role,role==='gateway'?'/state':'/health',undefined,{origin:a.session.endpoints.fixture});assert.equal(r.status,403)}return {evidence:{guest:g,unauthenticated:denied}}},
 async 'isolation.artifact-root'(c){const a=c.adapter,result=[];for(const path of ['/escape/outside.txt','/%2e%2e/private/outside.txt']){const r=await fetch(a.session.endpoints.artifact+path),body=await r.text();assert(r.status>=400&&!body.includes('BROWSER_LAB_PRIVATE_SENTINEL'));result.push({path,status:r.status})}const file=await a.api('backend','/display',{tab:'A',url:'file:///browser-lab-no-file'});assert(!file.ok);return {evidence:{result,file,scope:'root escape/junction and disallowed file scheme'}}},
 async 'profile.same-origin'(c){const a=c.adapter,key='browser_lab_'+randomUUID().replaceAll('-',''),guest='guest-'+randomUUID(),host='host-'+randomUUID(),original=a.float.url();try{const first=await a.call('backend','/storage-set',{key,value:guest}),peer=await a.call('backend','/storage-get',{key,tab:'B'});assert.equal(peer.localStorage,guest);await a.float.goto(fixture(a,'host'),{waitUntil:'domcontentloaded'});const before=await a.float.evaluate(key=>({storage:localStorage.getItem(key),cookie:document.cookie}),key);assert.equal(before.storage,null);assert(!before.cookie.includes(guest));await a.float.evaluate(({key,value})=>{localStorage.setItem(key,value);document.cookie=key+'='+value+'; Path=/; Max-Age=3600'},{key,value:host});const after=await a.call('backend','/storage-get',{key});assert.equal(after.localStorage,guest);assert(!after.cookie.includes(host));return {evidence:{first,peer,hostBefore:before,guestAfter:after}}}finally{await a.float.goto(original,{waitUntil:'domcontentloaded'})}},
 async 'profile.persistence'(c){const a=c.adapter,key='browser_lab_'+randomUUID().replaceAll('-',''),value='persist-'+randomUUID();await a.call('backend','/storage-set',{key,value});const before=await a.state();await a.call('backend','/profile-close',{});await a.recover('broker');const after=await a.state(),state=await a.call('backend','/storage-get',{key});assert.equal(state.localStorage,value);assert(state.cookie.includes(value));assert.equal(before.host.id,after.host.id);assert.notEqual(before.viewGeneration,after.viewGeneration);return {evidence:{state,beforeGeneration:before.viewGeneration,afterGeneration:after.viewGeneration}}},
 'popup.window-open':c=>popup(c,'#open-window'),
 'popup.target-blank':c=>popup(c,'#open-tab'),
 async 'download.attached-default'(c){
 const a=c.adapter,before=await policyState(a),p=await policyRequest(c,'#download-link','download');
 await sleep(500);assert.deepEqual((await policyState(a)).files,before.files);await policyChoice(a,p,'deny');
 return {evidence:{request:p,before,after:await policyState(a),scope:'actual attachment with DevTools default; no write before destination consent'}};
 },
 async 'download.native-default'(c){
 const a=c.adapter;await a.call('backend','/download-mode',{mode:'default'});
 try{const results=[];for(const selector of ['#download-link','#download-traversal','#download-cancel']){
 const before=await policyState(a),p=await policyRequest(c,selector,'download');await policyChoice(a,p,'deny');await sleep(100);
 assert.deepEqual((await policyState(a)).files,before.files);results.push(p)}return {evidence:{results}}}
 finally{await a.call('backend','/download-mode',{mode:'deny'})}
 },
 async 'permissions.defaults'(c){const evidence=await c.adapter.call('backend','/permissions',{});assert(!Object.values(evidence.queried).includes('granted'));return {evidence}},
 async 'permissions.lifecycle'(c){
 const a=c.adapter,old=await policyRequest(c,'#request-notification','notifications');
 await a.call('backend','/display',{tab:'A',url:fixture(a)});
 const stale=await a.api('backend','/native-policy/answer',{id:old.id,choice:'allow'});assert(!stale.ok);
 const p=await policyRequest(c,'#request-notification','notifications');await policyChoice(a,p,'allow');
 const granted=await waitFor(async()=>{const r=await a.call('backend','/permission-result');return r.state==='granted'&&r.request?.settled?r:false},'native notification allow');
 await policyChoice(a,p,'revoke');
 const revoked=await waitFor(async()=>{const r=await a.call('backend','/permission-result');return r.state==='denied'?r:false},'native notification revoke');
 await a.call('backend','/display',{tab:'A',url:fixture(a,'A','localhost')});
 const deny=await policyRequest(c,'#request-notification','notifications');await policyChoice(a,deny,'deny');
 const denied=await waitFor(async()=>{const r=await a.call('backend','/permission-result');return r.request?.settled&&r.state!=='granted'?r:false},'native notification deny');
 return {evidence:{stale,granted,revoked,denied,scope:'real Notification prompt and CEF content-setting revoke; no device access'}};
 },
 async 'download.authorization'(c){
 const a=c.adapter,p=await policyRequest(c,'#download-traversal','download');
 const invalid=await a.api('backend','/native-policy/answer',{id:p.id,choice:'allow',filename:'../outside.txt'});assert(!invalid.ok);
 await a.main.evaluate(id=>{document.querySelector('[data-native-policy-id="'+id+'"] input').value='approved-fixture.txt'},p.id);
 await policyChoice(a,p,'allow');
 const complete=await waitFor(async()=>{const r=(await policyState(a)).requests.find(r=>r.id===p.id);return r.result?r:false},'authorized native download');
 assert.equal(complete.result.state,'complete');assert.equal(complete.result.filename,'approved-fixture.txt');assert(complete.result.fixtureContent.startsWith('BROWSER_LAB isolated download'));assert.equal(complete.result.autoOpened,false);
 const slow=await policyRequest(c,'#download-cancel','download');await policyChoice(a,slow,'allow');
 await waitFor(async()=>(await policyState(a)).files.some(f=>f.name===slow.filename+'.partial'),'native streaming download');
 await policyChoice(a,slow,'revoke');
 const canceled=await waitFor(async()=>{const r=(await policyState(a)).requests.find(r=>r.id===slow.id);return r.result?r:false},'native active download revoked');
 assert.equal(canceled.result.state,'canceled');const final=await policyState(a);assert(!final.files.some(f=>f.name===slow.filename||f.name===slow.filename+'.partial'));
 const stale=await policyRequest(c,'#download-link','download');await a.call('backend','/display',{tab:'A',url:fixture(a)});
 const expired=await a.api('backend','/native-policy/answer',{id:stale.id,choice:'allow'});assert(!expired.ok);
 return {evidence:{invalid,complete,canceled,expired,files:final.files}};
 },
};
