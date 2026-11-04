// Isolated acceptance HTTP driver, never installed into normal product scope.
import {createRequire} from 'node:module';
import {pathToFileURL} from 'node:url';
export const inject=['agents','sessions','tools','systemPrompt','webServer','connection'];
export async function apply(ctx,config){
 const require=createRequire(config.cli);
 const {assembleContextFor}=await import(pathToFileURL(require.resolve('@deepseek-ai/dsh-agent')));
 const handles=new Map(),calls=new Map(),maintenance=new Set();
 ctx.effect(()=>ctx.webServer.register({kind:'exact',path:'/__dshwork/browser-acceptance',handler:async(req,res)=>{
  if(ctx.connection.requestRejection(req)!==undefined||req.headers['x-dsh-work-token']!==config.token){res.writeHead(403).end();return}
  try{
   let n=0;const chunks=[];for await(const b of req){n+=b.length;if(n>512*1024)throw Error('limit');chunks.push(b)}
   const v=JSON.parse(Buffer.concat(chunks)),id=v.session;let result;
   if(v.action==='create'){const parent=handles.get(v.parent)?.agent;const handle=await ctx.agents.create({sessionId:id,...(parent?{parentAgent:parent}:{}),meta:{cwd:config.cwd,...(parent?{parentSession:parent.session.id,origin:'subagent',delegationDepth:1}:{})}});handles.set(id,handle);result={id:handle.agent.id};}
   const agent=handles.get(id)?.agent;
   if(v.action==='schemas'){const prompt=await ctx.systemPrompt.assemble(assembleContextFor(agent,new AbortController().signal));result=prompt.tools.filter(t=>t.name.startsWith('my_browser_'));}
   if(v.action==='call'){
    const controller=new AbortController();calls.set(v.id,controller);
    try{result=await ctx.tools.execute({callId:v.id,name:v.tool,arguments:v.arguments??{},signal:controller.signal,agent})}finally{calls.delete(v.id)}
   }
   if(v.action==='maintenance-call'){maintenance.add(id);try{result=await agent.runMaintenance(signal=>ctx.tools.execute({callId:v.id,name:v.tool,arguments:v.arguments??{},signal,agent}));}finally{maintenance.delete(id)}}
   if(v.action==='stop-agent'){const active=maintenance.has(id);agent.cancel({kind:'user'});result={stopped:true,active};}
   if(v.action==='cancel'){const cancelled=calls.has(v.id);calls.get(v.id)?.abort();result={cancelled}}
   if(v.action==='stop'){agent.session.append('turn/start',{turn:1});agent.session.append('turn/end',{turn:1,reason:{kind:'aborted',reason:{kind:'user'}}});result={stopped:true}}
   if(v.action==='dispose'){await handles.get(id).dispose();handles.delete(id);result={disposed:true}}
   res.writeHead(200,{'Content-Type':'application/json'});res.end(JSON.stringify(result??{}));
  }catch(e){res.writeHead(200,{'Content-Type':'application/json'});res.end(JSON.stringify({error:e.message}))}
 }}));
 ctx.effect(()=>async()=>{for(const h of handles.values())await h.dispose()});
}
