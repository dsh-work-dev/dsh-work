import {AutomationService,ShellService} from "../bindings/github.com/local/dsh-work/internal/desktopclient";
import type {Snapshot, Component} from "../bindings/github.com/local/dsh-work/internal/automation";
import {t} from "./i18n";

export function componentIdentity(version:string,source?:string){
 if(!source)return version;
 if(!source.startsWith("dsh-work-extension/"))return `${version} · ${source}`;
 const local=source.split(";")[0].split("/").slice(1);
 if(local.length===2 && local[0]===version){const revision=local[1].match(/(?:^|-)(r\d+)$/)?.[1];return `${version} · ${revision||local[1]}`;}
 return `${version} · ${source}`;
}

export function mountAutomationComponents(accept:(snapshot:Snapshot)=>void) {
 let current:Snapshot|undefined;
 let checking=false;
 const busy=new Set<string>();
 const expanded=new Set<string>();
 const errors=new Map<string,string>();
 const engine=document.getElementById("automation-computer-engine") as HTMLSelectElement;
 const node=(tag:string,className="")=>{const el=document.createElement(tag);el.className=className;return el;};
 async function act(component:Component,repair=false){
  busy.add(component.id);errors.delete(component.id);render(current);
  try{accept(await AutomationService.InstallAutomationComponent(component.id,repair));}
  catch(error){errors.set(component.id,t(String(error).includes("UPDATE_BUSY")?"components.busy":"components.failed"));}
  finally{busy.delete(component.id);render(current);}
 }
 async function check(force:boolean){
  if(checking)return;errors.delete("check");checking=true;render(current);
  try{accept(await AutomationService.CheckAutomationComponents(force));}
  catch{errors.set("check",t("components.checkFailed"));}
  finally{checking=false;render(current);}
 }
 engine.addEventListener("change",async()=>{
  const value=engine.value;engine.disabled=true;
  try{accept(await AutomationService.SetComputerEngine(value));errors.delete("engine");}
  catch(error){errors.set("engine",t(String(error).includes("UPDATE_BUSY")?"components.busy":"error.saveSettings"));}
  finally{render(current);}
 });
 for(const capability of ["browser","computer"]){
  document.getElementById(`automation-${capability}-check-components`)!.addEventListener("click",()=>void check(true));
 }
 function render(snapshot?:Snapshot){
  current=snapshot;
  engine.value=snapshot?.computerEngine||"cua-driver";
  document.getElementById("automation-computer-engine-capabilities")!.textContent=t(snapshot?.computerEngine==="auv"?"components.auvCapabilities":"components.cuaCapabilities");
  engine.disabled=!snapshot?.canChange||!!snapshot?.computerAccess?.use||!!snapshot.components?.some(c=>c.capability!=="browser"&&(c.state==="preparing"||busy.has(c.id)));
  document.getElementById("automation-computer-engine-status")!.textContent=errors.get("engine")||(snapshot?.computerAccess?.failure?t("automation.computerAccess.cleanupFailed"):snapshot?.computer.availability==="available"?t("automation.computerAccess.ready"):snapshot?.computer.dependency==="preparing"?t("components.state.preparing"):snapshot?.computer.dependency!=="prepared"?t("components.engineNeedsInstall"):t("components.backendPending"));
  for(const capability of ["browser","computer"]){
   const list=document.getElementById(`automation-${capability}-components`)!;
   const checkButton=document.getElementById(`automation-${capability}-check-components`) as HTMLButtonElement;
   checkButton.disabled=checking||!snapshot;checkButton.textContent=t(checking?"components.checking":"components.check");
   const feedback=document.getElementById(`automation-${capability}-components-status`)!;
   const rows=(snapshot?.components||[]).filter(c=>(c.capability==="browser") === (capability==="browser"));
   feedback.textContent=errors.get("check")|| (rows.some(c=>c.checkFailure)?t("components.checkFailed"):rows.some(c=>c.checkedAt)?t("components.checked"):"");
   list.replaceChildren();
   for(const component of rows){
    const row=node("div","row component-row");row.dataset.component=component.id;
    const main=node("div","row-main"),title=node("strong");title.textContent=component.id==="browser-extension"?t("automation.browser.extension"):component.name;
    const version=node("span","setting-detail");version.textContent=component.installedVersion?t(component.id==="browser-extension"?"components.extensionVersions":"components.versions",{installed:componentIdentity(component.installedVersion,component.source),supported:componentIdentity(component.supportedVersion,component.expectedSource)}):t("components.compatible",{version:componentIdentity(component.supportedVersion,component.expectedSource)});
    const state=node("span","setting-detail");state.setAttribute("role","status");
    state.textContent=errors.get(component.id)||(component.failure?(component.id==="browser-extension"?t(`automation.browser.setup.${component.failure}`):t("automation.failed",{reason:t(`automation.failure.${component.failure}`)})):t(`components.state.${busy.has(component.id)?"preparing":component.state}`));
    if(component.step)state.textContent+=" · "+t(`automation.step.${component.step}`);
    if(component.totalBytes>0)state.textContent+=` · ${Math.min(100,Math.round(component.receivedBytes/component.totalBytes*100))}% (${component.receivedBytes.toLocaleString()} / ${component.totalBytes.toLocaleString()} B)`;
    if(component.loadedVersion)state.textContent+=" · "+t("components.loaded",{version:componentIdentity(component.loadedVersion,component.loadedSource)});
    if(component.unsupportedRelease)state.textContent+=" · "+t("components.appUpdate",{version:component.latestVersion||""});
    main.append(title,version,state);
    const details=document.createElement("details");details.open=expanded.has(component.id);
    const summary=node("summary","setting-detail");summary.textContent=t("components.details");details.append(summary);
    const location=node("p","setting-detail component-location");location.textContent=component.location;details.append(location);
    for(const identity of [component.source,component.loadedSource]){if(identity){const source=node("p","setting-detail component-location");source.textContent=identity;details.append(source);}}
    if(component.adapter){const adapter=node("p","setting-detail");adapter.textContent=t("components.compatibleAdapter",{adapter:component.adapter});details.append(adapter);}
    if(component.managedWithApp){const note=node("p","setting-detail");note.textContent=t("components.withApp");details.append(note);}
    if(component.id==="auv"){const note=node("p","setting-detail");note.textContent=t("components.auvService");details.append(note);}
    if(component.capability!=="browser"){const service=node("p","setting-detail");service.textContent=snapshot?.computerAccess?.use?.engine===(component.id==="cua-driver"?"cua-driver":"auv")?t("automation.computerAccess.using"):t("components.serviceNotStarted");details.append(service);}
    details.addEventListener("toggle",()=>{if(details.open)expanded.add(component.id);else expanded.delete(component.id);});main.append(details);
    const actions=node("div","row-trail");
    const action=component.state==="missing"?"install":component.state==="update"?"update":"repair";
    const button=document.createElement("button");button.className="button";button.type="button";button.textContent=t(`components.${action}`);
    button.disabled=!snapshot?.canChange||busy.has(component.id)||component.state==="preparing";
    button.addEventListener("click",()=>void act(component,action==="repair"));actions.append(button);
    if(component.failure==="toolchain"){const runtimes=document.createElement("button");runtimes.className="button";runtimes.textContent=t("automation.browser.openRuntimes");runtimes.addEventListener("click",()=>void ShellService.OpenSettings("runtimes"));actions.append(runtimes);}
    row.append(main,actions);list.append(row);
   }
  }
 }
 return {render,check};
}
