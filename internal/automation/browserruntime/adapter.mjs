// One Go-owned process per top-level session. JS adapts the pinned SDK and V2
// CDP model; Go owns identity, admission, creation, cancellation and disposal.
import net from 'node:net';
import http from 'node:http';
import path from 'node:path';
import fs from 'node:fs';
import {createRequire} from 'node:module';
import {pathToFileURL} from 'node:url';
import {createHmac,timingSafeEqual,randomBytes} from 'node:crypto';
import {ExtensionProtocolV2} from './cdp-adapter.mjs';
import {OwnedCEFProtocolV1} from './owned-cef.mjs';
import {createRefScope} from './refs.mjs';
import {inspectBrowser} from './inspect.mjs';
const socket=net.connect(process.env.DSH_WORK_BRIDGE_PIPE);
const token=process.env.DSH_WORK_BRIDGE_TOKEN;
delete process.env.DSH_WORK_BRIDGE_PIPE;delete process.env.DSH_WORK_BRIDGE_TOKEN;
let buffer=Buffer.alloc(0),nonce,authenticated=false,stopping=false;
let client,transport,server,wss,cdp,model,validator,schemas,require,indicator;
let refs;
const geometry=new Map();
let commandId=0;const commands=new Map();
const mac=role=>createHmac('sha256',token).update(role).update(nonce).digest();
const send=m=>{const b=Buffer.from(JSON.stringify(m));if(b.length>8*1024*1024)throw Error('BROWSER_ADAPTER_MESSAGE_TOO_LARGE');const h=Buffer.alloc(4);h.writeUInt32LE(b.length);socket.write(Buffer.concat([h,b]));};
async function stop(){if(stopping)return;stopping=true;for(const p of commands.values()){clearTimeout(p.timer);p.reject(Error('BROWSER_ROUTE_CLOSED'))}commands.clear();try{await client?.close()}catch{}try{await transport?.close()}catch{}cdp?.terminate();wss?.close();server?.close();socket.destroy();}
socket.on('error',()=>void stop());socket.on('close',()=>void stop());
socket.setTimeout(3000,()=>socket.destroy());
socket.on('data',chunk=>{
 buffer=Buffer.concat([buffer,chunk]);
 if(!nonce&&buffer.length>=32){nonce=buffer.subarray(0,32);buffer=buffer.subarray(32);socket.write(mac('worker'))}
 if(nonce&&!authenticated&&buffer.length>=32){if(!timingSafeEqual(buffer.subarray(0,32),mac('host'))){socket.destroy();return}buffer=buffer.subarray(32);authenticated=true;socket.setTimeout(0)}
 if(!authenticated)return;
 while(buffer.length>=4){const n=buffer.readUInt32LE();if(!n||n>8*1024*1024){socket.destroy();return}if(buffer.length<n+4)return;const b=buffer.subarray(4,n+4);buffer=buffer.subarray(n+4);try{void receive(JSON.parse(b)).catch(e=>{send({kind:'fatal',error:e.message});void stop()})}catch{socket.destroy();return}}
});
const allowed=new Set(['browser_close','browser_resize','browser_console_messages','browser_handle_dialog','browser_emulate_media','browser_file_upload','browser_drop','browser_find','browser_fill_form','browser_press_key','browser_type','browser_navigate','browser_navigate_back','browser_network_requests','browser_network_request','browser_take_screenshot','browser_snapshot','browser_click','browser_drag','browser_hover','browser_select_option','browser_tabs','browser_wait_for']);
async function receive(m){
 if(m.kind==='init'){
  if(model)throw Error('BROWSER_ADAPTER_ALREADY_INITIALIZED');
  refs=createRefScope(m.refScope);
  require=createRequire(path.join(m.dependencies,'package.json'));
  for(const [name,version] of [['@playwright/mcp','0.0.83'],['@modelcontextprotocol/sdk','1.32.2'],['ws','8.22.1']])if(JSON.parse(fs.readFileSync(path.join(m.dependencies,'node_modules',name,'package.json'))).version!==version)throw Error('BROWSER_DEPENDENCY_UPDATE_REQUIRED');
  const {WebSocketServer}=require('ws');
  const command=(method,params,optional=false)=>new Promise((resolve,reject)=>{const id=++commandId;const timer=setTimeout(()=>{commands.delete(id);reject(Error('BROWSER_WIRE_TIMEOUT'));if(!optional){send({kind:'fatal',error:'BROWSER_WIRE_TIMEOUT'});void stop()}},optional?250:20000);commands.set(id,{resolve,reject,timer});try{send({kind:'wire',payload:{id,method,params}})}catch(e){clearTimeout(timer);commands.delete(id);reject(e)}});
  const protocol=m.routeProtocol??'extension-v2';
  if(!['extension-v2','owned-cef-v1'].includes(protocol))throw Error('BROWSER_ROUTE_PROTOCOL_REJECTED');
  model=protocol==='owned-cef-v1'?new OwnedCEFProtocolV1(command):new ExtensionProtocolV2(command);
  indicator=value=>{void command('dsh.indicator',value,true).catch(()=>{});};
  const dispatch=async(method,params,sessionId)=>{
   if(method==='Browser.getVersion')return{protocolVersion:'1.3',product:'Chrome/DSH-Work',userAgent:'DSH-Work/0.2.0'};
   if(method==='Browser.setDownloadBehavior')return{};
   if(method.startsWith('Browser.'))throw Error('BROWSER_UNSCOPED_COMMAND');
   if(method.startsWith('Target.')&&!['Target.setAutoAttach','Target.createTarget','Target.closeTarget','Target.getTargetInfo'].includes(method))throw Error('BROWSER_CROSS_TARGET_REJECTED');
   const result=await model.handleCDPCommand(method,params,sessionId);return result?result.result:model.forwardToExtension(method,params,sessionId);
  };
  const endpointPath='/cdp/'+randomBytes(24).toString('hex');
  server=http.createServer((_req,res)=>res.writeHead(404).end());wss=new WebSocketServer({noServer:true,maxPayload:8*1024*1024});
  server.on('upgrade',(req,s,head)=>{if(req.url!==endpointPath||req.headers.origin||cdp||stopping){s.destroy();return}wss.handleUpgrade(req,s,head,ws=>wss.emit('connection',ws))});
  wss.on('connection',ws=>{cdp=ws;model.connectOverCDP(value=>{if(ws.readyState===1)ws.send(JSON.stringify(value))});ws.on('error',()=>void stop());ws.on('close',()=>void stop());ws.on('message',async data=>{let v;try{v=JSON.parse(data);const result=await dispatch(v.method,v.params,v.sessionId);if(ws.readyState===1)ws.send(JSON.stringify({id:v.id,sessionId:v.sessionId,result}))}catch(e){if(v&&ws.readyState===1)ws.send(JSON.stringify({id:v.id,sessionId:v.sessionId,error:{code:-32000,message:e.message}}))}})});
  await new Promise((resolve,reject)=>{server.once('error',reject);server.listen(0,'127.0.0.1',resolve)});
  // Go may now flush the extension's initialization messages into this model.
  send({kind:'adapter-ready'});
  await model.ready();
  const {Client}=await import(pathToFileURL(require.resolve('@modelcontextprotocol/sdk/client/index.js')));
  const {StdioClientTransport}=await import(pathToFileURL(require.resolve('@modelcontextprotocol/sdk/client/stdio.js')));
  const {AjvJsonSchemaValidator}=await import(pathToFileURL(require.resolve('@modelcontextprotocol/sdk/validation/ajv-provider.js')));
  validator=new AjvJsonSchemaValidator();
  transport=new StdioClientTransport({cwd:m.workspace || process.cwd(),command:process.execPath,args:[path.join(m.dependencies,'node_modules/@playwright/mcp/cli.js'),'--cdp-endpoint',`ws://127.0.0.1:${server.address().port}${endpointPath}`,'--output-dir',m.output,'--timeout-action','10000','--timeout-navigation','15000',...(protocol==='owned-cef-v1'?['--snapshot-mode','none']:[])],env:process.env,stderr:'pipe'});
  client=new Client({name:'DSH-Work-browser',version:'0.2.0'});const connecting=client.connect(transport);transport.stderr?.resume();await connecting;
  const all=(await client.listTools()).tools;
  // SDK result is the contract; never invent schemas/descriptions for tools.
  schemas=all.filter(t=>allowed.has(t.name));send({kind:'ready',schemas,allSchemas:all,pid:transport.pid});
 }else if(m.kind==='wire'){
  if(!model)throw Error('BROWSER_ADAPTER_NOT_INITIALIZED');const v=m.payload;
  if(v.id!==undefined){const p=commands.get(v.id);if(!p)return;commands.delete(v.id);clearTimeout(p.timer);v.error?p.reject(Error(String(v.error))):p.resolve(v.result)}else model.handleExtensionEvent(v.method,v.params);
 }else if(m.kind==='call'){
  if(m.tool==='__dsh_inspect'){geometry.clear();try{send({kind:'result',id:m.id,result:await inspectBrowser(client,refs,m.arguments??{},(target,rect)=>{if(target!==undefined)geometry.set(refs.argumentsForMCP({target}).target,rect)})})}catch(e){send({kind:'result',id:m.id,error:e.message})}return;}
  try{
   const schema=schemas?.find(t=>t.name===m.tool);if(!schema)throw Error('BROWSER_TOOL_UNAVAILABLE');
   const args=m.arguments;if(!args||typeof args!=='object'||Array.isArray(args)||Object.keys(args).some(k=>!Object.hasOwn(schema.inputSchema.properties??{},k)))throw Error('BROWSER_INVALID_ARGUMENT');
   const validated=validator.getValidator(schema.inputSchema)(args);if(!validated.valid)throw Error('BROWSER_INVALID_ARGUMENT: '+validated.errorMessage);
   if(Buffer.byteLength(JSON.stringify(args))>256*1024)throw Error('BROWSER_INPUT_TOO_LARGE: reduce complete UTF-8 JSON arguments to 262144 bytes');
   if(m.tool==='browser_file_upload'||m.tool==='browser_drop') {let total=0;for(const file of args.paths??[]){if(!path.isAbsolute(file))throw Error('BROWSER_UPLOAD_ABSOLUTE_PATH_REQUIRED');const stat=fs.statSync(file);if(!stat.isFile())throw Error('BROWSER_UPLOAD_NOT_FILE');total+=stat.size;}if(total>256*1024)throw Error('BROWSER_UPLOAD_TOO_LARGE: total file size must not exceed 262144 bytes');}
   m.arguments=refs.argumentsForMCP(args);
  }catch(e){send({kind:'result',id:m.id,error:e.message});return}
  try{
   if(['browser_type','browser_click','browser_hover','browser_drag','browser_fill_form','browser_select_option','browser_press_key'].includes(m.tool)){
    const tabs=await client.callTool({name:'browser_tabs',arguments:{action:'list'}},undefined,{timeout:5000});
    const text=tabs.content?.filter(v=>v.type==='text').map(v=>v.text).join('\n')??'';
    const current=/^- (\d+): \(current\)/m.exec(text);
    if(tabs.isError||!current)throw Error('BROWSER_CURRENT_TAB_UNAVAILABLE');
    const selected=await client.callTool({name:'browser_tabs',arguments:{action:'select',index:Number(current[1])}},undefined,{timeout:5000});
    if(selected.isError)throw Error('BROWSER_CURRENT_TAB_UNAVAILABLE');
   }
   // Only Go-admitted calls reach here. Inspections and pending approvals do
   // not animate; the extension receives geometry, never target text/values.
   // Reuse the gateway's mandatory, immediately preceding inspection. Optional
   // display never adds an MCP query or waits ahead of the admitted action.
   const target=m.arguments.target??m.arguments.fields?.[0]?.target??m.arguments.startTarget;
   const rect=['browser_type','browser_fill_form','browser_select_option'].includes(m.tool)?geometry.get(target):undefined;geometry.clear();
   indicator({running:true,rect,action:m.tool});
   let rawResult;
   try{rawResult=await client.callTool({name:m.tool,arguments:m.arguments},undefined,{timeout:25000})}
   finally{indicator({running:false})}
   send({kind:'result',id:m.id,result:refs.resultForAgent(rawResult),rawResult})
  }catch(e){send({kind:'result',id:m.id,error:e.message,fatal:true});void stop()}
 }else throw Error('BROWSER_ADAPTER_OPERATION_REJECTED');
}
