import test from 'node:test';
import assert from 'node:assert/strict';
import {EventEmitter} from 'node:events';
import {Readable} from 'node:stream';
import {apply,identity,projectTool} from './host.js';

test('MCP projection preserves the actual description and JSON schema',()=>{
 const schema={name:'browser_snapshot',description:'Actual MCP description',inputSchema:{type:'object',properties:{},additionalProperties:false}};
 const tool=projectTool(schema,()=>{});assert.equal(tool.name,'my_browser_snapshot');assert.equal(tool.parameters,schema.inputSchema);assert.equal(tool.description,schema.description);
});
test('trusted parent resolution rejects missing, broken and cyclic identities',()=>{
 const a={id:'a',session:{id:'a',header:{}}},child={id:'child',session:{id:'child',header:{parentSession:'a'}}};
 const agents=new Map([['a',a],['child',child]]),sessions=new Map([['a',a.session],['child',child.session]]);
 assert.equal(identity({agent:child},agents,sessions),a);assert.throws(()=>identity({},agents,sessions),/UNTRUSTED/);assert.throws(()=>identity({agent:{...a}},agents,sessions),/UNTRUSTED/);
 child.session.header.parentSession='missing';assert.throws(()=>identity({agent:child},agents,sessions),/PARENT_NOT_FOUND/);
 child.session.header.parentSession='child';assert.throws(()=>identity({agent:child},agents,sessions),/INVALID_PARENT_CHAIN/);
});
function harness(validation){
 const routes=new Map(),definitions=new Map(),effects=[],events=new Map();
 const a={id:'a',session:{id:'a',header:{}}};
 const ctx={webServer:{register:route=>{routes.set(route.path,route.handler);return ()=>routes.delete(route.path)}},connection:{requestRejection:()=>undefined},agents:new Map([['a',a]]),sessions:new Map([['a',a.session]]),tools:{register:definition=>{definitions.set(definition.name,definition);return ()=>definitions.delete(definition.name)}},effect:fn=>{const dispose=fn();if(dispose)effects.push(dispose)},on:(name,fn)=>events.set(name,fn)};
 const token='a'.repeat(64);apply(ctx,{generation:'worker',token,validation,mcpToolFactory:(_ctx,options)=>projectTool({name:options.rawName,description:options.description,inputSchema:options.inputSchema},options.call)});
 async function request(route,value,auth=token){const req=Readable.from(value===undefined?[]:[Buffer.from(JSON.stringify(value))]);req.headers={'x-dsh-work-token':auth};req.method=route==='events'?'GET':'POST';const res=new EventEmitter();Object.assign(res,{status:0,chunks:[],writeHead(status){this.status=status},write(chunk){this.chunks.push(chunk)},end(value){this.value=value;this.emit('close')},destroy(){this.emit('close')},flushHeaders(){}});await routes.get('/__dshwork/automation/'+route)(req,res);return res}
 return {ctx,events,a,definitions,request,close:()=>effects.reverse().forEach(fn=>fn())};
}
const state=(epoch,enabled=true)=>({generation:'worker',revision:epoch,canChange:true,defaultBrowserEnabled:true,gatewayConnected:true,browser:{enabled,epoch,dependency:'prepared'}});
test('product scope exposes no probe, even with dependencies prepared',async()=>{const h=harness(false);try{await h.request('state',state(1));const stream=await h.request('events');assert.equal(h.definitions.size,0);stream.end()}finally{h.close()}});
test('old execute references stay revoked after reenable; channel auth and disconnect fail closed',async()=>{
 const h=harness(true);try{
  assert.equal((await h.request('state',state(1),'bad')).status,403);
  await h.request('state',state(1));const stream=await h.request('events');const old=h.definitions.get('my_automation_probe');assert.ok(old);
  await h.request('state',state(2,false));assert.equal(h.definitions.size,0);
  await h.request('state',state(3));assert.ok(h.definitions.has('my_automation_probe'));
  const exec={agent:h.a,signal:new AbortController().signal};await assert.rejects(old.execute({},exec),/REVOKED/);
  const next=h.definitions.get('my_automation_probe');await assert.rejects(next.execute({session:'b'},exec),/INVALID_ARGUMENT/);
  const pending=next.execute({},exec);const frame=JSON.parse(stream.chunks.find(chunk=>chunk.startsWith('data: ')).slice(6));
  assert.equal(frame.request.session,'a');assert.equal(frame.request.epoch,3);assert.equal(frame.request.operation,'probe');
  await h.request('reply',{generation:'worker',id:frame.id,value:{content:[]}});assert.deepEqual(await pending,{content:[]});
  stream.end();assert.equal(h.definitions.size,0);await assert.rejects(next.execute({},exec),/REVOKED/);
 }finally{h.close()}
});

test('browser cwd comes from trusted top session, never child or tool arguments',async()=>{
 const h=harness(false);try{
  h.a.session.header.cwd='C:\\scratch-top';
  const child={id:'child',session:{id:'child',header:{parentSession:'a',cwd:'C:\\scratch-child'}}};
  h.ctx.agents.set(child.id,child);h.ctx.sessions.set(child.id,child.session);
  const value=state(1);value.browserTools=[{name:'browser_snapshot',description:'snapshot',inputSchema:{type:'object',properties:{}}}];
  await h.request('state',value);const stream=await h.request('events');
  const pending=h.definitions.get('my_browser_snapshot').execute({workingDirectory:'C:\\untrusted'},{agent:child,signal:new AbortController().signal});
  const frame=JSON.parse(stream.chunks.find(chunk=>chunk.startsWith('data: ')).slice(6));
  assert.equal(frame.request.session,'a');assert.equal(frame.request.workingDirectory,h.a.session.header.cwd);
  assert.notEqual(frame.request.workingDirectory,frame.request.arguments.workingDirectory);
  await h.request('reply',{generation:'worker',id:frame.id,value:{content:[]}});await pending;
 }finally{h.close()}
});

test('top user-stop signal stops shared resource while only child browser tool runs',async()=>{
 const h=harness(false);try{
  const rootSignal=new AbortController(),childSignal=new AbortController();
  const child={id:'child-stop',session:{id:'child-stop',header:{parentSession:'a'}}};h.ctx.agents.set(child.id,child);h.ctx.sessions.set(child.id,child.session);
  const value=state(1);value.browserTools=[{name:'browser_snapshot',description:'snapshot',inputSchema:{type:'object',properties:{}}}];
  await h.request('state',value);const stream=await h.request('events');
  await h.events.get('agent/pre-step')({agent:h.a,signal:rootSignal.signal},()=>Promise.resolve());
  const pending=h.definitions.get('my_browser_snapshot').execute({},{agent:child,signal:childSignal.signal});
  const call=JSON.parse(stream.chunks.find(c=>c.startsWith('data: ')).slice(6));rootSignal.abort({kind:'user'});
  const frames=stream.chunks.filter(c=>c.startsWith('data: ')).map(c=>JSON.parse(c.slice(6)));
  assert.equal(frames[1].kind,'stop');assert.equal(frames[1].request.session,'a');assert.equal(frames[1].request.lifetime,call.request.lifetime);assert.equal(childSignal.signal.aborted,false);
  await h.request('reply',{generation:'worker',id:call.id,error:'AUTOMATION_REVOKED'});await assert.rejects(pending,/AUTOMATION_REVOKED/);
 }finally{h.close()}
});

test('runtime-context messages never create a new browser authorization round',async()=>{
 const h=harness(false);try{
  await h.request('state',{...state(1),browserAccess:{}});const stream=await h.request('events');
  const emit=source=>h.events.get('session/event')(h.a.session,{type:'user/message',data:{source:{kind:source}}});
  emit('user');emit('runtime-context');
  await new Promise(resolve=>setImmediate(resolve));
  const frames=stream.chunks.filter(c=>c.startsWith('data: ')).map(c=>JSON.parse(c.slice(6)));
  assert.equal(frames.length,1,'one actual message plus runtime context must sync only one round');
  await h.request('reply',{generation:'worker',id:frames[0].id,value:{lifetime:frames[0].request.lifetime}});
 }finally{h.close()}
});

test('consecutive actual messages synchronize rounds in order with the latest Go lifetime',async()=>{
 const h=harness(false);try{
  await h.request('state',{...state(1),browserAccess:{}});const stream=await h.request('events');
  const emit=()=>h.events.get('session/event')(h.a.session,{type:'user/message',data:{source:{kind:'user'}}});
  const frames=()=>stream.chunks.filter(c=>c.startsWith('data: ')).map(c=>JSON.parse(c.slice(6)));
  emit();emit();await new Promise(resolve=>setImmediate(resolve));
  assert.equal(frames().length,1,'a later round must not race its predecessor');
  await h.request('reply',{generation:'worker',id:frames()[0].id,value:{lifetime:'renewed-by-Go'}});
  await new Promise(resolve=>setImmediate(resolve));
  assert.equal(frames().length,2);assert.equal(frames()[1].request.lifetime,'renewed-by-Go');
  await h.request('reply',{generation:'worker',id:frames()[1].id,value:{lifetime:'renewed-by-Go'}});
 }finally{h.close()}
});
