(async()=>{
 const cfg=__MAC_PREVIEW_CONFIG__;
 if(globalThis.__MAC_PREVIEW_SHELL__===cfg.nonce)return;
 if(!globalThis._wails)return;
 globalThis.__MAC_PREVIEW_SHELL__=cfg.nonce;
 const {Stream}=await import('/wails/runtime.js');
 const stream=Stream('mac-preview-consumer');let slot='',held,opened=false;const queue=[];
 const frame=()=>document.querySelector('iframe');
 const origin=()=>{const value=frame()?.src;const result=value&&globalThis.__DSH_WORK_ORIGINS__?.origin(value);return result&&result!=='null'?result:undefined};
 const send=value=>{const data=new TextEncoder().encode(JSON.stringify({...value,slot}));if(opened)stream.send(data);else if(queue.length<16)queue.push(data)};
 addEventListener('message',event=>{
  const expected=origin();if(!expected||event.source!==frame()?.contentWindow||event.origin!==expected)return;
  const m=event.data;if(m?.version!==1)return;
  if(m.type==='dsh-work/browser'&&m.action==='update'&&m.address===cfg.url&&typeof m.slot==='string'){slot=m.slot;if(held){send(held);held=undefined}}
  if(m.type==='dsh-work/mac-preview-report'&&m.nonce===cfg.nonce&&['slot-requested','hidden-requested','reopened-requested','failure'].includes(m.stage)){
   const value={stage:m.stage,nonce:m.nonce,session:m.session,step:m.step,workerOrigin:event.origin,shellOrigin:location.origin};
   if(m.stage==='slot-requested'&&!slot)held=value;else send(value);
  }
 });
 stream.onmessage=event=>{const m=JSON.parse(typeof event.data==='string'?event.data:new TextDecoder().decode(event.data));if(m.nonce!==cfg.nonce||!['hide','show'].includes(m.command))return;const expected=origin();if(expected)frame()?.contentWindow?.postMessage({version:1,type:'dsh-work/mac-preview-command',nonce:cfg.nonce,command:m.command},expected)};
 stream.onopen=()=>{opened=true;send({stage:'shell-ready',nonce:cfg.nonce,shellOrigin:location.origin});for(const data of queue)stream.send(data);queue.length=0};
})();
