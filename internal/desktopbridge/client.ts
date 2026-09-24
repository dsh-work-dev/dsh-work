import {Stream} from '@wailsio/runtime';
export {Stream};

const nativeFetch = globalThis.fetch.bind(globalThis);
const generation = (globalThis as unknown as {__WORK_GENERATION__:string}).__WORK_GENERATION__;
const chunkSize = 64 * 1024;

export async function workerFetch(input: RequestInfo | URL, init?: RequestInit): Promise<Response> {
  const request = new Request(input, init);
  const url = new URL(request.url);
  if (url.origin !== location.origin || url.pathname.startsWith('/wails/')) return nativeFetch(request);
  const socket = Stream('worker-fetch');
  socket.binaryType = 'arraybuffer';
  let settled = false, finished = false;
  let controller: ReadableStreamDefaultController<Uint8Array> | undefined;
  let uploadAck: (()=>void) | undefined;
  let rejectUpload: ((e:unknown)=>void) | undefined;
  let pullDone: (()=>void) | undefined;
  let uploadReader: ReadableStreamDefaultReader<Uint8Array> | undefined;
  const signal = request.signal;
  const close = () => {
    finished=true; signal.removeEventListener('abort',abort);
    rejectUpload?.(new Error('Worker upload closed')); pullDone?.();
    void uploadReader?.cancel().catch(()=>{}); socket.close();
  };
  const abort = () => fail(signal.reason ?? new DOMException('Aborted','AbortError'));
  let rejectResponse: (e:unknown)=>void;
  const fail = (error:unknown) => {
    if (finished) return;
    if (!settled) rejectResponse(error);
    else { try {controller?.error(error);} catch {} }
    rejectUpload?.(error); pullDone?.(); close();
  };
  const response = new Promise<Response>((resolve,reject) => {
    rejectResponse=reject;
    socket.onclose=()=>{if(!finished)fail(new Error('Worker connection closed'));};
    socket.onerror=()=>fail(new Error('Worker transport failed'));
    socket.onmessage=event=>{
      try {
        const bytes=new Uint8Array(event.data as ArrayBuffer);
        switch(bytes[0]) {
          case 0: {
            if(settled)throw new Error('duplicate headers');
            const meta=JSON.parse(new TextDecoder().decode(bytes.subarray(1)));
            const headers=new Headers();
            for(const [key,values] of Object.entries(meta.headers as Record<string,string[]>))for(const value of values)headers.append(key,value);
            const noBody=request.method==='HEAD'||[204,205,304].includes(meta.status);
            const body=new ReadableStream<Uint8Array>({
              start(c){controller=c;},
              pull(){return new Promise<void>(resolve=>{pullDone=resolve;socket.send(new Uint8Array([4]));});},
              cancel(){close();},
            },{highWaterMark:0});
            settled=true;resolve(new Response(noBody?null:body,{status:meta.status,headers}));
            if(noBody)close();
            break;
          }
          case 1:{controller!.enqueue(bytes.slice(1));const done=pullDone;pullDone=undefined;done?.();break;}
          case 2:controller?.close();pullDone?.();close();break;
          case 3:{const ack=uploadAck;uploadAck=undefined;rejectUpload=undefined;ack?.();break;}
          default:throw new Error('invalid Worker frame');
        }
      }catch(error){fail(error);}
    };
    socket.onopen=()=>{
      const headers:Record<string,string[]>={};request.headers.forEach((v,k)=>headers[k]=[v]);
      socket.send(new TextEncoder().encode(JSON.stringify({generation,url:url.pathname+url.search,method:request.method,headers,hasBody:!!request.body})));
      void (async()=>{
        if(!request.body)return;
        const reader=request.body.getReader();
        uploadReader=reader;
        try{
          for(;;){
            const {done,value}=await reader.read();if(done)break;
            for(let offset=0;offset<value.length;offset+=chunkSize){
              if(finished)throw new Error('Worker upload closed');
              const part=value.subarray(offset,offset+chunkSize),frame=new Uint8Array(part.length+1);frame[0]=1;frame.set(part,1);
              await new Promise<void>((resolve,reject)=>{uploadAck=resolve;rejectUpload=reject;socket.send(frame);});
            }
          }
          if(!finished)socket.send(new Uint8Array([2]));
        }finally{await reader.cancel().catch(()=>{});reader.releaseLock();}
      })().catch(fail);
    };
  });
  signal.addEventListener('abort',abort,{once:true});if(signal.aborted)abort();
  return response;
}

// Install only in the dedicated Worker surface, before the DSH entry modules.
globalThis.fetch = workerFetch;
(globalThis as any).WorkerBridge = {Stream,workerFetch};

(globalThis as any).__DSH_TRANSPORT__ = {
  ownsHost:true,
  // DSH owns Remote stream framing, uplinks and peer admission on its
  // published WebSocket route. The WorkerSocket below carries those bytes.
};

const NativeWebSocket=globalThis.WebSocket;
class WorkerSocket extends EventTarget {
 static readonly CONNECTING=0;static readonly OPEN=1;static readonly CLOSING=2;static readonly CLOSED=3;
 readonly CONNECTING=0;readonly OPEN=1;readonly CLOSING=2;readonly CLOSED=3;
 readyState=0;binaryType:BinaryType='blob';bufferedAmount=0;extensions='';protocol='';url:string;
 onopen:((e:Event)=>unknown)|null=null;onclose:((e:CloseEvent)=>unknown)|null=null;
 onerror:((e:Event)=>unknown)|null=null;onmessage:((e:MessageEvent)=>unknown)|null=null;
 private socket=Stream('worker-websocket');private queue=Promise.resolve();private ack?:()=>void;
 private rejectAck?:(e:Error)=>void;private parts:Uint8Array[]=[];private size=0;private kind=2;private closed:CloseEvent|undefined;
 constructor(raw:string|URL,protocols?:string|string[]){
  super();this.url=new URL(raw,location.href).href;this.socket.binaryType='arraybuffer';
  this.socket.onopen=()=>{const u=new URL(this.url);this.socket.send(new TextEncoder().encode(JSON.stringify({url:u.pathname+u.search,generation,protocols:typeof protocols==='string'?[protocols]:protocols??[]})));};
  this.socket.onmessage=e=>{
   const frame=new Uint8Array(e.data as ArrayBuffer);
   if(frame[0]===0){this.protocol=new TextDecoder().decode(frame.subarray(1));this.readyState=1;this.emit('open',new Event('open'));this.socket.send(new Uint8Array([4]));}
   else if(frame[0]===6){const end=JSON.parse(new TextDecoder().decode(frame.subarray(1)));this.closed=new CloseEvent('close',{code:end.code,reason:end.reason,wasClean:true});this.socket.close();}
   else if(frame[0]===3){const ack=this.ack;this.ack=undefined;this.rejectAck=undefined;ack?.();}
   else if(frame[0]===1||frame[0]===2){this.kind=frame[0];this.parts.push(frame.slice(1));this.size+=frame.length-1;if(this.size>16*1024*1024){this.close();return;}this.socket.send(new Uint8Array([4]));}
   else if(frame[0]===5){const bytes=new Uint8Array(this.size);let offset=0;for(const part of this.parts){bytes.set(part,offset);offset+=part.length;}this.parts=[];this.size=0;const data=this.kind===1?new TextDecoder().decode(bytes):this.binaryType==='arraybuffer'?bytes.buffer:new Blob([bytes]);this.emit('message',new MessageEvent('message',{data,origin:location.origin}));this.socket.send(new Uint8Array([4]));}
  };
  this.socket.onerror=()=>this.emit('error',new Event('error'));
  this.socket.onclose=()=>{this.readyState=3;this.rejectAck?.(new Error('WebSocket closed'));this.emit('close',this.closed??new CloseEvent('close',{code:1006,wasClean:false}));};
 }
 private emit(type:string,event:Event){this.dispatchEvent(event);try{(this as any)['on'+type]?.(event);}catch(error){reportError(error);}}
 send(data:string|ArrayBufferLike|Blob|ArrayBufferView){
  if(this.readyState===0)throw new DOMException('WebSocket is connecting','InvalidStateError');if(this.readyState!==1)return;
  const kind=typeof data==='string'?1:2;
  if(typeof data!=='string'&&!(data instanceof Blob))data=ArrayBuffer.isView(data)?new Uint8Array(data.buffer,data.byteOffset,data.byteLength).slice().buffer:new Uint8Array(data).slice().buffer;
  const size=typeof data==='string'?new TextEncoder().encode(data).length:data instanceof Blob?data.size:data.byteLength;this.bufferedAmount+=size;
  this.queue=this.queue.then(async()=>{
   const bytes=typeof data==='string'?new TextEncoder().encode(data):data instanceof Blob?new Uint8Array(await data.arrayBuffer()):ArrayBuffer.isView(data)?new Uint8Array(data.buffer,data.byteOffset,data.byteLength):new Uint8Array(data);
   for(let offset=0;offset<bytes.length||offset===0;offset+=chunkSize){const part=bytes.subarray(offset,offset+chunkSize),frame=new Uint8Array(part.length+1);frame[0]=kind;frame.set(part,1);await this.write(frame);}
   await this.write(new Uint8Array([5]));this.bufferedAmount-=size;
  }).catch(()=>this.close());
 }
 private write(frame:Uint8Array){return new Promise<void>((resolve,reject)=>{if(this.readyState!==1){reject(new Error('WebSocket closed'));return;}this.ack=resolve;this.rejectAck=reject;this.socket.send(frame);});}
 close(code=1000,reason=''){if(code!==1000&&(code<3000||code>4999))throw new DOMException('Invalid close code','InvalidAccessError');if(new TextEncoder().encode(reason).length>123)throw new DOMException('Close reason too long','SyntaxError');if(this.readyState>=2)return;if(this.readyState===0){this.readyState=2;this.socket.close();return;}this.readyState=2;const data=new TextEncoder().encode(JSON.stringify({code,reason})),frame=new Uint8Array(data.length+1);frame[0]=6;frame.set(data,1);this.socket.send(frame);}
}
globalThis.WebSocket=new Proxy(NativeWebSocket,{construct(target,args){const u=new URL(args[0],location.href);const local=new URL(location.href);if(u.host===local.host)return new WorkerSocket(args[0],args[1]);return Reflect.construct(target,args);}});

function openExternal(raw:string){const u=new URL(raw,location.href);if(!['http:','https:'].includes(u.protocol))return;void workerFetch('/__work/external?url='+encodeURIComponent(u.href),{method:'POST'}).catch(()=>{});}
document.addEventListener('click',event=>{const anchor=(event.target as Element)?.closest?.('a[href]') as HTMLAnchorElement|null;if(!anchor)return;const u=new URL(anchor.href,location.href);if(u.origin!==location.origin){event.preventDefault();openExternal(u.href);}},true);
const nativeOpen=window.open.bind(window);
window.open=((url?:string|URL,target?:string,features?:string)=>{if(url){const u=new URL(url,location.href);if(u.origin!==location.origin){openExternal(u.href);return null;}}return nativeOpen(url,target,features);}) as typeof window.open;
