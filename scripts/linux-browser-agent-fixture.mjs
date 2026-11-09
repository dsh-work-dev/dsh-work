// One local, deterministic model turn through the real DSH composer and tools.
// This is a fixture consumer of existing product RPCs, not an execution server.
import http from 'node:http';
import {randomUUID} from 'node:crypto';
import {join} from 'node:path';
import {existsSync} from 'node:fs';
import {isDeepStrictEqual} from 'node:util';

export async function runLinuxBrowserAgent({inspector,shell,dsh,visible,fixture,out,save,call,click}) {
 const wait=async(label,fn,ms=30000)=>{const deadline=Date.now()+ms;while(Date.now()<deadline){const value=await fn();if(value)return value;await new Promise(r=>setTimeout(r,100))}throw Error(label+': bounded deadline')};
 const origin=new URL(fixture).origin,session=visible.slot.slice(0,visible.slot.lastIndexOf('/'));
 const model='e67-linux-snapshot',route='e67-linux-local',credential='E67_LINUX_LOCAL_API_KEY';
 const prompt=`E67 local Linux validation: call my_browser_snapshot exactly once for the selected ${fixture} page. Do not type, submit, navigate or call another tool. Then finish.`;
 const callId='e67_snapshot_'+randomUUID();
 let requests=0,emitted=0,finished=false,toolResult,rejected,port;
 const events=[];
 const stream=(res,delta,finish)=>{
  res.writeHead(200,{'Content-Type':'text/event-stream','Cache-Control':'no-store','Connection':'close'});
  for(const [d,f] of [[{role:'assistant'},null],[delta,null],[{},finish]])res.write('data: '+JSON.stringify({id:'chatcmpl-'+callId,object:'chat.completion.chunk',created:Math.floor(Date.now()/1000),model,choices:[{index:0,delta:d,finish_reason:f}]})+'\n\n');
  res.end('data: [DONE]\n\n');
 };
 const server=http.createServer(async(req,res)=>{
  try {
   if(req.method!=='POST'||req.url!=='/v1/chat/completions'||req.headers.host!==`127.0.0.1:${port}`||req.headers.authorization!=='Bearer e67-local-fixture-only')throw Error('Unexpected model endpoint or identity');
   if(++requests>6)throw Error('Local model request budget exceeded');
   let size=0;const chunks=[];for await(const b of req){size+=b.length;if(size>8*1024*1024)throw Error('Model body limit');chunks.push(b)}
   const value=JSON.parse(Buffer.concat(chunks).toString());
   if(value.model!==model||value.stream!==true||!Array.isArray(value.messages))throw Error('Unknown model request');
   const serialized=JSON.stringify(value.messages);
   if(serialized.includes('Create a concise title for an AI coding-assistant session')){events.push('local-title');stream(res,{content:'E67 local Linux snapshot'},'stop');return}
   if(!serialized.includes(prompt))throw Error('Not the dedicated test prompt');
   const result=value.messages.find(m=>m.role==='tool'&&m.tool_call_id===callId);
   if(result){toolResult=result.content;if(!JSON.stringify(toolResult).includes('Real DSH Linux CEF slot')||!JSON.stringify(toolResult).includes('linux-dsh-original'))throw Error('Actual tool result did not contain the own fixture');finished=true;events.push('actual-tool-result');stream(res,{content:'Local Linux snapshot finished.'},'stop');return}
   if(emitted!==0||!value.tools?.some(t=>t.function?.name==='my_browser_snapshot'))throw Error('Snapshot unavailable or duplicate call');
   emitted++;events.push('snapshot-tool-call');stream(res,{tool_calls:[{index:0,id:callId,type:'function',function:{name:'my_browser_snapshot',arguments:'{}'}}]},'tool_calls');
  }catch(error){rejected=String(error);events.push('rejected');res.writeHead(400,{'Content-Type':'application/json'});res.end(JSON.stringify({error:{message:'Local fixture request refused'}}))}
 });
 server.requestTimeout=15000;server.headersTimeout=5000;
 await new Promise((resolve,reject)=>{server.once('error',reject);server.listen(0,'127.0.0.1',resolve)});port=server.address().port;
 const rpc=(endpoint,args)=>inspector.evaluate(dsh,`(async()=>{const rpcId=crypto.randomUUID(),endpoint=${JSON.stringify(endpoint)};const response=await fetch('/api/'+endpoint,{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({type:'client-request',rpcId,method:endpoint,payload:{args:${JSON.stringify(args)}}})});if(!response.ok)throw Error('Official RPC HTTP '+response.status);const full=await response.json();if(full.rpcId!==rpcId||!full.result?.ok)throw Error(full.result?.error?.message||'Official RPC rejected');return full.result.value})()`);
 const describe=()=>rpc('settings/describe',{});
 const mutate=async(ns,ops)=>{const before=(await describe()).namespaces.find(x=>x.ns===ns);if(!before)throw Error('Settings namespace missing '+ns);return rpc('settings/mutate',{ns,ops,expectedRevision:before.revision})};
 let original,originalAutomation,draft,configured=false,credentialStored=false,sent=false,primaryError,titleNamespaces=[];
 try {
  originalAutomation=await call('GetAutomation');
  if(originalAutomation.browserAccess?.requests?.length||originalAutomation.browserAccess?.sessions?.length)throw Error('Fixture did not start without existing browser authority');
  original=await describe();
  titleNamespaces=original.namespaces.filter(x=>['session-title-first-prompt-llm','session-title-all-prompts-llm'].includes(x.ns));
  const provider=original.namespaces.find(x=>x.ns==='llm-pi-ai'),defaults=original.namespaces.find(x=>x.ns==='agent-default-model');
  if(!provider||!defaults||provider.user?.providers?.[route]!==undefined)throw Error('Local model settings are not independently writable');
  const named=await rpc('credentials/describe',{refs:[credential]});if(named[credential]?.configured)throw Error('Dummy credential already exists');
  draft=await inspector.evaluate(dsh,`(()=>{const rows=[...document.querySelectorAll('[data-composer-input][role="textbox"][contenteditable="true"]')].filter(e=>e.getClientRects().length);if(rows.length!==1||rows[0].textContent.trim())throw Error('Expected the own empty provisional composer');return rows[0].innerHTML})()`);
  save('agent-original',{session,defaultUser:defaults.user??{},providerRoutePresent:false,draftEmpty:true});
  await call('InstallAutomationComponent','browser-mcp',false);
  const prepared=await wait('actual Browser component prepared',async()=>{const s=await call('GetAutomation');if(s.browser.failure)throw Error(s.browser.failure);return s.browser.dependency==='prepared'&&s.components.some(c=>c.id==='browser-mcp'&&c.state==='installed')&&s},60000);
  save('agent-prepared',{browser:prepared.browser,components:prepared.components});
  await call('SetAutomationEnabled','browser',true);await call('SetDefaultBrowserEnabled',true);
  credentialStored=true;await rpc('credentials/set',{ref:credential,value:'e67-local-fixture-only'});
  configured=true;
  await mutate('llm-pi-ai',[{op:'set',path:['providers',route],value:{displayName:'E67 local Linux',api:'openai-completions',apiKeyEnv:credential,baseURL:`http://127.0.0.1:${port}/v1`,models:[{id:model,name:'E67 local Linux snapshot',reasoning:false,input:['text'],contextWindow:32768,maxTokens:1024}]}}]);
  await mutate('agent-default-model',[{op:'set',path:['provider'],value:route},{op:'set',path:['model'],value:model},{op:'unset',path:['reasoningEffort']}]);
  for(const ns of titleNamespaces)await mutate(ns.ns,[{op:'set',path:['provider'],value:route},{op:'set',path:['model'],value:model}]);
  const catalog=await rpc('session/modelCatalog',{});if(catalog.default?.provider!==route||catalog.default?.model!==model)throw Error('Actual default model is not the local-only route');save('agent-local-model',{model,provider:route,baseURL:`http://127.0.0.1:${port}/v1`,defaultSelection:catalog.defaultSelection??catalog.default});
  // Only the real toolbar selects the page; only real composer Send makes a turn.
  await wait('Use for this conversation',()=>click('^Use for this conversation$'));
  const selected=await call('GetAutomation');if(selected.browserAccess?.sessions?.length)throw Error('Page selection granted authority before a real turn');save('agent-selection',selected.browserAccess);
  const editing=await inspector.evaluate(dsh,`(async()=>{const rows=[...document.querySelectorAll('[data-composer-input][role="textbox"][contenteditable="true"]')].filter(e=>e.getClientRects().length);if(rows.length!==1)throw Error('Own editable composer is not unique');const e=rows[0],events=[];const observe=event=>{if(events.length<10)events.push({type:event.type,inputType:event.inputType,dataLength:event.data?.length??0,trusted:event.isTrusted})};e.addEventListener('beforeinput',observe);e.addEventListener('input',observe);try{e.focus({preventScroll:true});const range=document.createRange();range.selectNodeContents(e);range.collapse(false);const selection=getSelection();selection.removeAllRanges();selection.addRange(range);document.dispatchEvent(new Event('selectionchange'));await new Promise(r=>setTimeout(r,0));if(document.activeElement!==e||!e.contains(selection.anchorNode)||!e.contains(selection.focusNode)||!selection.isCollapsed)return{matches:false,reason:'Caret did not belong to the own composer',active:document.activeElement===e,documentFocused:document.hasFocus(),anchorInside:e.contains(selection.anchorNode),focusInside:e.contains(selection.focusNode),collapsed:selection.isCollapsed,length:e.textContent.length,events};const accepted=document.execCommand('insertText',false,${JSON.stringify(prompt)});await new Promise(r=>setTimeout(r,0));return{accepted,matches:e.textContent===${JSON.stringify(prompt)},length:e.textContent.length,documentFocused:document.hasFocus(),active:document.activeElement===e,anchorInside:e.contains(selection.anchorNode),focusInside:e.contains(selection.focusNode),events}}finally{e.removeEventListener('beforeinput',observe);e.removeEventListener('input',observe)}})()`);
  save('agent-composer-input',editing);
  if(!editing.matches)throw Error('Own composer did not contain the exact fixed prompt');
  await wait('real composer Send',()=>click('^Send message$'));sent=true;
  for(const kind of ['browser','site']){
   const request=await wait('actual '+kind+' permission',async()=>{if(rejected)throw Error(rejected);const s=await call('GetAutomation');const r=s.browserAccess?.requests?.[0];if(!r)return;if(emitted!==1||r.session!==session||r.browser!=='embedded'||r.kind!==kind||r.phase!=='waiting'||r.allowAlways||kind==='site'&&r.site!==origin)throw Error('Unexpected permission scope');return r});
   save('agent-permission-'+kind,{request,origin,session,operation:'snapshot'});
   await wait('native permission screenshot '+kind,()=>existsSync(join(out,'agent-permission-'+kind+'.captured')),15000);
   const current=await call('GetAutomation'),r=current.browserAccess?.requests?.[0];
   if(!current.canChange||r?.id!==request.id||r.session!==session||r.kind!==kind||r.phase!=='waiting'||r.browser!=='embedded'||kind==='site'&&r.site!==origin)throw Error('Permission changed before the narrow answer');
   await call('AnswerBrowserConsent',request.id,'allow');
  }
  await wait('one actual snapshot result',async()=>{if(rejected)throw Error(rejected);return finished},45000);
  await wait('own Agent finished',()=>inspector.evaluate(dsh,`![...document.querySelectorAll('button')].some(e=>e.getClientRects().length&&e.getAttribute('aria-label')==='Stop')`));
  const trajectory=await rpc('session/events',{request:{address:{kind:'session',sessionId:session},maxMessages:20}});
  save('agent-trajectory',trajectory);save('agent-model-result',{requests,emitted,events,toolResult});
  const access=await call('GetAutomation');save('agent-authority',access.browserAccess);
  if(access.browserAccess?.sessions?.length!==1||access.browserAccess.sessions[0].session!==session)throw Error('Unexpected post-tool authority');
 } catch(error) { primaryError=error;save('agent-first-error',{error:String(error),requests,emitted,events,rejected,toolResult});throw error;
 } finally {
  const failures=[];const cleanup=async(label,fn)=>{try{await fn()}catch(error){failures.push({label,error:String(error)})}};
  if(sent)await cleanup('cancel own agent',()=>rpc('session/cancel',{request:{sessionId:session}}));
  await cleanup('Stop acquired browser scope',async()=>{const s=await call('GetAutomation'),uses=s.browserAccess?.sessions??[],requests=s.browserAccess?.requests??[];if(uses.some(use=>use.session!==session)||requests.some(r=>r.session!==session))throw Error('Foreign browser authority appeared');if(uses.length){for(const use of uses)await call('StopBrowserUse',session,use.lifetime)}else if(requests.length){await call('StopBrowserUse',session,'')}save('agent-stop',{grantedScopes:uses.length,pendingRequests:requests.length,action:uses.length||requests.length?'StopBrowserUse':'no acquired authority; restore original Browser flags'})});
  if(configured){await cleanup('restore temporary default',async()=>{
   const prior=original.namespaces.find(x=>x.ns==='agent-default-model').user??{};
   await mutate('agent-default-model',['provider','model','reasoningEffort'].map(key=>Object.hasOwn(prior,key)?{op:'set',path:[key],value:prior[key]}:{op:'unset',path:[key]}));
  });
  for(const ns of titleNamespaces)await cleanup('restore '+ns.ns,()=>mutate(ns.ns,['provider','model'].map(key=>Object.hasOwn(ns.user??{},key)?{op:'set',path:[key],value:ns.user[key]}:{op:'unset',path:[key]})));
  await cleanup('remove temporary route',async()=>{
   const providers=original.namespaces.find(x=>x.ns==='llm-pi-ai').user;
   await mutate('llm-pi-ai',[{op:'unset',path:providers&&Object.hasOwn(providers,'providers')?['providers',route]:['providers']}]);
  });}
  if(credentialStored)await cleanup('remove own dummy credential',()=>rpc('credentials/unset',{ref:credential}));
  if(originalAutomation){await cleanup('restore Browser intent',()=>call('SetDefaultBrowserEnabled',originalAutomation.defaultBrowserEnabled));await cleanup('restore Browser master',()=>call('SetAutomationEnabled','browser',originalAutomation.browser.enabled))}
  if(draft!==undefined)await cleanup('restore own empty draft',async()=>{const result=await inspector.evaluate(dsh,`(async()=>{const rows=[...document.querySelectorAll('[data-composer-input][role="textbox"][contenteditable="true"]')].filter(e=>e.getClientRects().length);if(rows.length!==1)throw Error('Own composer unavailable for restoration');const e=rows[0];if(e.textContent.trim()){e.focus({preventScroll:true});const range=document.createRange();range.selectNodeContents(e);const selection=getSelection();selection.removeAllRanges();selection.addRange(range);document.dispatchEvent(new Event('selectionchange'));await new Promise(r=>setTimeout(r,0));if(!e.contains(selection.anchorNode)||!e.contains(selection.focusNode))throw Error('Draft restoration selection escaped the composer');document.execCommand('delete');await new Promise(r=>setTimeout(r,0))}getSelection()?.removeAllRanges();return{empty:e.textContent.trim()==='',selectionCleared:getSelection()?.rangeCount===0}})()`);save('agent-draft-restored',result);if(!result.empty)throw Error('Own composer did not become empty')});
  if(original&&originalAutomation)await cleanup('read back restored configuration and authority',async()=>{
   const current=await describe();
   for(const ns of ['llm-pi-ai','agent-default-model',...titleNamespaces.map(x=>x.ns)])if(!isDeepStrictEqual(current.namespaces.find(x=>x.ns===ns)?.user??{},original.namespaces.find(x=>x.ns===ns)?.user??{}))throw Error('Settings restoration mismatch '+ns);
   const named=await rpc('credentials/describe',{refs:[credential]});if(credentialStored&&named[credential]?.configured)throw Error('Own dummy credential remains');
   const state=await call('GetAutomation');if(state.defaultBrowserEnabled!==originalAutomation.defaultBrowserEnabled||state.browser.enabled!==originalAutomation.browser.enabled||state.browserAccess?.sessions?.length||state.browserAccess?.requests?.length||state.browserAccess?.pendingActions||state.browserAccess?.policyRevision!==originalAutomation.browserAccess?.policyRevision||!isDeepStrictEqual(state.browserAccess?.policy,originalAutomation.browserAccess?.policy))throw Error('Browser intent, policy or authority was not restored');
  });
  await new Promise(resolve=>{server.close(resolve);server.closeAllConnections()});
  const stopped=await call('GetAutomation').catch(()=>undefined);save('agent-restored',{failures,authority:stopped?.browserAccess,requests,emitted,events,rejected});
  if(failures.length&&!primaryError)throw Error('Local Agent fixture cleanup incomplete: '+failures.map(f=>f.label).join(', '));
 }
 save('agent-completed',{session,readOnly:true,emitted,modelHTTP:'self-owned localhost only'});
}
