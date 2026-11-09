import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import net from 'node:net';
import {spawn} from 'node:child_process';
import {createRequire} from 'node:module';
import {createHmac} from 'node:crypto';
import {once} from 'node:events';

const dependencies=process.env.DSH_WORK_BROWSER_TEST_DEPENDENCIES;
const frame=value=>{const body=Buffer.from(JSON.stringify(value)),header=Buffer.alloc(4);header.writeUInt32LE(body.length);return Buffer.concat([header,body]);};

// Exercise the actual adapter parser, pending-command promises, owned protocol
// and websocket dispatch. Only SDK process startup is replaced with endpoint
// discovery, so a real CDP client can supply the minimal Playwright sequence.
test('a coalesced route response precedes its following execution-context event', {skip:!dependencies,timeout:10000}, async t=>{
 const root=await fs.mkdtemp(path.join(os.tmpdir(),'dsh-adapter-order-'));
 t.after(()=>fs.rm(root,{recursive:true,force:true}));
 for(const name of ['cdp-adapter.mjs','owned-cef.mjs','refs.mjs','inspect.mjs'])await fs.copyFile(new URL(name,import.meta.url),path.join(root,name));
 // Peer admission is outside this framing regression; use the fixture pipe
 // carrier on every OS, leaving the production parser/dispatch unchanged.
 await fs.writeFile(path.join(root,'native-socket.mjs'),"import {connect} from 'node:net'; export async function openPrivateSocket(config){return connect(config.pipe)}");
 let source=await fs.readFile(new URL('adapter.mjs',import.meta.url),'utf8');
 const start=source.indexOf('  const {Client}='),end=source.indexOf(" }else if(m.kind==='wire')");
 assert.ok(start>0&&end>start);
 source=source.slice(0,start)+`  send({kind:'test-endpoint',endpoint:'ws://127.0.0.1:'+server.address().port+endpointPath});\n`+source.slice(end);
 await fs.writeFile(path.join(root,'adapter.mjs'),source);
 const pipe=process.platform==='win32'?`\\\\.\\pipe\\dsh-order-${process.pid}-${Date.now()}`:path.join(root,'ipc.sock');
 await fs.writeFile(path.join(root,'native-connection.json'),JSON.stringify({pipe}));
 const server=net.createServer();
 server.listen(pipe);await once(server,'listening');
 t.after(()=>server.close());
 const accepted=once(server,'connection');
 const child=spawn(process.execPath,[path.join(root,'adapter.mjs')],{env:{...process.env,DSH_WORK_BRIDGE_TOKEN:'fixture-token'},stdio:'ignore'});
 t.after(async()=>{child.kill();if(child.exitCode===null)await once(child,'exit');});
 const [socket]=await accepted;t.after(()=>socket.destroy());
 const nonce=Buffer.alloc(32,7),mac=role=>createHmac('sha256','fixture-token').update(role).update(nonce).digest();
 let buffer=Buffer.alloc(0),authenticated=false;
 const pending=[],messages=[];
 const next=()=>messages.length?Promise.resolve(messages.shift()):new Promise(resolve=>pending.push(resolve));
 socket.on('data',chunk=>{
  buffer=Buffer.concat([buffer,chunk]);
  if(!authenticated){if(buffer.length<32)return;assert.deepEqual(buffer.subarray(0,32),mac('worker'));buffer=buffer.subarray(32);authenticated=true;socket.write(Buffer.concat([mac('host'),frame({kind:'init',dependencies,output:root,refScope:'fixture',routeProtocol:'owned-cef-v1'})]));}
  while(buffer.length>=4){const n=buffer.readUInt32LE();if(buffer.length<n+4)return;const value=JSON.parse(buffer.subarray(4,n+4));buffer=buffer.subarray(n+4);pending.length?pending.shift()(value):messages.push(value);}
 });
 socket.write(nonce);
 assert.equal((await next()).kind,'adapter-ready');
 socket.write(Buffer.concat([frame({kind:'wire',payload:{method:'tab.created',params:[{id:1,url:'https://fixture.test/'}]}}),frame({kind:'wire',payload:{method:'route.ready',params:[]}})]));
 const endpoint=await next();assert.equal(endpoint.kind,'test-endpoint');
 const {WebSocket}=createRequire(path.join(dependencies,'package.json'))('ws');
 const ws=new WebSocket(endpoint.endpoint);t.after(()=>ws.terminate());await once(ws,'open');
 const seen=[],waiters=[];
 ws.on('message',data=>{const value=JSON.parse(data);seen.push(value);for(const wake of waiters.splice(0))wake();});
 const until=async predicate=>{while(!predicate())await new Promise(resolve=>waiters.push(resolve));};
 ws.send(JSON.stringify({id:1,method:'Target.setAutoAttach',params:{autoAttach:true}}));
 const attach=await next();assert.equal(attach.payload.method,'devtools.attach');
 socket.write(frame({kind:'wire',payload:{id:attach.payload.id,result:{}}}));
 const target=await next();assert.equal(target.payload.params[1],'Target.getTargetInfo');
 socket.write(frame({kind:'wire',payload:{id:target.payload.id,result:{targetInfo:{targetId:'frame',type:'page',url:'https://fixture.test/'}}}}));
 await until(()=>seen.some(v=>v.id===1));
 const attached=seen.find(v=>v.method==='Target.attachedToTarget');
 const sessionId=attached.params.sessionId;
 ws.send(JSON.stringify({id:2,sessionId,method:'Page.getFrameTree'}));
 const tree=await next();assert.equal(tree.payload.params[1],'Page.getFrameTree');
 seen.length=0;
 socket.write(Buffer.concat([
  frame({kind:'wire',payload:{id:tree.payload.id,result:{frameTree:{frame:{id:'frame',url:'https://fixture.test/'}}}}}),
  frame({kind:'wire',payload:{method:'devtools.event',params:[{tabId:1},'Runtime.executionContextCreated',{context:{id:1,name:'',origin:'https://fixture.test',auxData:{frameId:'frame',isDefault:true}}}]}}),
 ]));
 await until(()=>seen.length===2);
 assert.deepEqual(seen.map(v=>v.id===2?'frame-tree-response':v.method),['frame-tree-response','Runtime.executionContextCreated']);
 const closed=once(ws,'close');
 socket.write(Buffer.concat([
  frame({kind:'invalid-test-operation'}),
  frame({kind:'wire',payload:{method:'devtools.event',params:[{tabId:1},'Runtime.executionContextDestroyed',{executionContextId:1}]}}),
 ]));
 const fatal=await next();assert.equal(fatal.kind,'fatal');assert.equal(fatal.error,'BROWSER_ADAPTER_OPERATION_REJECTED');
 await closed;
 assert.equal(seen.some(v=>v.method==='Runtime.executionContextDestroyed'),false,'a queued envelope was dispatched after stop');
});
