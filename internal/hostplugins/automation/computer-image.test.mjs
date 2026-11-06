import test from 'node:test';
import assert from 'node:assert/strict';
import {createRequire} from 'node:module';
import {mkdtemp,readFile,rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {projectComputerTool} from './host.js';

// Opt-in exact installed rc.5 SDK; no model, network or desktop operations.
test('computer MCP image becomes a durable attachment and serializes on the next model step', {skip:!process.env.DSH_WORK_TEST_DSH_ENTRY}, async()=>{
 const require=createRequire(process.env.DSH_WORK_TEST_DSH_ENTRY);
 const {createMcpToolDefinition}=require('@deepseek-ai/dsh-mcp-client');
 const {Context}=require('@deepseek-ai/cordis');
 const {LocalAttachmentStore}=require('@deepseek-ai/dsh-attachment-local');
 const {DeepSeekAdapter,resolveAdapterOptions}=require('@deepseek-ai/dsh-llm-deepseek');
 const home=await mkdtemp(join(tmpdir(),'dsh-computer-images-')),ctx=new Context();
 const attachments=new LocalAttachmentStore(ctx,{dshHome:home});
 const exec={signal:new AbortController().signal,agent:{session:{requestHeader:()=>({config:{provider:'offline',model:'test'}})}}};
 const image=await readFile(process.env.DSH_WORK_TEST_IMAGE);
 let admitted=false,payload,fetches=0;
 const services={attachments,llm:{resolveModelInfo:async()=>({inputModalities:['text','image']})}};
 const tool=projectComputerTool({get:k=>services[k]}, {name:'computer_observe',description:'test',inputSchema:{type:'object'}},async(_args,actual)=>{assert.equal(actual,exec);assert.ok(admitted,'Host approval must precede raw call');return {content:[{type:'text',text:'owned observation'},{type:'image',mimeType:'image/png',data:image.toString('base64')}]};},createMcpToolDefinition);
 const oldFetch=globalThis.fetch;
 try{
  await assert.rejects(tool.execute({},exec),/Host approval/);
  admitted=true;const value=await tool.execute({},exec),fallback=tool.output.render({},value),content=tool.projectContent(exec,{value,content:fallback});
  const block=content.find(x=>x.type==='image');assert.ok(block.attachment.attachmentId);assert.equal(block.data,undefined);
  await attachments.readImage(block.attachment,exec.signal);
  assert.equal(tool.projectContent({...exec},{value,content:fallback}),undefined,'projection cannot migrate to another exec');
  const connection=resolveAdapterOptions({baseURL:'http://offline.invalid',models:[{id:'test',inputModalities:['text','image']}],streamIdleTimeoutMs:1000});
  globalThis.fetch=async(url,options)=>{fetches++;assert.equal(String(url),'http://offline.invalid/v1/messages');payload=JSON.parse(options.body);return new Response('event: message_start\ndata: {"type":"message_start","message":{"id":"offline","model":"test","usage":{"input_tokens":1,"output_tokens":0}}}\n\nevent: content_block_start\ndata: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}\n\nevent: content_block_delta\ndata: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"offline-ok"}}\n\nevent: content_block_stop\ndata: {"type":"content_block_stop","index":0}\n\nevent: message_delta\ndata: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":0}}\n\nevent: message_stop\ndata: {"type":"message_stop"}\n\n',{headers:{'content-type':'text/event-stream'}})};
  const adapter=new DeepSeekAdapter({options:()=>connection,prepareExtensions:async()=>({fields:{},accept:async()=>{}}),resolveAttachments:()=>attachments,resolveAuth:async()=>({headers:{}}),resolveUserId:()=> 'offline-test',resolveFiles:()=>({ensureUploaded:async()=>{throw Error('offline inline fallback')}})});
  for await(const _ of adapter.stream({model:'test',provider:'offline',messages:[{role:'assistant',source:{kind:'model',provider:'offline',model:'test'},content:[{type:'tool-call',id:'owned',name:'my_computer_observe',arguments:'{}'}]},{role:'tool',toolCallId:'owned',name:'my_computer_observe',content}]})){}
  assert.equal(fetches,1);const wire=payload.messages.flatMap(x=>x.content).flatMap(x=>x.content??[]).find(x=>x.type==='image');assert.equal(wire.source.type,'base64');assert.ok(wire.source.data.length>0);
  console.log(JSON.stringify({attachmentId:block.attachment.attachmentId,mediaType:block.attachment.mediaType,wireType:wire.source.type,fetches,networkCalls:0}));
 }finally{globalThis.fetch=oldFetch;await rm(home,{recursive:true,force:true})}
});








