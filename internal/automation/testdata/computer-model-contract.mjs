// Isolated live-test continuation. Network and model responses are replaced at
// the transport boundary; the real rc.5 serializer reads the projected content.
import fs from 'node:fs';import {createRequire} from 'node:module';
const require=createRequire(process.argv[2]);
const {DeepSeekAdapter,resolveAdapterOptions}=require('@deepseek-ai/dsh-llm-deepseek');
const {Context}=require('@deepseek-ai/cordis');
const {LocalAttachmentStore}=require('@deepseek-ai/dsh-attachment-local');
const result=JSON.parse(fs.readFileSync(0,'utf8'));
if(result.isError)throw Error('observation tool failed');
const attachments=new LocalAttachmentStore(new Context(),{dshHome:process.argv[3]});
const connection=resolveAdapterOptions({baseURL:'http://offline.invalid',models:[{id:'test',inputModalities:['text','image']}],streamIdleTimeoutMs:1000});
let payload,requests=0;
globalThis.fetch=async(url,options)=>{requests++;if(String(url)!=='http://offline.invalid/v1/messages')throw Error('unexpected offline endpoint');payload=JSON.parse(options.body);return new Response('event: message_start\ndata: {"type":"message_start","message":{"id":"offline","model":"test","usage":{"input_tokens":1,"output_tokens":0}}}\n\nevent: content_block_start\ndata: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}\n\nevent: content_block_delta\ndata: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"offline-ok"}}\n\nevent: content_block_stop\ndata: {"type":"content_block_stop","index":0}\n\nevent: message_delta\ndata: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}\n\nevent: message_stop\ndata: {"type":"message_stop"}\n\n',{headers:{'content-type':'text/event-stream'}})};
const adapter=new DeepSeekAdapter({options:()=>connection,prepareExtensions:async()=>({fields:{},accept:async()=>{}}),resolveAttachments:()=>attachments,resolveAuth:async()=>({headers:{}}),resolveUserId:()=> 'offline-test',resolveFiles:()=>({ensureUploaded:async()=>{throw Error('offline inline fallback')}})});
for await(const _ of adapter.stream({model:'test',provider:'offline',messages:[{role:'assistant',source:{kind:'model',provider:'offline',model:'test'},content:[{type:'tool-call',id:'owned',name:'my_computer_observe',arguments:'{}'}]},{role:'tool',toolCallId:'owned',name:'my_computer_observe',content:result.content}]})){}
const blocks=payload.messages.flatMap(x=>x.content).filter(x=>x.type==='tool_result').flatMap(x=>x.content);
const text=blocks.filter(x=>x.type==='text').map(x=>x.text).join('\n');
const line=text.split('\n').find(x=>x.startsWith('computer_observation '));
if(!line)throw Error('snapshot contract absent from final model-visible text');
const observation=JSON.parse(line.slice('computer_observation '.length));
if(!observation.pid||!observation.window_id||!observation.snapshot_id||!observation.click_coordinates.includes('screenshot pixels')||!observation.screenshot_width||!observation.screenshot_height)throw Error('incomplete final observation contract');
let action;
if(process.argv[4].startsWith('@point:')){
 const [x,y]=process.argv[4].slice(7).split(',').map(Number);
 if(!Number.isFinite(x)||!Number.isFinite(y)||x<0||y<0||x>=observation.screenshot_width||y>=observation.screenshot_height||observation.elements.length!==0||!text.includes('AUV Windows screenshot'))throw Error('invalid AUV image-local point contract');
 action={x,y};
}else{
 const matches=observation.elements.filter(x=>x.name===process.argv[4]);
 if(matches.length!==1||typeof matches[0].element_index!=='number'||!matches[0].role||!Array.isArray(matches[0].actions))throw Error('next control unavailable/ambiguous in visible contract');
 action={element_index:matches[0].element_index};
}
if(!blocks.some(x=>x.type==='image'))throw Error('image lost in continuation');
console.log(JSON.stringify({arguments:{pid:observation.pid,window_id:observation.window_id,snapshot_id:observation.snapshot_id,...action},observation,offlineTransportRequests:requests,networkCalls:0,modelCalls:0}));
