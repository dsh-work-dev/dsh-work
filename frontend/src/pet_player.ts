export interface Frame {index:number;durationMs:number;assetId?:string;x?:number;y?:number;width?:number;height?:number}
export interface Playback {key:string;video?:boolean;assets:Record<string,string>;tracks:Record<string,Frame[]>;look:Record<string,Frame>}
export interface Timeline {trackId:string;frameIndex:number;lookTrackId?:string;lookFrameIndex:number;actionTrackId?:string;elapsedMs:number;loop:boolean;repeatCount:number;playbackId:number;reducedMotion:boolean}

export function frameAt(frames:Frame[], elapsed:number, loop:boolean, repeats:number):Frame|undefined {
 const total=frames.reduce((n,f)=>n+f.durationMs,0);
 if(!frames.length || total<=0)return frames[0];
 let offset=Math.max(0,elapsed);
 if(!loop && offset>=total*Math.max(1,repeats))return frames.at(-1);
 offset%=total;
 for(const frame of frames){if(offset<frame.durationMs)return frame;offset-=frame.durationMs;}
 return frames.at(-1);
}

// Canvas is the browser's image decoder/compositor. Asset loading is independent
// of timeline updates; selection epochs prevent late loads restoring an old pet.
export class PetPlayer {
 private plan:Playback|null=null;
 private videos:[HTMLVideoElement,HTMLVideoElement]=[document.createElement("video"),document.createElement("video")];
 private front=0;
 private videoKey="";
 private ready=false;
 private switching=false;
 private fadeAt=0;
 private videoTimer:number|undefined;
 private failed=false;
 private hitCanvas=document.createElement("canvas");
 private lastHit=0;
 private silhouette={left:1,top:1,right:0,bottom:0};
 private images=new Map<string,HTMLImageElement>();
 private epoch=0;
 private timeline:Timeline|null=null;
 private received=0;
 private timer:number|undefined;
 private visible=false;
 private drawn="";
 constructor(private canvas:HTMLCanvasElement, private onBounds:(rect:DOMRect)=>void,private onFailure:()=>void=()=>{}){}
 async load(plan:Playback):Promise<void>{
  const epoch=++this.epoch;this.stop();this.canvas.hidden=true;this.plan=null;this.images.clear();this.resetVideo();this.failed=false;this.silhouette={left:1,top:1,right:0,bottom:0};this.lastHit=0;
  if(plan.video){this.plan=plan;this.start();return;}
  const images=await Promise.all(Object.entries(plan.assets).map(async([id,url])=>{
   const image=new Image();image.src=url;
   let timeout:ReturnType<typeof setTimeout>|undefined;
   try{await Promise.race([image.decode(),new Promise((_,reject)=>{timeout=setTimeout(()=>reject(new Error("Pet image timed out")),8000);})]);}finally{clearTimeout(timeout);}
   return [id,image] as const;
  }));
  if(epoch!==this.epoch)return;
  this.images=new Map(images);this.plan=plan;this.drawn="";this.start();
 }
 update(timeline:Timeline,visible:boolean){this.timeline=timeline;this.received=performance.now();this.visible=visible;this.canvas.hidden=!visible||this.failed;if(visible)this.start();else this.stop();}
 clear(){this.resetVideo();this.epoch++;this.stop();this.plan=null;this.images.clear();this.canvas.hidden=true;this.drawn="";}
 private stop(){for(const v of this.videos)v.pause();if(this.timer!==undefined)cancelAnimationFrame(this.timer);this.timer=undefined;}
 private start(){if(this.timer===undefined && this.visible && this.plan && !this.failed){this.timer=requestAnimationFrame(()=>this.draw());}}
 private resetVideo(){
  if(this.videoTimer!==undefined)clearTimeout(this.videoTimer);this.videoTimer=undefined;
  for(const v of this.videos){v.onloadeddata=null;v.onerror=null;v.pause();v.removeAttribute("src");v.load();}
  this.ready=false;this.switching=false;this.videoKey="";
 }
 private drawVideo(p:Playback,t:Timeline){
  const frame=p.tracks[t.trackId]?.[0];if(!frame?.assetId)return;
  const key=`${t.trackId}:${t.playbackId}:${t.reducedMotion}`;
  if(key!==this.videoKey){
   this.videoKey=key;this.switching=true;
   if(this.videoTimer!==undefined)clearTimeout(this.videoTimer);
   const next=this.ready?1-this.front:this.front,v=this.videos[next],epoch=this.epoch;
   v.pause();v.onloadeddata=null;v.onerror=null;v.muted=true;v.playsInline=true;v.preload="auto";v.loop=t.loop;
   const fail=()=>{if(this.epoch!==epoch||this.videoKey!==key)return;this.failed=true;this.canvas.hidden=true;this.stop();this.onFailure();};
   v.onerror=fail;this.videoTimer=window.setTimeout(fail,8000);
   v.onloadeddata=()=>{
    if(this.epoch!==epoch||this.videoKey!==key)return;
    if(!v.videoWidth||v.videoWidth>4096||v.videoHeight>4096||!Number.isFinite(v.duration)||v.duration>120){fail();return;}
    clearTimeout(this.videoTimer);this.videoTimer=undefined;
    this.front=next;this.ready=true;this.switching=false;this.fadeAt=performance.now();
    v.onloadeddata=null;if(!t.reducedMotion)void v.play().catch(fail);this.start();
   };
   v.src=p.assets[frame.assetId];v.load();
  }
  if(this.ready){
   const v=this.videos[this.front],old=this.videos[1-this.front];
   if(t.reducedMotion)v.pause();else if(v.paused&&!v.ended)void v.play().catch(()=>{});
   this.canvas.width=v.videoWidth;this.canvas.height=v.videoHeight;
   const context=this.canvas.getContext("2d")!,alpha=t.reducedMotion?1:Math.min(1,(performance.now()-this.fadeAt)/160);
   if(alpha<1&&old.readyState>=2){context.globalAlpha=1-alpha;context.drawImage(old,0,0,this.canvas.width,this.canvas.height);}
   context.globalAlpha=alpha;context.drawImage(v,0,0,this.canvas.width,this.canvas.height);context.globalAlpha=1;
   if(alpha>=1)old.pause();this.canvas.hidden=false;
   const box=this.canvas.getBoundingClientRect(),scale=Math.min(box.width/v.videoWidth,box.height/v.videoHeight);
   this.publishBounds(new DOMRect(box.x+(box.width-v.videoWidth*scale)/2,box.y+(box.height-v.videoHeight*scale)/2,v.videoWidth*scale,v.videoHeight*scale));
  }
  if(!t.reducedMotion||this.switching)this.start();
 }
 private publishBounds(box:DOMRect){
  const now=performance.now();
  if(now-this.lastHit>250 || !this.lastHit){
   this.lastHit=now;this.hitCanvas.width=64;this.hitCanvas.height=64;
   const c=this.hitCanvas.getContext("2d",{willReadFrequently:true})!;c.drawImage(this.canvas,0,0,64,64);
   const pixels=c.getImageData(0,0,64,64).data;
   for(let y=0;y<64;y++)for(let x=0;x<64;x++)if(pixels[(y*64+x)*4+3]>24){
    this.silhouette.left=Math.min(this.silhouette.left,x/64);this.silhouette.top=Math.min(this.silhouette.top,y/64);
    this.silhouette.right=Math.max(this.silhouette.right,(x+1)/64);this.silhouette.bottom=Math.max(this.silhouette.bottom,(y+1)/64);
   }
  }
  const s=this.silhouette;if(s.right<=s.left)return;
  this.onBounds(new DOMRect(box.x+box.width*s.left,box.y+box.height*s.top,box.width*(s.right-s.left),box.height*(s.bottom-s.top)));
 }
 private draw(){
  this.timer=undefined;const p=this.plan,t=this.timeline;if(!p||!t||!this.visible)return;
  if(p.video){this.drawVideo(p,t);return;}
  const frames=p.tracks[t.trackId]??[];
  const look=t.lookTrackId===t.trackId && !t.actionTrackId && t.lookFrameIndex>=0;
  const frame=look?p.look[String(t.lookFrameIndex)]??frames.find(f=>f.index===t.frameIndex):frameAt(frames,t.reducedMotion?0:t.elapsedMs+performance.now()-this.received,t.loop,t.repeatCount);
  const image=frame?.assetId?this.images.get(frame.assetId):undefined;
  if(frame&&image){
   const key=JSON.stringify(frame);
   if(key!==this.drawn){
    this.drawn=key;const width=frame.width||image.naturalWidth,height=frame.height||image.naturalHeight;
    this.canvas.width=width;this.canvas.height=height;
    this.canvas.getContext("2d")?.drawImage(image,frame.x??0,frame.y??0,width,height,0,0,width,height);
   }
   this.canvas.hidden=false;const box=this.canvas.getBoundingClientRect(),scale=Math.min(box.width/this.canvas.width,box.height/this.canvas.height);
   this.publishBounds(new DOMRect(box.x+(box.width-this.canvas.width*scale)/2,box.y+(box.height-this.canvas.height*scale)/2,this.canvas.width*scale,this.canvas.height*scale));
  }
  if(!t.reducedMotion)this.start();
 }
}
