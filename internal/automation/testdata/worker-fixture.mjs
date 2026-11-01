import {createRequire} from 'node:module';
import {pathToFileURL} from 'node:url';
export const inject=['agents','sessions','tools','systemPrompt','webServer','connection'];
export async function apply(ctx,config){
 const require=createRequire(config.cli);
 const {assembleContextFor}=await import(pathToFileURL(require.resolve('@deepseek-ai/dsh-agent')).href);
 const handles=new Map();let old;
 ctx.effect(()=>ctx.webServer.register({kind:'exact',path:'/__dshwork/automation-fixture',handler:async(req,res)=>{
  if(ctx.connection.requestRejection(req)!==undefined || req.headers['x-dsh-work-token']!==config.token){res.writeHead(403);res.end();return}
  try{
   const u=new URL(req.url,'http://localhost'),action=u.searchParams.get('action'),id=u.searchParams.get('id')||'a';let value;
   if(action==='create') {const parent=handles.get(u.searchParams.get('parent'))?.agent;const h=await ctx.agents.create({sessionId:id,...(parent?{parentAgent:parent}:{}),meta:{cwd:config.cwd,...(parent?{parentSession:parent.session.id,origin:'subagent',delegationDepth:1}:{})}});handles.set(id,h);value={id:h.agent.id};}
   const agent=handles.get(id)?.agent;
   if(action==='schemas'){const assembled=await ctx.systemPrompt.assemble(assembleContextFor(agent,new AbortController().signal));value=assembled.tools.filter(s=>s.name==='my_automation_probe');if(value.length)old=value[0];}
   if(action==='call'||action==='old'||action==='bad')value=await ctx.tools.execute({callId:crypto.randomUUID(),name:'my_automation_probe',arguments:action==='bad'?{session:'b'}:{},signal:new AbortController().signal,agent,...(action==='old'?{schema:old}:{})});
   if(action==='stop'){agent.session.append('turn/start',{turn:1});agent.session.append('turn/end',{turn:1,reason:{kind:'aborted',reason:{kind:'user'}}});value={stopped:true}}
   if(action==='dispose'){await handles.get(id).dispose();handles.delete(id);value={disposed:true}}
   res.writeHead(200);res.end(JSON.stringify(value??{}));
  }catch(error){res.writeHead(200);res.end(JSON.stringify({error:String(error)}))}
 }}));
 ctx.effect(()=>async()=>{for(const h of handles.values())await h.dispose()});
}
