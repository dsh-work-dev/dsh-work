import {EmbeddedBrowserService} from "../bindings/github.com/local/dsh-work/internal/desktopclient";
import {t, subscribeLocale} from "./i18n";
import {bridgeOrigin} from "../../internal/desktopbridge/origin-policy";
import type {SlotBounds, WebDialog, WebPopup} from "../bindings/github.com/local/dsh-work/internal/embeddedbrowser/models";

/** DSH supplies CSS slot intent; this shell owns the iframe transform and clip. */
export function embeddedBrowserBridge(frame: HTMLIFrameElement) {
 let enabled=false,candidate=false,documentToken="",accepting=false;
 let announcedDocument="";
 let availability:ReturnType<typeof EmbeddedBrowserService.Availability>|undefined;
 const unavailableCopy:Record<string,string>={missing:"browser.availability.install",corrupt:"browser.availability.install",incompatible:"browser.availability.update","unsupported-runtime":"browser.availability.runtime","unsupported-platform":"browser.availability.platform"};
 const ensureAvailability=async()=>{const result=await (availability??=EmbeddedBrowserService.Availability());if(result.state==="ready")return;const error=Error(t(unavailableCopy[result.state]??"browser.availability.generic")) as Error&{code:string};error.code=result.code;throw error};
 const visibleSlots=new Map<string,SlotBounds>();
 const coveredSlots=new Map<string,SlotBounds>();
 type ArtifactLabels = Record<"choose"|"reselect"|"select"|"scope"|"local"|"selected"|"waiting"|"canceled"|"failed"|"recover"|"paused",string>;
 const artifactLabels=():ArtifactLabels=>({choose:t("browser.artifact.choose"),reselect:t("browser.artifact.reselect"),select:t("browser.artifact.select"),scope:t("browser.artifact.scope"),local:t("browser.artifact.local"),selected:t("browser.artifact.selected"),waiting:t("browser.artifact.waiting"),canceled:t("browser.artifact.canceled"),failed:t("browser.artifact.failed"),recover:t("browser.artifact.recover"),paused:t("browser.artifact.paused")});
 subscribeLocale(()=>{const src=frame.getAttribute("src"),origin=src?bridgeOrigin(src):undefined;if(accepting&&origin)frame.contentWindow?.postMessage({version:1,type:"dsh-work/browser-artifact-labels",document:documentToken,value:artifactLabels()},origin)});

 let nativeQueue=Promise.resolve();
 let overlayRevision=0;
 // Cover can fail after Go has already blocked the window. Unknown must be
 // explicitly cleared through Overlay(false), never treated as resumed.
 let nativeOverlay:"clear"|"blocked"|"unknown"="clear";
 type OcclusionSource = "shell-menu"|"settings"|"dsh-modal"|"provider-modal";
 const occlusions=new Set<OcclusionSource>();
 let overlayBarrier=Promise.resolve();
 let coveredPreviews:unknown[]=[];
 let previewAvailable=false;
 const native=<T>(task:()=>PromiseLike<T>):Promise<T>=>{const result=nativeQueue.then(task);nativeQueue=result.then(()=>{},()=>{});return result};
 const applyNativeOverlay=async(blocked:boolean,token:string)=>{
  if(blocked&&nativeOverlay!=="blocked"){
   visibleSlots.forEach((rect,slot)=>coveredSlots.set(slot,rect));
   nativeOverlay="unknown";
   try {const previews=await EmbeddedBrowserService.Cover()??[];if(documentToken===token){coveredPreviews=previews;previewAvailable=true}}
   catch(error){if(!String(error).includes("CEF_PREVIEW_UNAVAILABLE"))throw error;await EmbeddedBrowserService.Overlay(true);previewAvailable=false}
   nativeOverlay="blocked";
  } else if(!blocked&&nativeOverlay!=="clear"){await EmbeddedBrowserService.Overlay(false);nativeOverlay="clear";coveredPreviews=[];previewAvailable=false;coveredSlots.clear()}
 };
 const send=(id:string,value:unknown,error="",errorCode="")=>{const src=frame.getAttribute("src"),origin=src?bridgeOrigin(src):undefined;if(origin)frame.contentWindow?.postMessage({version:1,type:"dsh-work/browser-result",id,value,error,errorCode},origin)};
 const clamp=(raw:unknown)=>{
  const value=raw as Record<string,unknown>;
  if(!value||![value.x,value.y,value.width,value.height].every(n=>typeof n==="number"&&Number.isFinite(n)&&Math.abs(n)<1e6))throw Error("Invalid browser bounds");
  const viewport=frame.getBoundingClientRect();
  const scaleX=frame.clientWidth?viewport.width/frame.clientWidth:1,scaleY=frame.clientHeight?viewport.height/frame.clientHeight:1;
  const x=viewport.left+Math.max(0,value.x as number)*scaleX,y=viewport.top+Math.max(0,value.y as number)*scaleY;
  const right=Math.min(viewport.right,x+Math.max(0,value.width as number)*scaleX),bottom=Math.min(viewport.bottom,y+Math.max(0,value.height as number)*scaleY);
  return {X:x,Y:y,Width:Math.max(0,right-x),Height:Math.max(0,bottom-y)} satisfies SlotBounds;
 };
 const layoutChanged=()=>{const src=frame.getAttribute("src"),origin=src?bridgeOrigin(src):undefined;if(accepting&&origin)frame.contentWindow?.postMessage({version:1,type:"dsh-work/browser-layout"},origin)};
 const frameObserver=new ResizeObserver(layoutChanged);frameObserver.observe(frame);
 const applyOcclusion=async(blocked:boolean)=>{if(!enabled&&(blocked||nativeOverlay==="clear"))return;const revision=++overlayRevision,token=documentToken;const post=(type:string,value?:unknown)=>{if(!accepting||documentToken!==token)return;const src=frame.getAttribute("src"),origin=src?bridgeOrigin(src):undefined;if(origin)frame.contentWindow?.postMessage({version:1,type,document:token,blocked,value},origin)};await native(()=>applyNativeOverlay(blocked,token));
  // Only the latest source set can publish the visible state to this document.
  if(revision===overlayRevision){if(blocked&&previewAvailable)post("dsh-work/browser-previews",coveredPreviews);post("dsh-work/browser-blocked")}
 };
 return {
  get enabled(){return enabled},
  overlaps(bounds:DOMRect){let hit=false;for(const slots of [visibleSlots,coveredSlots])slots.forEach(slot=>{if(bounds.left<slot.X+slot.Width&&bounds.right>slot.X&&bounds.top<slot.Y+slot.Height&&bounds.bottom>slot.Y)hit=true});return hit},
  async retireDocument(token:string){documentToken=token;accepting=false;announcedDocument="";availability=undefined;visibleSlots.clear();coveredSlots.clear();coveredPreviews=[];previewAvailable=false;++overlayRevision;return native(async()=>{try{candidate=await EmbeddedBrowserService.BeginDocument(token);return candidate}finally{enabled=false}})},
  acceptsDocument(token:unknown){return !candidate||(accepting&&token===documentToken)},
  activateDocument(token:string){if(documentToken===token)accepting=true},
  menuOverlaps(bounds:DOMRect){const blocked=this.overlaps(bounds);if(!blocked&&!occlusions.has("shell-menu"))return undefined;return this.setOcclusion("shell-menu",blocked)},
  setOcclusion(source:OcclusionSource,blocked:boolean){const previous=occlusions.has(source);if(blocked)occlusions.add(source);else occlusions.delete(source);if(previous===blocked&&nativeOverlay!=="unknown")return overlayBarrier;overlayBarrier=applyOcclusion(occlusions.size>0);return overlayBarrier},
  handle(event:MessageEvent){
   const m=event.data as Record<string,unknown>;
   if(m?.version!==1||m.type!=="dsh-work/browser")return false;
   if(typeof m.id!=="string"||m.id.length>128||typeof m.action!=="string")return true;
   const slot=m.slot,token=documentToken;
   if(!accepting||m.document!==token)return true;
   const execute=async()=>{
    if(!accepting||documentToken!==token)throw Error("Browser document retired");
    if(m.action==="artifact-labels")return artifactLabels();
    if(!enabled){await ensureAvailability();if(!accepting||documentToken!==token)throw Error("Browser document retired");enabled=await EmbeddedBrowserService.Capability();if(!accepting||documentToken!==token)throw Error("Browser document retired");if(!enabled)throw Error("Embedded browser is unavailable")}
    if(nativeOverlay==="unknown" || (nativeOverlay==="clear")!== (occlusions.size===0))await applyNativeOverlay(occlusions.size>0,token);
    if(!accepting||documentToken!==token)throw Error("Browser document retired");
    if(announcedDocument!==token){announcedDocument=token;const src=frame.getAttribute("src"),origin=src?bridgeOrigin(src):undefined;if(origin)frame.contentWindow?.postMessage({version:1,type:"dsh-work/browser-blocked",document:token,blocked:occlusions.size>0},origin)}
    if(m.action==="modal-overlay") {if(typeof m.blocked!=="boolean")throw Error("Invalid modal intent");await this.setOcclusion("provider-modal",m.blocked);return true}
    if(typeof slot!=="string"||slot.length>1024)throw Error("Invalid browser slot");
    switch(m.action){
     case "update":if(typeof m.address!=="string"||m.address.length>8192||typeof m.visible!=="boolean")throw Error("Invalid browser request");{const rect=clamp(m.rect),visible=m.visible&&!frame.hidden;try{const result=await EmbeddedBrowserService.Update(token,slot,m.address,rect,visible);if(accepting&&documentToken===token){if(result.visible)visibleSlots.set(slot,rect);else visibleSlots.delete(slot);if(nativeOverlay!=="clear"&&(visible||coveredSlots.has(slot))){if(rect.Width>0&&rect.Height>0)coveredSlots.set(slot,rect);else coveredSlots.delete(slot)}}return result}catch(error){visibleSlots.delete(slot);coveredSlots.delete(slot);throw error}}
     case "artifact-open":return EmbeddedBrowserService.OpenArtifact(token,slot);
     case "state":return EmbeddedBrowserService.State(token,slot);
     case "select-control":if(typeof m.session!=="string"||m.session.length>1024||typeof m.lease!=="string")throw Error("Invalid browser selection");return EmbeddedBrowserService.SelectControl(token,new URL(frame.src).searchParams.get("generation")||"",m.session,slot,m.lease);
     case "recover":if(typeof m.address!=="string"||m.address.length>8192)throw Error("Invalid browser address");return EmbeddedBrowserService.Recover(token,slot,m.address);
     case "dialog-suspend":case "dialog-ready":case "dialog-answer":{
      const d=m.dialog as WebDialog;
      if(!d||![d.epoch,d.document,d.view,d.presentation,d.origin,d.kind,d.message,d.defaultPrompt].every(v=>typeof v==="string")||typeof d.truncated!=="boolean"||!["alert","confirm","prompt","beforeunload"].includes(d.kind))throw Error("Invalid webpage dialog");
      if(m.action!=="dialog-answer")return EmbeddedBrowserService.PresentDialog(token,slot,d,m.action==="dialog-ready");
      if(typeof m.accept!=="boolean"||typeof m.text!=="string")throw Error("Invalid webpage answer");
      return EmbeddedBrowserService.AnswerDialog(token,slot,d,m.accept,m.text);
     }
     case "popup":{
      const p=m.popup as WebPopup;
      if(!p||![p.id,p.context,p.frameDocument,p.browser,p.popup,p.frame,p.document,p.view,p.presentation,p.origin,p.sourceURL,p.url].every(v=>typeof v==="string"&&v.length<=8192)||p.id.length>256||p.userGesture!==true||!["suspend","ready","allow","deny"].includes(String(m.command)))throw Error("Invalid popup request");
      return EmbeddedBrowserService.Popup(token,slot,p,String(m.command));
     }
     case "download-cancel":if(typeof m.download!=="string"||m.download.length!==26)throw Error("Invalid download");return EmbeddedBrowserService.CancelDownload(token,slot,m.download);
     case "detach":visibleSlots.delete(slot);coveredSlots.delete(slot);return EmbeddedBrowserService.Detach(token,slot);
     case "action":if(!["back","forward","reload"].includes(String(m.command)))throw Error("Invalid browser action");return EmbeddedBrowserService.Action(token,slot,String(m.command));
     default:throw Error("Unsupported browser request");
    }
   };
   void ((m.action==="modal-overlay"||m.action==="artifact-open")?execute():native(execute)).then(value=>{if(accepting&&documentToken===token)send(m.id as string,value)},error=>{if(accepting&&documentToken===token){const code=(error as {code?:unknown})?.code;send(m.id as string,undefined,typeof code==="string"?(error as Error).message:String(error),typeof code==="string"?code:"")}});
   return true;
  }
 };
}
