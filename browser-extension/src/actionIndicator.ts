import {install} from './actionIndicatorRenderer';
const WORLD = 'dsh-work-action-indicator-v1';
type TabState={script:string,context?:number,loading?:boolean,attempted?:string,pending?:any,updating?:boolean,epoch:number,waiting?:boolean,clickEpoch?:number};
export class ActionIndicator {
 private tabs=new Map<number,TabState>();
 private closed=false;
 // Selected only by Agent-issued CDP targets, not group membership or user focus.
 private current?:number;
 private running=false;
 clickMode=false;
 private cancelledClick=false;
 private timer= setInterval(()=>{for(const id of this.tabs.keys())this.schedule(id,{})},1000);
 private send(tabId:number,method:string,params:any={}) {return chrome.debugger.sendCommand({tabId},method,params) as Promise<any>}
 async attach(tabId:number){
  if(this.closed)return;
  const {identifier}=await this.send(tabId,'Page.addScriptToEvaluateOnNewDocument',{source:install,worldName:WORLD});
  if(this.closed){await this.send(tabId,'Page.removeScriptToEvaluateOnNewDocument',{identifier});return}
  this.tabs.set(tabId,{script:identifier,epoch:0});this.schedule(tabId,{});
 }
 private schedule(tabId:number,value:any){
  const state=this.tabs.get(tabId);if(!state||this.closed)return;
  state.pending={...state.pending,...value,click:state.pending?.click||value.click,focus:state.pending?.focus||value.focus};
  if(state.updating)return;state.updating=true;
  void (async()=>{try{while(state.pending&&!this.closed&&this.tabs.get(tabId)===state){const next=state.pending;state.pending=undefined;await this.update(tabId,next)}}finally{state.updating=false}})();
 }
 private async render(tabId:number,method:string,params:any,timeout=250){
  let timer:ReturnType<typeof setTimeout>|undefined;
  try{return await Promise.race([this.send(tabId,method,params),new Promise<never>((_,reject)=>{timer=setTimeout(()=>reject(Error('INDICATOR_TIMEOUT')),timeout)})])}
  finally{clearTimeout(timer)}
 }
 async update(tabId:number,value:any){
  const state=this.tabs.get(tabId);if(!state||this.closed)return;
  const epoch=state.epoch;
  try{const {frameTree}=await this.render(tabId,'Page.getFrameTree',{});const {executionContextId}=await this.render(tabId,'Page.createIsolatedWorld',{frameId:frameTree.frame.id,worldName:WORLD});
   if(state.context!==executionContextId)state.attempted=undefined;
   state.context=executionContextId;
   if(this.closed||this.tabs.get(tabId)!==state||state.epoch!==epoch)return;
   if(state.waiting){state.pending=undefined;return;}
   if(state.pending){value={...value,...state.pending,click:value.click||state.pending.click,focus:value.focus||state.pending.focus};state.pending=undefined;}
   const evaluation=await this.render(tabId,'Runtime.evaluate',{contextId:state.context,returnByValue:true,expression:`${install};globalThis.__dshIndicator?.update(${JSON.stringify({active:tabId===this.current,running:this.running&&tabId===this.current,...value})});globalThis.__dshIndicator?.resource()`});
   const source=evaluation.result?.value;
   if(tabId===this.current&&typeof source==='string'&&!state.loading&&state.attempted!==source&&/^https?:/.test(source)){
    state.attempted=source;state.loading=true;void this.resource(tabId,state,source,frameTree.frame.id).finally(()=>state.loading=false);
   }
  }catch{/* A navigating or closing document is retried on the next heartbeat. */}
 }
 // CDP loads the existing favicon with no credentials, preserving cross-origin
 // site icons without adding extension host permissions. Only bounded image
 // bytes are returned to the local compositor, never to MCP/Go/the agent.
 private async resource(tabId:number,state:TabState,source:string,frameId:string){
  let stream:string|undefined;
  try{
   const {resource}=await this.send(tabId,'Network.loadNetworkResource',{frameId,url:source,options:{disableCache:false,includeCredentials:false}});
   stream=resource?.stream;if(!resource?.success||!stream)return;
   const type=Object.entries(resource.headers??{}).find(([k])=>k.toLowerCase()==='content-type')?.[1];
   if(typeof type!=='string'||!type.toLowerCase().startsWith('image/'))return;
   const chunks:Uint8Array[]=[];let size=0;
   for(;;){const part=await this.send(tabId,'IO.read',{handle:stream,size:32768});const bytes=part.base64Encoded?Uint8Array.from(atob(part.data),c=>c.charCodeAt(0)):new TextEncoder().encode(part.data);size+=bytes.length;if(size>262144)return;chunks.push(bytes);if(part.eof)break}
   if(this.closed||this.tabs.get(tabId)!==state)return;
   const all=new Uint8Array(size);let offset=0;for(const chunk of chunks){all.set(chunk,offset);offset+=chunk.length}
   let binary='';for(const byte of all)binary+=String.fromCharCode(byte);
   await this.send(tabId,'Runtime.evaluate',{contextId:state.context,expression:'globalThis.__dshIndicator?.base('+JSON.stringify(source)+','+JSON.stringify('data:'+type.split(';')[0]+';base64,'+btoa(binary))+')'});
  }catch{}finally{if(stream)try{await this.send(tabId,'IO.close',{handle:stream})}catch{}}
 }
 private select(tabId:number){
  if(this.current===tabId)return;
  const previous=this.current;this.current=tabId;
  if(previous!==undefined){this.invalidate(previous,false);this.schedule(previous,{})}
  this.schedule(tabId,{});
 }
 state(running:boolean,rect?:any,action?:string){
  this.running=running;
  this.clickMode=running&&action==='browser_click';
  if(running)this.cancelledClick=false;
  const geometry=rect&&['x','y','width','height'].every(key=>typeof rect[key]==='number'&&Number.isFinite(rect[key]))
   ?{x:rect.x,y:rect.y,width:rect.width,height:rect.height}:undefined;
  if(this.current!==undefined)this.schedule(this.current,{...(geometry?{rect:geometry}: {})});
 }
 invalidate(tabId:number,resetDocument=true){
  const state=this.tabs.get(tabId);if(!state)return;
  state.epoch++;state.pending=undefined;state.clickEpoch=undefined;if(state.waiting)this.cancelledClick=true;
  if(state.context)void this.send(tabId,'Runtime.evaluate',{contextId:state.context,expression:resetDocument?'globalThis.__dshIndicator?.reset()':'globalThis.__dshIndicator?.suspend()'}).catch(()=>{});
 }
 async beforeClick(tabId:number,p:any){
  if(this.cancelledClick)throw Error('BROWSER_CLICK_INTERRUPTED');
  const state=this.tabs.get(tabId);
  this.select(tabId);
  if(!state?.context)return; // Optional renderer unavailable before any wait.
  const epoch=state.epoch,deadline=Date.now()+400;state.clickEpoch=epoch;state.waiting=true;state.pending=undefined;
  const current=()=>!this.closed&&this.tabs.get(tabId)===state&&state.epoch===epoch&&!this.cancelledClick;
  const evaluate=(expression:string,awaitPromise=false)=>this.render(tabId,'Runtime.evaluate',{contextId:state.context,expression,awaitPromise,returnByValue:true},Math.max(1,deadline-Date.now()));
  try{
   const setup=await evaluate(`globalThis.__dshIndicator?.update({active:true,running:${this.running}});globalThis.__dshIndicator?.prepareClick(${p.x},${p.y},${Math.min(350,deadline-Date.now()-30)})`);
   if(!current())throw Error('BROWSER_CLICK_INTERRUPTED');
   if(setup.exceptionDetails)throw Error('BROWSER_CLICK_PREPARATION_FAILED');
   const plan=setup.result?.value;
   if(!plan||plan.status==='unavailable')return;
   const arrived=await evaluate(`globalThis.__dshIndicator?.waitClick(${plan.token})`,true);
   if(!current()||arrived.result?.value?.status!=='ready')throw Error('BROWSER_CLICK_INTERRUPTED');
  }catch(error){
   if(!this.closed&&this.tabs.get(tabId)===state)void evaluate('globalThis.__dshIndicator?.cancelClick()').catch(()=>{});
   this.cancelledClick=true;
   throw error;
  }finally{state.waiting=false;}
 }
 committedClick(tabId:number,p:any){
  const state=this.tabs.get(tabId);if(!state?.context||this.closed||state.clickEpoch!==state.epoch||this.current!==tabId)return;
  void this.send(tabId,'Runtime.evaluate',{contextId:state.context,expression:`globalThis.__dshIndicator?.commitClick(${p.x},${p.y})`}).catch(()=>{});
 }
 input(tabId:number,method:string,p:any){
  if(method==='Page.bringToFront'){this.select(tabId);return}
  if(!method.startsWith('Input.'))return;
  this.select(tabId);
  this.schedule(tabId,{...(method==='Input.dispatchMouseEvent'?{x:p.x,y:p.y,click:p.type==='mousePressed',...(p.type==='mousePressed'?{pulseX:p.x,pulseY:p.y}:{})}:{}),focus:method==='Input.insertText'||method==='Input.dispatchKeyEvent'});
 }
 async detach(tabId:number){const state=this.tabs.get(tabId);this.tabs.delete(tabId);if(!state)return;
  const cleanup=async()=>{try{await this.send(tabId,'Page.removeScriptToEvaluateOnNewDocument',{identifier:state.script});if(state.context)await this.send(tabId,'Runtime.evaluate',{contextId:state.context,expression:'globalThis.__dshIndicator?.dispose()'})}catch{/* Detached contexts self-expire within 3.4 seconds. */}};
  // A paused/unresponsive renderer cannot hold physical debugger release.
  let timer:ReturnType<typeof setTimeout>|undefined;
  await Promise.race([cleanup(),new Promise<void>(resolve=>{timer=setTimeout(resolve,250)})]);
  clearTimeout(timer);
 }
 async close(){this.closed=true;clearInterval(this.timer);await Promise.all([...this.tabs.keys()].map(id=>this.detach(id)))}
}
