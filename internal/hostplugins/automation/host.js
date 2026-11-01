import {randomUUID, timingSafeEqual} from 'node:crypto';
export const name = 'dsh-work-automation';
export const inject = ['webServer', 'connection', 'agents', 'sessions', 'tools'];

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
 const pending=new Map(), lifetimes=new WeakMap();
 function lifetime(top){if(!lifetimes.has(top))lifetimes.set(top,randomUUID());return lifetimes.get(top)}
 const failAll=()=>{for(const p of pending.values())p.reject(Error('AUTOMATION_HOST_UNAVAILABLE'));pending.clear()};
 function unregister(){for(const dispose of disposers.splice(0))dispose();registeredEpoch=undefined}
 function disconnect(){stream=undefined;state=undefined;unregister();failAll()}
 function send(message){if(!stream || closed)throw Error('AUTOMATION_HOST_UNAVAILABLE');stream.write(`data: ${JSON.stringify(message)}\n\n`)}
 function call(request,signal){
  signal.throwIfAborted();if(pending.size>=64)throw Error('AUTOMATION_BUSY');
  return new Promise((resolve,reject)=>{
   const id=randomUUID();const abort=()=>{try{send({id,kind:'cancel',request})}catch{};finish(Error('AUTOMATION_CANCELLED'))};
   const timer=setTimeout(()=>finish(Error('AUTOMATION_HOST_TIMEOUT')),15000);
   function finish(error,value){if(!pending.delete(id))return;clearTimeout(timer);signal.removeEventListener('abort',abort);error?reject(error):resolve(value)}
   pending.set(id,{resolve:value=>finish(null,value),reject:error=>finish(error)});signal.addEventListener('abort',abort,{once:true});
   try{send({id,kind:'call',request})}catch(error){finish(error)}
  });
 }
 function reconcile(){
  const cap=state?.browser;
  const epoch=cap?.epoch;
  if(!(config.validation===true || config.validation==='true') || !stream || !state?.canChange || !state.gatewayConnected || !state.defaultBrowserEnabled || !cap?.enabled || cap.dependency!=='prepared'){unregister();return}
  if(registeredEpoch===epoch)return;
  unregister();registeredEpoch=epoch;
  const schema={name:'automation_probe',description:'Validate DSH Work host routing without browser or desktop actions.',inputSchema:{type:'object',properties:{},additionalProperties:false}};
  const definition=projectTool(schema,async(args,exec)=>{
   if(closed || !stream || state?.browser.epoch!==epoch || !state?.browser.enabled || !state.gatewayConnected || !state.defaultBrowserEnabled || !state.canChange)throw Error('AUTOMATION_REVOKED');
   if(!args||typeof args!=='object'||Array.isArray(args)||Object.keys(args).length)throw Error('AUTOMATION_INVALID_ARGUMENT');
   const top=identity(exec,ctx.agents,ctx.sessions);
   const result=await call({generation:config.generation,epoch,capability:'browser',session:top.session.id,lifetime:lifetime(top),operation:'probe',tool:schema.name,arguments:args},exec.signal);
   if(state?.browser.epoch!==epoch)throw Error('AUTOMATION_REVOKED');
   return result;
  });
  disposers.push(ctx.tools.register(definition));
 }
 const stop=agent=>{if(!agent || agent.session.header.parentSession)return;const id=lifetimes.get(agent);if(!id)return;try{send({kind:'stop',request:{generation:config.generation,session:agent.session.id,lifetime:id}})}catch{}};
 ctx.on('agent/disposed',({agent})=>stop(agent));
 ctx.on('session/event',(session,event)=>{if(event.type==='turn/end' && event.data?.reason?.kind==='aborted' && event.data.reason.reason?.kind==='user')stop(ctx.agents.get(session.id))});
 function authorized(req){const actual=Buffer.from(req.headers['x-dsh-work-token']??''),expected=Buffer.from(config.token??'');return expected.length>=32 && actual.length===expected.length && timingSafeEqual(actual,expected)}
 const reply=(res,status,value)=>{res.writeHead(status,{'Content-Type':'application/json','Cache-Control':'no-store'});res.end(JSON.stringify(value))};
 async function body(req){let n=0;const chunks=[];for await(const chunk of req){n+=chunk.length;if(n>65536)throw Error('limit');chunks.push(chunk)};return JSON.parse(Buffer.concat(chunks).toString())}
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
 ctx.effect(()=>()=>{closed=true;unregister();stream?.end();disconnect()});
}
