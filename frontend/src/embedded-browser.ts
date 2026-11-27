import {EmbeddedBrowserService} from "../bindings/github.com/local/dsh-work/internal/desktopclient";
import {bridgeOrigin} from "../../internal/desktopbridge/origin-policy";
import type {SlotBounds} from "../bindings/github.com/local/dsh-work/internal/embeddedbrowser/models";

/** DSH supplies CSS slot intent; this shell owns the iframe transform and clip. */
export function embeddedBrowserBridge(frame: HTMLIFrameElement, modalIntent: (blocked:boolean)=>Promise<void>) {
 let enabled=false,candidate=false,documentToken="",accepting=false;
 let nativeQueue=Promise.resolve();
 const native=<T>(task:()=>PromiseLike<T>):Promise<T>=>{const result=nativeQueue.then(task);nativeQueue=result.then(()=>{},()=>{});return result};
 const send=(id:string,value:unknown,error="")=>{const src=frame.getAttribute("src"),origin=src?bridgeOrigin(src):undefined;if(origin)frame.contentWindow?.postMessage({version:1,type:"dsh-work/browser-result",id,value,error},origin)};
 const clamp=(raw:unknown)=>{
  const value=raw as Record<string,unknown>;
  if(!value||![value.x,value.y,value.width,value.height].every(n=>typeof n==="number"&&Number.isFinite(n)&&Math.abs(n)<1e6))throw Error("Invalid browser bounds");
  const viewport=frame.getBoundingClientRect();
  const scaleX=frame.clientWidth?viewport.width/frame.clientWidth:1,scaleY=frame.clientHeight?viewport.height/frame.clientHeight:1;
  const x=viewport.left+Math.max(0,value.x as number)*scaleX,y=viewport.top+Math.max(0,value.y as number)*scaleY;
  const right=Math.min(viewport.right,x+Math.max(0,value.width as number)*scaleX),bottom=Math.min(viewport.bottom,y+Math.max(0,value.height as number)*scaleY);
  return {X:x,Y:y,Width:Math.max(0,right-x),Height:Math.max(0,bottom-y)} satisfies SlotBounds;
 };
 return {
  get enabled(){return enabled},
  async retireDocument(token:string){documentToken=token;accepting=false;return native(async()=>{candidate=await EmbeddedBrowserService.BeginDocument(token);return candidate})},
  acceptsDocument(token:unknown){return !candidate||(accepting&&token===documentToken)},
  activateDocument(token:string){if(documentToken===token)accepting=true},
  async overlay(blocked:boolean){if(!enabled)return;const post=()=>{const src=frame.getAttribute("src"),origin=src?bridgeOrigin(src):undefined;if(origin)frame.contentWindow?.postMessage({version:1,type:"dsh-work/browser-blocked",blocked},origin)};if(blocked)post();await native(()=>EmbeddedBrowserService.Overlay(blocked));if(!blocked)post()},
  handle(event:MessageEvent){
   const m=event.data as Record<string,unknown>;
   if(m?.version!==1||m.type!=="dsh-work/browser")return false;
   if(typeof m.id!=="string"||m.id.length>128||typeof m.action!=="string")return true;
   const slot=m.slot,token=documentToken;
   if(!accepting||m.document!==token)return true;
   void (async()=>{
    enabled=await EmbeddedBrowserService.Capability();if(!accepting||documentToken!==token)throw Error("Browser document retired");if(!enabled)throw Error("Embedded browser is unavailable");
    if(m.action==="modal-overlay") {if(typeof m.blocked!=="boolean")throw Error("Invalid modal intent");await modalIntent(m.blocked);return true}
    if(typeof slot!=="string"||slot.length>1024)throw Error("Invalid browser slot");
    switch(m.action){
     case "update":if(typeof m.address!=="string"||m.address.length>8192||typeof m.visible!=="boolean")throw Error("Invalid browser request");return EmbeddedBrowserService.Update(token,slot,m.address,clamp(m.rect),m.visible&&!frame.hidden);
     case "state":return EmbeddedBrowserService.State(token,slot);
     case "select-control":if(typeof m.session!=="string"||m.session.length>1024||typeof m.lease!=="string")throw Error("Invalid browser selection");return EmbeddedBrowserService.SelectControl(token,new URL(frame.src).searchParams.get("generation")||"",m.session,slot,m.lease);
     case "recover":if(typeof m.address!=="string"||m.address.length>8192)throw Error("Invalid browser address");return EmbeddedBrowserService.Recover(token,slot,m.address);
     case "detach":return EmbeddedBrowserService.Detach(token,slot);
     case "action":if(!["back","forward","reload"].includes(String(m.command)))throw Error("Invalid browser action");return EmbeddedBrowserService.Action(token,slot,String(m.command));
     default:throw Error("Unsupported browser request");
    }
   })().then(value=>{if(accepting&&documentToken===token)send(m.id as string,value)},error=>{if(accepting&&documentToken===token)send(m.id as string,undefined,String(error))});
   return true;
  }
 };
}
