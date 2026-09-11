import {PetSettingsService} from "../bindings/github.com/local/dsh-work/internal/desktopclient";
import {mountPetActivity} from "./pet_activity";
import {PetPlayer, type Playback, type Timeline} from "./pet_player";

export function mountPetOverlay() {
 const updateActivity=mountPetActivity();
 const playbackError=document.getElementById("pet-playback-error")!;
 const canvas=document.getElementById("pet-overlay-image") as HTMLCanvasElement;
 const touch=document.getElementById("pet-touch") as HTMLButtonElement;
 const bubble=document.getElementById("pet-activity")!;
 const handle=document.querySelector<HTMLElement>(".pet-drag-handle")!;
 const reduced=matchMedia("(prefers-reduced-motion: reduce)");
 let disposed=false, inFlight=false, loaded="", requested="", timer:number|undefined, epoch=0, bodyRect=new DOMRect();
 const player=new PetPlayer(canvas,rect=>{
  bodyRect=rect;
  Object.assign(touch.style,{left:`${rect.x}px`,top:`${rect.y}px`,width:`${rect.width}px`,height:`${rect.height}px`});
 },()=>{playbackError.hidden=false;touch.hidden=true;});
 async function hitRegions(){
  if(disposed||document.visibilityState!=="visible")return;
  const rectangles:DOMRect[]=[];
  if(!canvas.hidden)rectangles.push(bodyRect);
  if(!bubble.hidden)rectangles.push(bubble.getBoundingClientRect());
  if(!canvas.hidden)rectangles.push(handle.getBoundingClientRect());
  const regions=rectangles.map(r=>{const x=Math.max(0,Math.min(1,r.x/innerWidth)),y=Math.max(0,Math.min(1,r.y/innerHeight));return {x,y,width:Math.max(0,Math.min(1-x,r.width/innerWidth)),height:Math.max(0,Math.min(1-y,r.height/innerHeight))};});
  await PetSettingsService.SetPetHitRegions(regions).catch(()=>{});
 }
 async function refresh(){
  if(disposed||inFlight||document.visibilityState!=="visible")return;
  inFlight=true;const revision=epoch;
  try{
   const state=await PetSettingsService.GetPetPresentation();
   if(disposed||revision!==epoch)return;
   updateActivity(state.activity);
   const visible=state.runtime?.effectiveVisibility==="visible" && !!state.playbackKey;
   touch.hidden=!visible || !playbackError.hidden;
   if(!visible){playbackError.hidden=true;player.clear();loaded="";requested="";return;}
   if(state.playbackKey!==loaded && state.playbackKey!==requested){
    requested=state.playbackKey;playbackError.hidden=true;
    const plan=await PetSettingsService.GetPetPlayback(loaded);
    if(disposed||revision!==epoch)return;
    if(plan && plan.key===state.playbackKey){await player.load(plan as Playback);if(disposed||revision!==epoch)return;loaded=plan.key;}
    requested="";
   }
   if(loaded===state.playbackKey)player.update(state.snapshot as Timeline,visible);
   await hitRegions();
  }catch(error){playbackError.hidden=false;player.clear();loaded="";requested="";touch.hidden=true;console.error("Pet playback unavailable",error);}
  finally{inFlight=false;}
 }
 touch.addEventListener("click",()=>void PetSettingsService.PetGesture("click",0,0).then(refresh).catch(()=>{}));
 let lastPointer=0;
 touch.addEventListener("pointermove",event=>{
  const now=performance.now();if(now-lastPointer<80)return;lastPointer=now;
  void PetSettingsService.PetGesture("pointer.move",event.clientX-bodyRect.x-bodyRect.width/2,event.clientY-bodyRect.y-bodyRect.height/3).catch(()=>{});
 });
 touch.addEventListener("pointerleave",()=>void PetSettingsService.PetGesture("pointer.leave",0,0).catch(()=>{}));
 function syncReduced(){void PetSettingsService.SetPetReducedMotion(reduced.matches).then(refresh).catch(()=>{});}
 function visibility(){epoch++;if(timer!==undefined)clearInterval(timer);timer=undefined;
  if(document.visibilityState==="visible"){timer=window.setInterval(()=>void refresh(),160);void refresh();}
  else{player.clear();loaded="";requested="";touch.hidden=true;}
 }
 const observer=new MutationObserver(()=>void hitRegions());observer.observe(bubble,{attributes:true,attributeFilter:["hidden"]});
 document.addEventListener("visibilitychange",visibility);reduced.addEventListener("change",syncReduced);
 window.addEventListener("unload",()=>{disposed=true;epoch++;player.clear();observer.disconnect();if(timer!==undefined)clearInterval(timer);reduced.removeEventListener("change",syncReduced);document.removeEventListener("visibilitychange",visibility);});
 syncReduced();visibility();
}
