import {createRequire} from 'node:module';
import {pathToFileURL} from 'node:url';
export const inject=['agents','sessions','tools','systemPrompt','webServer','connection'];
export async function apply(ctx,config){
 const require=createRequire(config.cli);
 const {assembleContextFor}=await import(pathToFileURL(require.resolve('@deepseek-ai/dsh-agent')).href);
 const computer=config.computer==='true',product=config.product==='true'||computer,prefix=computer?'my_computer_':'my_browser_';
 const modelContract=config.modelContract==='true';
 if(modelContract){
  const {DeepSeekAdapter,resolveAdapterOptions}=require('@deepseek-ai/dsh-llm-deepseek');
  const options=resolveAdapterOptions({baseURL:'http://offline.invalid',models:[{id:'fixture-vision',inputModalities:['text','image']}]});
  // Admission metadata only. This isolated fixture never starts a model turn.
  ctx.effect(()=>ctx.get('llm').registerAdapter(['fixture-offline'],new DeepSeekAdapter({options:()=>options,resolveAuth:async()=>{throw Error('fixture prohibits model requests')}})));
 }
 const handles=new Map();let old,approvalOutcome='rejected',testTurn=0;
 if(product)ctx.on('approval/request',(request,next)=>request.toolName.startsWith(prefix)?approvalOutcome:next(),{prepend:true,global:true});
 ctx.effect(()=>ctx.webServer.register({kind:'exact',path:'/__dshwork/automation-fixture',handler:async(req,res)=>{
  if(ctx.connection.requestRejection(req)!==undefined || req.headers['x-dsh-work-token']!==config.token){res.writeHead(403);res.end();return}
  try{
   const u=new URL(req.url,'http://localhost'),action=u.searchParams.get('action'),id=u.searchParams.get('id')||'a';let value;
   if(action==='create') {const parent=handles.get(u.searchParams.get('parent'))?.agent;const h=await ctx.agents.create({sessionId:id,...(modelContract?{agentOptions:{provider:'fixture-offline',model:'fixture-vision'}}:{}),...(parent?{parentAgent:parent}:{}),meta:{cwd:config.cwd,...(parent?{parentSession:parent.session.id,origin:'subagent',delegationDepth:1}:{})}});handles.set(id,h);if(computer)h.agent.session.append('turn/start',{turn:++testTurn});value={id:h.agent.id};}
   const agent=handles.get(id)?.agent;
   if(action==='schemas'){const assembled=await ctx.systemPrompt.assemble(assembleContextFor(agent,new AbortController().signal));value=assembled.tools.filter(s=>product?s.name.startsWith(prefix):s.name==='my_automation_probe');if(value.length)old=value[0];}
   if(action==='approval'&&product){approvalOutcome=u.searchParams.get('outcome')==='allowed-once'?'allowed-once':'rejected';value={changed:true}}
   if(action==='call'||action==='old'||action==='bad'){
    const run=signal=>ctx.tools.execute({callId:crypto.randomUUID(),name:product?u.searchParams.get('tool'):'my_automation_probe',arguments:product?JSON.parse(u.searchParams.get('args')||'{}'):action==='bad'?{session:'b'}:{},signal,agent,...(action==='old'?{schema:old}:{})});
    if(computer)value=await run(new AbortController().signal);else if(product){const turn=++testTurn;agent.session.append('turn/start',{turn});try{value=await run(new AbortController().signal)}finally{agent.session.append('turn/end',{turn,reason:{kind:'completed'}})}}else value=await run(new AbortController().signal);
   }
   if(action==='child-complete'){const h=await ctx.agents.create({parentAgent:agent,meta:{cwd:config.cwd,parentSession:agent.session.id,origin:'subagent',delegationDepth:1}});h.agent.session.append('turn/start',{turn:1});h.agent.session.append('turn/end',{turn:1,reason:{kind:'completed'}});await h.dispose();value={childCompleted:true}}
   if(action==='complete'){agent.session.append('turn/end',{turn:testTurn,reason:{kind:'completed'}});value={completed:true}}
   if(action==='message'){agent.session.append('user/message',{source:{kind:'user'},content:[]});value={message:true}}
   if(action==='stop'){agent.session.append('turn/start',{turn:1});agent.session.append('turn/end',{turn:1,reason:{kind:'aborted',reason:{kind:'user'}}});value={stopped:true}}
   if(action==='dispose'){await handles.get(id).dispose();handles.delete(id);value={disposed:true}}
   res.writeHead(200);res.end(JSON.stringify(value??{}));
  }catch(error){res.writeHead(200);res.end(JSON.stringify({error:String(error)}))}
 }}));
 ctx.effect(()=>async()=>{for(const h of handles.values())await h.dispose()});
}
