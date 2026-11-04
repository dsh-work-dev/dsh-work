import brand from '../../frontend/public/branding/dsh-work-mark.png';
import pointerSvg from '../../frontend/public/branding/agent-pointer.svg';
import pointer from '../../frontend/public/branding/agent-pointer.json';

const WORLD = 'dsh-work-action-indicator-v1';
// Serialized into a CDP isolated world. No page text, values or input listeners
// are involved. The closed shadow and aria-hidden host keep snapshots unchanged.
function mount(mark: string, graphic: string, geometry: typeof pointer) {
 const g = globalThis as any;
 if(window.top!==window)return;
 if (g.__dshIndicator) {
  if(g.__dshIndicator.version==='document-position-v2')return;
  g.__dshIndicator.dispose();
 }
 if (!document.documentElement) {
  document.addEventListener('DOMContentLoaded',()=>mount(mark,graphic,geometry),{once:true});
  return;
 }
 let alive = Date.now(), running = false, disposed = false, active = false, positioned = false, x = 24, y = 24;
 const host = document.createElement('div');
 host.setAttribute('aria-hidden','true');
 host.style.cssText='all:initial!important;position:fixed!important;inset:0!important;pointer-events:none!important;z-index:2147483647!important;';
 const shadow=host.attachShadow({mode:'closed'});
 shadow.innerHTML=`<style>*{box-sizing:border-box;pointer-events:none}#cursor{visibility:hidden;position:absolute;width:${geometry.width}px;height:${geometry.height}px}#graphic{position:absolute;left:${-geometry.hotspot.x}px;top:${-geometry.hotspot.y}px;width:${geometry.width}px;height:${geometry.height}px}.running .halo{animation:breathe 900ms ease-in-out infinite}#pulse{position:absolute;width:44px;height:44px;border:2px solid #0a0a0a;box-shadow:0 0 0 2px white;border-radius:50%;display:none}#target{position:absolute;display:none;border:2px solid #0a0a0a;box-shadow:0 0 0 2px white;border-radius:5px;background:#ffffff18}@keyframes breathe{50%{opacity:${geometry.glow.runningOpacityMin}}}@keyframes pulse{from{opacity:1;transform:scale(.3)}to{opacity:0;transform:scale(1.3)}}@media(prefers-reduced-motion:reduce){.running .halo{animation:none}#pulse{animation:none!important}}</style><div id="target"></div><div id="pulse"></div><div id="cursor"><div id="graphic">${graphic}</div></div>`;
 const cursor=shadow.querySelector('#cursor') as HTMLElement, pulse=shadow.querySelector('#pulse') as HTMLElement, target=shadow.querySelector('#target') as HTMLElement;
 const reducedMotion=matchMedia('(prefers-reduced-motion: reduce)');
 let movement:Animation|undefined,motionEpoch=0;
 let prepared:any,prepareSequence=0;
 cursor.style.transform=`translate(${x}px,${y}px)`;
 function moveTo(nextX:number,nextY:number,instant=false,onArrive?:()=>void){
  if(disposed||!active)return;
  const first=!positioned;
  if(!first&&!onArrive&&nextX===x&&nextY===y)return;
  // Reveal the fixed origin only after a real target, then use the normal curve.
  if(first){positioned=true;cursor.style.visibility='visible';}
  const from=getComputedStyle(cursor).transform;
  const current=new DOMMatrixReadOnly(from==='none'?undefined:from);
  const distance=Math.hypot(nextX-current.e,nextY-current.f);
  x=nextX;y=nextY;const epoch=++motionEpoch;
  movement?.cancel();movement=undefined;
  const to=`translate(${x}px,${y}px)`;cursor.style.transform=to;
  if(instant||reducedMotion.matches||distance<1){onArrive?.();return}
  const bow=Math.min(26,distance*.075),controlX=(current.e+x)/2-(y-current.f)/distance*bow,controlY=(current.f+y)/2+(x-current.e)/distance*bow;
  const frames=Array.from({length:9},(_,i)=>{const t=i/8,u=1-t;return{offset:t,transform:`translate(${u*u*current.e+2*u*t*controlX+t*t*x}px,${u*u*current.f+2*u*t*controlY+t*t*y}px)`}});
  movement=cursor.animate(frames,{
   duration:Math.min(prepared?.budget??350,Math.max(150,distance*.4)),
   easing:'cubic-bezier(.2,.75,.25,1)'
  });
  movement.onfinish=()=>{if(disposed||epoch!==motionEpoch)return;movement=undefined;onArrive?.()};
 }
 function cancelMotion(){
  if(disposed)return;
  cancelPrepared();
  const current=new DOMMatrixReadOnly(getComputedStyle(cursor).transform);
  ++motionEpoch;movement?.cancel();movement=undefined;
  x=current.e;y=current.f;cursor.style.transform=`translate(${x}px,${y}px)`;
  target.style.display=pulse.style.display='none';clearTimeout(targetTimer);clearTimeout(pulseTimer);
 }
 function suspend(){cancelMotion();running=false;cursor.classList.remove('running');icon.remove()}
 function reset(){suspend();positioned=false;x=y=24;cursor.style.transform=`translate(${x}px,${y}px)`;cursor.style.visibility='hidden'}
 const onReducedMotion=()=>{if(reducedMotion.matches&&positioned){if(prepared&&!prepared.settled){movement?.finish();return}const destination={x,y};cancelMotion();moveTo(destination.x,destination.y,true)}};
 addEventListener('scroll',cancelMotion,{capture:true,passive:true});
 addEventListener('resize',cancelMotion);
 reducedMotion.addEventListener('change',onReducedMotion);
 addEventListener('pagehide',dispose,{once:true});
 document.documentElement.appendChild(host);
 let targetTimer:any,pulseTimer:any;
 // Site-owned icon nodes are never edited or removed. A separate last icon
 // wins while active; removing it restores the site's latest icon, not a cache.
 const icon=document.createElement('link');icon.rel='icon';icon.type='image/png';
 const canvas=document.createElement('canvas');canvas.width=canvas.height=32;
 const ctx=canvas.getContext('2d')!;let base:HTMLImageElement|undefined, lastSource='',needsResource=false,renderEpoch=0;
 const logo=new Image();logo.src=mark;
 function draw(){if(disposed||!active||!base||!base.complete)return;try{ctx.clearRect(0,0,32,32);ctx.drawImage(base,0,0,32,32);ctx.fillStyle='#0a0a0a';ctx.strokeStyle='white';ctx.lineWidth=1.5;ctx.beginPath();ctx.roundRect(15,15,17,17,5);ctx.fill();ctx.stroke();ctx.drawImage(logo,18,19,11,10);if(running){ctx.fillStyle=Math.floor(Date.now()/400)%2?'#ffffff':'#8d8d8d';ctx.beginPath();ctx.arc(29,17,2,0,Math.PI*2);ctx.fill()}icon.href=canvas.toDataURL();if(document.head&&icon.parentNode!==document.head)document.head.append(icon)}catch{/* Cross-origin taint: retain the site's icon. */}}
 async function refresh(){const links=[...document.querySelectorAll('link[rel~="icon"]')].filter(n=>n!==icon) as HTMLLinkElement[];const source=links.at(-1)?.href||(/^https?:/.test(location.href)?new URL('/favicon.ico',location.href).href:'');if(!source)return;if(source===lastSource){if(base&&icon.parentNode===document.head&&icon!==document.head.lastElementChild)document.head.append(icon);return}lastSource=source;needsResource=false;const epoch=++renderEpoch;const image=new Image();image.crossOrigin='anonymous';image.onload=()=>{if(epoch!==renderEpoch||disposed)return;base=image;draw()};image.onerror=()=>{if(epoch===renderEpoch){base=undefined;needsResource=true;icon.remove()}};image.src=source}
 const observer=new MutationObserver(records=>{if(records.some(r=>r.target!==icon&&!(r.type==='childList'&&[...r.addedNodes,...r.removedNodes].every(n=>n===icon))))void refresh()});observer.observe(document,{subtree:true,childList:true,attributes:true,attributeFilter:['href','rel']});void refresh();logo.onload=draw;
 const timer=setInterval(()=>{if(Date.now()-alive>3000){dispose();return}if(running)draw()},400);
 function dispose(){if(disposed)return;cancelPrepared();disposed=true;++motionEpoch;movement?.cancel();movement=undefined;removeEventListener('scroll',cancelMotion,true);removeEventListener('resize',cancelMotion);removeEventListener('pagehide',dispose);reducedMotion.removeEventListener('change',onReducedMotion);clearInterval(timer);clearTimeout(targetTimer);clearTimeout(pulseTimer);observer.disconnect();host.remove();icon.remove();delete g.__dshIndicator}
 function showPulse(px:number,py:number){
  if(disposed||!active||!positioned)return;
  pulse.style.cssText=`display:block;left:${px-22}px;top:${py-22}px;animation:none`;
  void pulse.offsetWidth;pulse.style.animation='pulse 650ms ease-out';
  clearTimeout(pulseTimer);pulseTimer=setTimeout(()=>pulse.style.display='none',650);
 }
 function updateHint(v:any){
  if(disposed)return;alive=Date.now();
  if(typeof v.active==='boolean'){active=v.active;if(!active){suspend();return}}
  if(!active)return;
  if(typeof v.running==='boolean'){running=v.running;cursor.classList.toggle('running',running);draw()}
  if(prepared&&!prepared.settled)return;
  const positioned=Number.isFinite(v.x)&&Number.isFinite(v.y);
  if(v.click){
   const px=v.pulseX??v.x??x,py=v.pulseY??v.y??y;
   if(positioned&&(v.x!==px||v.y!==py)){
    // Coalesced drag: establish the real press point before following its tail.
    moveTo(px,py,true,()=>showPulse(px,py));moveTo(v.x,v.y);
   }else moveTo(px,py,false,()=>showPulse(px,py));
  }else if(positioned)moveTo(v.x,v.y);
  if(v.rect){
   const r=v.rect;
   if(!v.click&&!positioned)moveTo(Math.max(0,Math.min(innerWidth-(geometry.width-geometry.hotspot.x),r.x+r.width+6)),Math.max(0,Math.min(innerHeight-(geometry.height-geometry.hotspot.y),r.y+8)));
   target.style.cssText=`display:block;left:${r.x-3}px;top:${r.y-3}px;width:${r.width+6}px;height:${r.height+6}px`;
   clearTimeout(targetTimer);targetTimer=setTimeout(()=>target.style.display='none',900);
  }
  if(v.focus){let e:any=document.activeElement;while(e?.shadowRoot?.activeElement)e=e.shadowRoot.activeElement;if(e&&e!==document.body&&!['IFRAME','FRAME'].includes(e.tagName)){const r=e.getBoundingClientRect();updateHint({rect:{x:r.x,y:r.y,width:r.width,height:r.height}})}}
 }
 function hitAt(px:number,py:number){
  let d=document;const frames:any[]=[];let localX=px,localY=py;
  for(let depth=0;depth<16;depth++){
   const hit=d.elementFromPoint(localX,localY) as any;if(!hit)return;
   if(!['IFRAME','FRAME'].includes(hit.tagName))return{hit,frames};
   const child=hit.contentDocument;if(!child)return;const r=hit.getBoundingClientRect();
   frames.push({element:hit,x:r.x,y:r.y,width:r.width,height:r.height});
   localX-=r.x+hit.clientLeft;localY-=r.y+hit.clientTop;d=child;
  }
 }
 function cancelPrepared(){if(prepared){prepared.observer?.disconnect();prepared.settled=true;prepared.resolve({status:'cancelled'});prepared=undefined}}
 function prepareClick(px:number,py:number,budget:number){
  cancelPrepared();const captured=hitAt(px,py);if(!captured)return{status:'unavailable'};
  const element=captured.hit,rect=element.getBoundingClientRect(),scrolls=[window,...captured.frames.map(f=>f.element.contentWindow)].map(view=>({view,x:view.scrollX,y:view.scrollY}));
  let resolve:any;const promise=new Promise(r=>resolve=r);const token=++prepareSequence;
  const state:any={token,budget,resolve,promise,settled:false};prepared=state;
  const observed=element.closest('button,a,input,select,textarea,[role="button"],[role="link"]')||element;
  state.observer=new MutationObserver(()=>cancelMotion());state.observer.observe(observed,{attributes:true,childList:true,characterData:true,subtree:true});
  for(const node of [observed,...captured.frames.map(f=>f.element)])for(let parent=node.parentElement;parent;parent=parent.parentElement)state.observer.observe(parent,{attributes:true});
  moveTo(px,py,false,()=>{
   if(disposed||prepared!==state)return;
   const now=hitAt(px,py),r=element.getBoundingClientRect();
   const same=now?.hit===element&&element.isConnected&&scrolls.every(s=>s.view.scrollX===s.x&&s.view.scrollY===s.y)&&['x','y','width','height'].every(k=>Math.abs(r[k]-rect[k])<.5)&&captured.frames.every(f=>{const n=f.element.getBoundingClientRect();return['x','y','width','height'].every(k=>Math.abs(n[k]-f[k])<.5)});
   state.settled=true;state.resolve({status:same?'ready':'cancelled'});
  });
  return{status:'prepared',token};
 }
 function commitClick(px:number,py:number){cancelPrepared();moveTo(px,py,true);showPulse(px,py)}
 g.__dshIndicator={version:'document-position-v2',dispose,reset,suspend,prepareClick,waitClick(token:number){return prepared?.token===token?prepared.promise:Promise.resolve({status:'cancelled'})},cancelClick(){cancelMotion()},commitClick,resource(){return needsResource?lastSource:undefined},base(source:string,data:string){if(disposed||source!==lastSource)return;const image=new Image();image.onload=()=>{if(disposed||source!==lastSource)return;base=image;needsResource=false;draw()};image.src=data},update:updateHint};
}

const install=`(${mount.toString()})(${JSON.stringify(brand)},${JSON.stringify(pointerSvg)},${JSON.stringify(pointer)})`;
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
