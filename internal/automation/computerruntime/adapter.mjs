import net from 'node:net';
import path from 'node:path';
import {readFileSync} from 'node:fs';
import {createRequire} from 'node:module';
import {pathToFileURL} from 'node:url';
import {createHmac,timingSafeEqual} from 'node:crypto';
import {isDeepStrictEqual} from 'node:util';
import {createAuvBackend} from './auv.mjs';
const socket=net.connect(process.env.DSH_WORK_BRIDGE_PIPE),token=process.env.DSH_WORK_BRIDGE_TOKEN;
delete process.env.DSH_WORK_BRIDGE_PIPE;delete process.env.DSH_WORK_BRIDGE_TOKEN;
let buffer=Buffer.alloc(0),nonce,authenticated=false,stopping=false,client,transport,schemas,backend;
let serial=Promise.resolve();
const mac=role=>createHmac('sha256',token).update(role).update(nonce).digest();
const send=m=>{const b=Buffer.from(JSON.stringify(m));if(b.length>8*1024*1024)throw Error('COMPUTER_RESULT_TOO_LARGE');const h=Buffer.alloc(4);h.writeUInt32LE(b.length);socket.write(Buffer.concat([h,b]));};
async function stop(){if(stopping)return;stopping=true;try{await backend?.close()}catch{}try{await client?.close()}catch{}try{await transport?.close()}catch{}socket.destroy()}
socket.on('error',()=>void stop());socket.on('close',()=>void stop());socket.setTimeout(3000,()=>socket.destroy());
socket.on('data',chunk=>{
 buffer=Buffer.concat([buffer,chunk]);
 if(!nonce&&buffer.length>=32){nonce=buffer.subarray(0,32);buffer=buffer.subarray(32);socket.write(mac('worker'))}
 if(nonce&&!authenticated&&buffer.length>=32){if(!timingSafeEqual(buffer.subarray(0,32),mac('host'))){socket.destroy();return}buffer=buffer.subarray(32);authenticated=true;socket.setTimeout(0)}
 if(!authenticated)return;
 while(buffer.length>=4){const n=buffer.readUInt32LE();if(!n||n>8*1024*1024){socket.destroy();return}if(buffer.length<n+4)return;const b=buffer.subarray(4,n+4);buffer=buffer.subarray(n+4);try{const m=JSON.parse(b);serial=serial.then(()=>receive(m)).catch(()=>void stop())}catch{socket.destroy();return}}
});
async function receive(m){
 if(stopping)return;
 if(m.kind==='init'){
  try{
   if(client||backend)throw Error('COMPUTER_ENGINE_ALREADY_INITIALIZED');
   if(m.engine==='auv'){backend=await createAuvBackend(m);send({kind:'ready'});return}
   if(m.engine!=='cua-driver')throw Error('COMPUTER_ENGINE_UNAVAILABLE');
   const require=createRequire(path.join(m.dependencies,'package.json'));
   if(JSON.parse(readFileSync(path.join(m.dependencies,'node_modules/@modelcontextprotocol/client/package.json'),'utf8')).version!=='2.2.0')throw Error('COMPUTER_SDK_VERSION_MISMATCH');
   const {Client}=await import(pathToFileURL(require.resolve('@modelcontextprotocol/client')));
   const {StdioClientTransport}=await import(pathToFileURL(require.resolve('@modelcontextprotocol/client/stdio')));
   transport=new StdioClientTransport({command:m.driver,args:['mcp'],cwd:path.dirname(m.driver),env:process.env,stderr:'pipe'});
   client=new Client({name:'DSH-Work-computer',version:'0.1.0'});await client.connect(transport);transport.stderr?.resume();
   if(client.getServerVersion()?.version!=='0.28.1')throw Error('COMPUTER_DRIVER_VERSION_MISMATCH');
   const all=(await client.listTools()).tools;
   for(const expected of m.schemas){const actual=all.find(t=>t.name===expected.name);if(!actual||!isDeepStrictEqual(actual.inputSchema,expected.inputSchema))throw Error('COMPUTER_TOOL_CONTRACT_CHANGED: '+expected.name)}
   schemas=m.schemas;send({kind:'ready'});
  }catch(e){send({kind:'ready',error:e.message});await stop()}
 }else if(m.kind==='call'){
  let dispatched=false;
  try{
   if(backend){dispatched=true;const result=await backend.execute(m.tool,m.arguments);send({kind:'result',id:m.id,result});return}
   const schema=schemas?.find(t=>t.name===m.tool);if(!schema)throw Error('COMPUTER_TOOL_UNAVAILABLE');
   if(!m.arguments||typeof m.arguments!=='object'||Array.isArray(m.arguments)||Object.keys(m.arguments).some(k=>!Object.hasOwn(schema.inputSchema.properties??{},k)))throw Error('COMPUTER_INVALID_ARGUMENT');
   if(Buffer.byteLength(JSON.stringify(m.arguments))>256*1024)throw Error('COMPUTER_INPUT_TOO_LARGE');
   if(m.tool!=='list_windows'&&(!Number.isSafeInteger(m.arguments.pid)||!Number.isSafeInteger(m.arguments.window_id)))throw Error('COMPUTER_EXACT_WINDOW_REQUIRED');
   if(!['list_windows','get_window_state'].includes(m.tool)&&(m.arguments.scope!=='window'||m.arguments.delivery_mode!=='background'||m.arguments.target!==undefined))throw Error('COMPUTER_BACKGROUND_WINDOW_REQUIRED');
   dispatched=true;const result=await client.callTool({name:m.tool,arguments:m.arguments},undefined,{timeout:25000});
   send({kind:'result',id:m.id,result});
  }catch(e){send({kind:'result',id:m.id,error:e.message,fatal:dispatched});if(dispatched)await stop()}
 }else throw Error('COMPUTER_ADAPTER_INVALID_MESSAGE');
}
