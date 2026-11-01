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
 const token='a'.repeat(64);apply(ctx,{generation:'worker',token,validation});
 async function request(route,value,auth=token){const req=Readable.from(value===undefined?[]:[Buffer.from(JSON.stringify(value))]);req.headers={'x-dsh-work-token':auth};req.method=route==='events'?'GET':'POST';const res=new EventEmitter();Object.assign(res,{status:0,chunks:[],writeHead(status){this.status=status},write(chunk){this.chunks.push(chunk)},end(value){this.value=value;this.emit('close')},destroy(){this.emit('close')},flushHeaders(){}});await routes.get('/__dshwork/automation/'+route)(req,res);return res}
 return {a,definitions,request,close:()=>effects.reverse().forEach(fn=>fn())};
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
