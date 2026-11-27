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
 let overlayRevision=0,layoutRevision=0;
 // Cover can fail after Go has already blocked the window. Unknown must be
 // explicitly cleared through SetOcclusion, never treated as resumed.
 let nativeOverlay:"clear"|"blocked"|"unknown"="clear";
 type OcclusionSource = "shell-menu"|"settings"|"dsh-modal"|"provider-modal";
 const occlusions=new Set<OcclusionSource>();
 let menuBounds:DOMRect|null=null;
 let overlayBarrier=Promise.resolve();
 let coveredPreviews:unknown[]=[];
 let previewAvailable=false;
 const native=<T>(task:()=>PromiseLike<T>):Promise<T>=>{const result=nativeQueue.then(task);nativeQueue=result.then(()=>{},()=>{});return result};
 const applyNativeOverlay=async(blocked:boolean,token:string)=>{
  if(blocked&&nativeOverlay!=="blocked"){
   visibleSlots.forEach((rect,slot)=>coveredSlots.set(slot,rect));
   nativeOverlay="unknown";
   const previews=await EmbeddedBrowserService.SetOcclusion(token,true);
   if(documentToken===token){visibleSlots.clear();coveredPreviews=previews??[];previewAvailable=previews!==null}
   nativeOverlay="blocked";
  } else if(!blocked&&nativeOverlay!=="clear"){
   await EmbeddedBrowserService.SetOcclusion(token,false);
   // Updates sampled while the overlay was present may still be in flight.
   // Require a new measurement before allowing a native view to show again.
   layoutRevision++;
   nativeOverlay="clear";coveredPreviews=[];previewAvailable=false;coveredSlots.clear();
  }
 };
 const send=(id:string,value:unknown,error="",errorCode="")=>{const src=frame.getAttribute("src"),origin=src?bridgeOrigin(src):undefined;if(origin)frame.contentWindow?.postMessage({version:1,type:"dsh-work/browser-result",id,value,error,errorCode},origin)};
 const clamp=(raw:unknown)=>{
  const value=raw as Record<string,unknown>;
  if(!value||![value.x,value.y,value.width,value.height].every(n=>typeof n==="number"&&Number.isFinite(n)&&Math.abs(n)<1e6))throw Error("Invalid browser bounds");
  const viewport=frame.getBoundingClientRect();
  const scaleX=frame.clientWidth?viewport.width/frame.clientWidth:1,scaleY=frame.clientHeight?viewport.height/frame.clientHeight:1;
  const left=viewport.left+(value.x as number)*scaleX;
  const top=viewport.top+(value.y as number)*scaleY;
  const right=Math.min(viewport.right,left+Math.max(0,value.width as number)*scaleX);
  const bottom=Math.min(viewport.bottom,top+Math.max(0,value.height as number)*scaleY);
  const x=Math.min(viewport.right,Math.max(viewport.left,left));
  const y=Math.min(viewport.bottom,Math.max(viewport.top,top));
  return {X:x,Y:y,Width:Math.max(0,right-x),Height:Math.max(0,bottom-y)} satisfies SlotBounds;
 };
 const layoutChanged=(remeasure=false)=>{const src=frame.getAttribute("src"),origin=src?bridgeOrigin(src):undefined;if(accepting&&origin)frame.contentWindow?.postMessage({version:1,type:"dsh-work/browser-layout",document:documentToken,remeasure},origin)};
 let resizeRecheck:ReturnType<typeof setTimeout>|undefined;
 const frameObserver=new ResizeObserver(()=>{
  if(!accepting)return;
  layoutChanged();
  if(resizeRecheck!==undefined)clearTimeout(resizeRecheck);
  // WebView layout can reach the presenter before native window geometry settles.
  // Reconcile the last proposal once without delaying live resize updates.
  resizeRecheck=setTimeout(()=>{
   resizeRecheck=undefined;
   layoutChanged(true);
  },120);
 });
 frameObserver.observe(frame);
 const applyOcclusion=async(revision:number,token:string)=>{
  if(!accepting||documentToken!==token||!enabled)return;
  const blocked=occlusions.size>0;
  await applyNativeOverlay(blocked,token);
  // Only the latest source set can publish the visible state to this document.
  if(!accepting||documentToken!==token||revision!==overlayRevision)return;
  const src=frame.getAttribute("src"),origin=src?bridgeOrigin(src):undefined;
  if(!origin)return;
  if(blocked&&previewAvailable)frame.contentWindow?.postMessage({version:1,type:"dsh-work/browser-previews",document:token,blocked,value:coveredPreviews},origin);
  frame.contentWindow?.postMessage({version:1,type:"dsh-work/browser-blocked",document:token,blocked,layoutRevision},origin);
 };
 const changeSource=(source:OcclusionSource,blocked:boolean)=>{
  const previous=occlusions.has(source);
  if(blocked)occlusions.add(source);else occlusions.delete(source);
  return previous!==blocked||nativeOverlay==="unknown";
 };
 const setOcclusion=(source:OcclusionSource,blocked:boolean)=>{
  if(changeSource(source,blocked)){
   const revision=++overlayRevision,token=documentToken;
   overlayBarrier=native(()=>applyOcclusion(revision,token));
  }
  return overlayBarrier;
 };
 const intersects=(bounds:DOMRect,slot:SlotBounds)=>bounds.left<slot.X+slot.Width&&bounds.right>slot.X&&bounds.top<slot.Y+slot.Height&&bounds.bottom>slot.Y;
 const overlaps=(bounds:DOMRect)=>{
  let hit=false;
  for(const slots of [visibleSlots,coveredSlots])slots.forEach(slot=>{if(intersects(bounds,slot))hit=true});
  return hit;
 };
 return {
  get enabled(){return enabled},
  layoutChanged,
  overlaps,
  async retireDocument(token:string){
   if(resizeRecheck!==undefined){
    clearTimeout(resizeRecheck);
    resizeRecheck=undefined;
   }
   documentToken=token;accepting=false;announcedDocument="";availability=undefined;
   visibleSlots.clear();coveredSlots.clear();coveredPreviews=[];previewAvailable=false;++overlayRevision;
   return native(async()=>{try{candidate=await EmbeddedBrowserService.BeginDocument(token);nativeOverlay="clear";layoutRevision=0;return candidate}finally{enabled=false}});
  },
  acceptsDocument(token:unknown){return !candidate||(accepting&&token===documentToken)},
  activateDocument(token:string){if(documentToken===token)accepting=true},
  menuOverlaps(bounds:DOMRect){
   menuBounds=bounds;
   return native(async()=>{
    // An in-flight native update may acknowledge new bounds before the menu opens.
    if(menuBounds===bounds&&changeSource("shell-menu",overlaps(bounds)))await applyOcclusion(++overlayRevision,documentToken);
   });
  },
  closeMenu(){menuBounds=null;return setOcclusion("shell-menu",false)},
  setOcclusion,
  handle(event:MessageEvent){
   const m=event.data as Record<string,unknown>;
   if(m?.version!==1||m.type!=="dsh-work/browser")return false;
   if(typeof m.id!=="string"||m.id.length>128||typeof m.action!=="string")return true;
   const slot=m.slot,token=documentToken;
   if(!accepting||m.document!==token)return true;
   const execute=async()=>{
    if(!accepting||documentToken!==token)throw Error("Browser document retired");
    if(m.action==="artifact-labels")return artifactLabels();
    const prepare=async()=>{
     if(!accepting||documentToken!==token)throw Error("Browser document retired");
     if(!enabled){await ensureAvailability();if(!accepting||documentToken!==token)throw Error("Browser document retired");enabled=await EmbeddedBrowserService.Capability();if(!accepting||documentToken!==token)throw Error("Browser document retired");if(!enabled)throw Error("Embedded browser is unavailable")}
     if(nativeOverlay==="unknown" || (nativeOverlay==="clear")!== (occlusions.size===0))await applyNativeOverlay(occlusions.size>0,token);
     if(!accepting||documentToken!==token)throw Error("Browser document retired");
     if(announcedDocument!==token){announcedDocument=token;const src=frame.getAttribute("src"),origin=src?bridgeOrigin(src):undefined;if(origin)frame.contentWindow?.postMessage({version:1,type:"dsh-work/browser-blocked",document:token,blocked:occlusions.size>0,layoutRevision},origin)}
    };
    // The native picker can await a person. Serialize admission and occlusion
    // reconciliation, then let visibility and retirement continue while it waits.
    if(m.action==="artifact-open"||m.action==="modal-overlay")await native(prepare);else await prepare();
    if(!accepting||documentToken!==token)throw Error("Browser document retired");
    if(m.action==="modal-overlay") {if(typeof m.blocked!=="boolean")throw Error("Invalid modal intent");await this.setOcclusion("provider-modal",m.blocked);return true}
    if(typeof slot!=="string"||slot.length>1024)throw Error("Invalid browser slot");
    switch(m.action){
     case "update":{
      if(typeof m.address!=="string"||m.address.length>8192||typeof m.visible!=="boolean")throw Error("Invalid browser request");
      const fresh=(m.layoutRevision??0)===layoutRevision;
      if(!fresh&&!m.address)throw Object.assign(Error("Browser layout was superseded"),{code:"CEF_PRESENTATION_LAYOUT_STALE"});
      // Navigation remains a valid intent for this document. A late request
      // can navigate, but only with geometry already admitted for this slot.
      const admitted=visibleSlots.get(slot);
      const rect=fresh?clamp(m.rect):admitted??{X:0,Y:0,Width:0,Height:0};
      const visible=(fresh?m.visible:!!admitted)&&!frame.hidden&&rect.Width>0&&rect.Height>0;
      if(visible&&menuBounds&&intersects(menuBounds,rect)&&changeSource("shell-menu",true)){
       await applyOcclusion(++overlayRevision,token);
       if(!accepting||documentToken!==token)throw Error("Browser document retired");
      }
      try{
       const result=await EmbeddedBrowserService.Update(token,slot,m.address,rect,visible);
       if(accepting&&documentToken===token){
        if(result.visible)visibleSlots.set(slot,rect);else visibleSlots.delete(slot);
        if(nativeOverlay!=="clear"&&fresh){
         if(!visible)coveredSlots.delete(slot);
         else coveredSlots.set(slot,rect);
        }
       }
       return result;
      }catch(error){visibleSlots.delete(slot);if(fresh)coveredSlots.delete(slot);throw error}
     }
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
