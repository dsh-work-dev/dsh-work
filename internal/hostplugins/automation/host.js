import {randomUUID, timingSafeEqual} from 'node:crypto';
export const name = 'dsh-work-automation';
export const inject = ['webServer', 'connection', 'agents', 'sessions', 'tools', 'sessionProjections'];

// Identity comes only from actual rc.5 registries, never tool parameters.
export function identity(exec, agents, sessions) {
 if (!exec.agent || agents.get(exec.agent.id) !== exec.agent) throw Error('AUTOMATION_UNTRUSTED_AGENT');
 let session=exec.agent.session; const seen=new Set();
 while(session) {
  if(seen.has(session.id)||seen.size>=64) throw Error('AUTOMATION_INVALID_PARENT_CHAIN');
  seen.add(session.id);
  if(!session.header.parentSession) break;
  session=sessions.get(session.header.parentSession);
  if(!session) throw Error('AUTOMATION_PARENT_NOT_FOUND');
 }
 const top=agents.get(session?.id);
 if(!top || top.session!==session) throw Error('AUTOMATION_TOP_SESSION_INACTIVE');
 return top;
}
// MCP JSON schema and descriptions pass through unchanged. Validation belongs
// to the SDK adapter before this boundary, not raw tools.register().
export function projectTool(schema, execute) {
 return {name:'my_'+schema.name,description:schema.description,parameters:schema.inputSchema,timeoutMs:90000,
 output:{schema:{type:'object',additionalProperties:true},render:(_args,result)=>result.content??[]},execute};
}
export function apply(ctx,config) {
 let state,stream,closed=false,disposers=[],registeredEpoch;
 const pending=new Map(), lifetimes=new WeakMap(), rounds=new WeakMap(), roundSync=new WeakMap(), approved=new WeakMap(), activityWatches=new Map();
 function lifetime(top){if(!lifetimes.has(top))lifetimes.set(top,randomUUID());return lifetimes.get(top)}
 const failAll=()=>{for(const p of pending.values())p.reject(Error('AUTOMATION_HOST_UNAVAILABLE'));pending.clear()};
 function unregister(){for(const dispose of disposers.splice(0))dispose();registeredEpoch=undefined}
 function disconnect(){stream=undefined;state=undefined;unregister();failAll()}
 function send(message){if(!stream || closed)throw Error('AUTOMATION_HOST_UNAVAILABLE');stream.write(`data: ${JSON.stringify(message)}\n\n`)}
 function call(request,signal,topLevel){
  signal.throwIfAborted();if(pending.size>=64)throw Error('AUTOMATION_BUSY');
  return new Promise((resolve,reject)=>{
   const browser=request.operation==='browser'||request.operation==='authorize';
   const id=randomUUID();const abort=()=>{const rootStop=browser&&topLevel&&['user','disposed'].includes(signal.reason?.kind);try{send({id,kind:rootStop?'stop':'cancel',request})}catch{};if(!browser)finish(Error('AUTOMATION_CANCELLED'))};
   const timer=setTimeout(()=>{if(browser)try{send({kind:'stop',request})}catch{};finish(Error('AUTOMATION_HOST_TIMEOUT'))},request.operation==='authorize'?310000:browser?125000:15000);
   function finish(error,value){if(!pending.delete(id))return;clearTimeout(timer);signal.removeEventListener('abort',abort);error?reject(error):resolve(value)}
   pending.set(id,{resolve:value=>finish(null,value),reject:error=>finish(error)});signal.addEventListener('abort',abort,{once:true});
   try{send({id,kind:'call',request})}catch(error){finish(error)}
  });
 }
 function round(top){if(!rounds.has(top))rounds.set(top,{turn:randomUUID(),synced:false});return rounds.get(top)}
 async function syncRound(top){
  if(!state?.browserAccess)return;
  const entry=round(top);if(entry.synced)return;
  if(!entry.pending){
   // Serialize real user-message boundaries: a stopped round may mint a new
   // Go lifetime, which the following round must carry rather than race.
   const prior=roundSync.get(top)??Promise.resolve();
   entry.pending=prior.catch(()=>{}).then(()=>call({generation:config.generation,capability:'browser',operation:'turn',session:top.session.id,lifetime:lifetime(top),turn:entry.turn},new AbortController().signal,true)).then(value=>{lifetimes.set(top,value.lifetime);entry.synced=true}).finally(()=>{entry.pending=undefined});
   roundSync.set(top,entry.pending);
  }
  await entry.pending;
  if(round(top)!==entry)await syncRound(top);
 }
 function browserRequest(top,args,tool,epoch){
  const values=ctx.sessionProjections?.snapshot(top.session).values,title=values?.title?.title??values?.title;
  return {generation:config.generation,epoch,capability:'browser',session:top.session.id,lifetime:lifetime(top),...(state?.browserAccess?{turn:round(top).turn}:{}),workingDirectory:top.session.header.cwd,title:typeof title==='string'?[...title.replace(/[\u0000-\u001f\u007f]/g,' ')].slice(0,120).join(''):top.session.id,operation:'browser',tool,arguments:args};
 }
 ctx.on('tools/pre-execute',async(exec,next)=>{
  if(!state?.browserAccess||!exec.name.startsWith('my_browser_'))return next();
  const schema=state.browserTools?.find(item=>'my_'+item.name===exec.name);
  if(!schema)return {kind:'deny',reason:'This browser tool is not available.'};
  const top=identity(exec,ctx.agents,ctx.sessions);await syncRound(top);
  const request=browserRequest(top,exec.arguments,schema.name,state.browser.epoch);
  const decision=await call({...request,operation:'authorize'},exec.signal,exec.agent===top);
  request.epoch=decision.epoch;request.permit=decision.permit;
  approved.set(exec,{request});
  const downstream=await next();
  if(downstream.kind==='deny'||downstream.kind==='cancel')return downstream;
  if(decision.kind==='ask'){
   const displayReason={...decision.displayReason};
   if(downstream.kind==='ask')for(const locale of ['en','zh-CN','ja-JP'])displayReason[locale]+='\n'+(downstream.displayReason?.[locale]??downstream.reason??'');
   return {kind:'ask',reason:decision.reason,displayReason};
  }
  return downstream;
 });
 if(typeof ctx.tools.guard==='function')ctx.effect(()=>ctx.tools.guard(exec=>state?.browserAccess&&exec.name.startsWith('my_browser_')&&!approved.has(exec)?'Browser authorization is required.':undefined));
 ctx.on('tools/result',(exec)=>{const entry=approved.get(exec);if(entry){approved.delete(exec);try{send({kind:'discard',request:entry.request})}catch{}}});
 function reconcile(){
  const cap=state?.browser;
  const epoch=cap?.epoch;
  const validation=config.validation===true || config.validation==='true';
  const schemas=validation?[{name:'automation_probe',description:'Validate DSH Work host routing without browser or desktop actions.',inputSchema:{type:'object',properties:{},additionalProperties:false}}]:(state?.browserTools??[]);
  if(!schemas.length || !stream || !state?.canChange || !state.gatewayConnected || !state.defaultBrowserEnabled || !cap?.enabled || cap.dependency!=='prepared'){unregister();return}
  if(registeredEpoch===epoch)return;
  unregister();registeredEpoch=epoch;
  for(const schema of schemas){
  const definition=projectTool(schema,async(args,exec)=>{
   if(closed || !stream || !state?.browserAccess&&state?.browser.epoch!==epoch || !state?.browser.enabled || !state.gatewayConnected || !state.defaultBrowserEnabled || !state.canChange)throw Error('AUTOMATION_REVOKED');
   if(!args||typeof args!=='object'||Array.isArray(args)||validation&&Object.keys(args).length)throw Error('AUTOMATION_INVALID_ARGUMENT');
   const top=identity(exec,ctx.agents,ctx.sessions);
   let request;
   if(state?.browserAccess){
    const entry=approved.get(exec);if(!entry)throw Error('BROWSER_AUTHORIZATION_REQUIRED');
    request=entry.request;
    if(request.session!==top.session.id||request.lifetime!==lifetime(top)||request.turn!==round(top).turn)throw Error('AUTOMATION_REVOKED');
   }else request=browserRequest(top,args,schema.name,epoch);
   request={...request,operation:validation?'probe':'browser'};
   const result=await call(request,exec.signal,exec.agent===top);
   exec.signal.throwIfAborted();
   if(state?.browserAccess?state.browser.epoch>request.epoch:state?.browser.epoch!==epoch)throw Error('AUTOMATION_REVOKED');
   if(result?.isError)throw Error(result.content?.filter(x=>x.type==='text').map(x=>x.text).join('\n')||'MCP tool failed');
   return result;
  });
  disposers.push(ctx.tools.register(definition));
  }
 }
 const stop=agent=>{if(!agent || agent.session.header.parentSession)return;const id=lifetimes.get(agent);if(!id)return;try{send({kind:'stop',request:{generation:config.generation,session:agent.session.id,lifetime:id}})}catch{}};
 function clearActivity(agent){const watch=activityWatches.get(agent);if(watch){watch.signal.removeEventListener('abort',watch.abort);activityWatches.delete(agent)}}
 // The actual top turn signal stops owned browser work even while only a child
 // is executing a browser tool. Waiting for turn/end would wait for that tool.
 ctx.on('agent/pre-step',({agent,signal},next)=>{
  if(!agent.session.header.parentSession&&ctx.agents.get(agent.id)===agent&&activityWatches.get(agent)?.signal!==signal){
   clearActivity(agent);const abort=()=>{if(['user','disposed'].includes(signal.reason?.kind))stop(agent)};
   activityWatches.set(agent,{signal,abort});signal.addEventListener('abort',abort,{once:true});if(signal.aborted)abort();
  }
  return next();
 });
 ctx.on('agent/disposed',({agent})=>{clearActivity(agent);stop(agent)});
 ctx.on('session/event',(session,event)=>{
  const agent=ctx.agents.get(session.id);
  // rc.5 also logs synthetic runtime context as user/message on each step.
  // Only an actual user source is a new authorization round.
  if(event.type==='user/message'&&event.data?.source?.kind==='user'&&agent&&agent.session===session&&!session.header.parentSession){clearActivity(agent);rounds.set(agent,{turn:randomUUID(),synced:false});void syncRound(agent).catch(()=>{});}
  if(event.type==='turn/end'){clearActivity(agent);if(!state?.browserAccess&&event.data?.reason?.kind==='aborted'&&event.data.reason.reason?.kind==='user')stop(agent)}
 });
 function authorized(req){const actual=Buffer.from(req.headers['x-dsh-work-token']??''),expected=Buffer.from(config.token??'');return expected.length>=32 && actual.length===expected.length && timingSafeEqual(actual,expected)}
 const reply=(res,status,value)=>{res.writeHead(status,{'Content-Type':'application/json','Cache-Control':'no-store'});res.end(JSON.stringify(value))};
 async function body(req){let n=0;const chunks=[];for await(const chunk of req){n+=chunk.length;if(n>8*1024*1024)throw Error('limit');chunks.push(chunk)};return JSON.parse(Buffer.concat(chunks).toString())}
 for(const route of ['state','events','reply'])ctx.effect(()=>ctx.webServer.register({kind:'exact',path:'/__dshwork/automation/'+route,handler:async(req,res)=>{
  const rejection=ctx.connection.requestRejection(req);if(rejection!==undefined)return reply(res,rejection,{});
  if(!authorized(req))return reply(res,403,{});
  if(req.method!==(route==='events'?'GET':'POST'))return reply(res,405,{});
  try {
   if(route==='events'){
    if(stream)return reply(res,409,{});
    stream=res;res.writeHead(200,{'Content-Type':'text/event-stream','Cache-Control':'no-store'});res.flushHeaders?.();res.write(': connected\n\n');reconcile();
    const timer=setInterval(()=>{try{res.write(': ping\n\n')}catch{res.destroy()}},15000);
    res.on('close',()=>{clearInterval(timer);if(stream===res)disconnect()});return;
   }
   const value=await body(req);if(value.generation!==config.generation)return reply(res,409,{});
   if(route==='state'){
    if(!state || value.revision>state.revision){state=value;reconcile()}
   }else{const p=pending.get(value.id);if(p)value.error?p.reject(Error(value.error)):p.resolve(value.value)}
   reply(res,200,{});
  }catch{reply(res,400,{})}
 }}));
 ctx.effect(()=>()=>{closed=true;for(const agent of activityWatches.keys())clearActivity(agent);unregister();stream?.end();disconnect()});
}
