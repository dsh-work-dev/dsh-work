import {Events} from "@wailsio/runtime";
import {AutomationService,ShellService} from "../bindings/github.com/local/dsh-work/internal/desktopclient";
import type {Snapshot} from "../bindings/github.com/local/dsh-work/internal/automation";
import {mountAutomationComponents} from "./automation-components";
import {mountComputerAccess} from "./computer-access";
import {mountBrowserAccess} from "./browser-access";
import {browserSetupStage} from "./browser-setup";
import {subscribeLocale, t} from "./i18n";

type Capability = "browser" | "computer";
export function mountAutomation(changed?: (state: Snapshot)=>void) {
 let current: Snapshot | undefined;
 const access=mountBrowserAccess(accept);
 const computerAccess=mountComputerAccess(accept);
 const components=mountAutomationComponents(accept);
 const busy = new Set<Capability>();
 const capabilities: Capability[] = ["browser", "computer"];
 let setupError = "";
 let editingName: {id:string,value:string}|undefined;
 let expiryTimer: ReturnType<typeof setTimeout> | undefined;
 const browserName = (browser: string)=>browser === "edge" ? "Edge" : "Chrome";
 function renderProfiles() {
  const visible=!!current?.availableProfile && !!current.browser.enabled && current.defaultBrowserEnabled;
  for(const id of ["profile-row","extension-row"])document.getElementById(`automation-browser-${id}`)!.hidden=!visible;
  const select=document.getElementById("automation-browser-profile") as HTMLSelectElement;
  const rows=current?.profiles?.bindings ?? [], selected=rows.find(row=>row.id===current?.profiles?.selected);
  const stage=browserSetupStage(current?.profiles);
  const guide=document.getElementById("automation-browser-setup-row")!;
  guide.hidden=!visible||stage==="connected";
  if(stage!=="connected")for(const key of ["title","detail"]){const node=document.getElementById(`automation-browser-setup-${key}`)!;const text=t(`automation.browser.guide.${stage}.${key}`);if(node.textContent!==text)node.textContent=text;}
  document.getElementById("automation-browser-setup-steps")!.hidden=stage!=="install";
  document.getElementById("automation-browser-profile-row")!.hidden=!visible||!rows.length;
  document.getElementById("automation-browser-extension-detail")!.textContent=t(stage==="install"?"automation.browser.guide.extensionInstall":"automation.browser.guide.extensionOther");

  const placeholder=document.createElement("option");placeholder.value="";placeholder.textContent=t("automation.browser.chooseProfile");
  select.replaceChildren(placeholder);
  for(const row of rows){
   const repeated=rows.filter(other=>other.browser===row.browser&&other.name===row.name).length>1;
   const option=document.createElement("option");option.value=row.id;option.textContent=`${browserName(row.browser)} · ${row.name}${repeated?` (${row.id.slice(0,8).toUpperCase()})`:""} — ${t(`automation.browser.connection.${row.status}`)}`;select.append(option);
  }
  select.value=current?.profiles?.selected ?? "";
  select.disabled=!current?.canChange || busy.has("browser") || !rows.length;
  for(const suffix of ["forget","rename","extension-folder","check"])(document.getElementById(`automation-browser-${suffix}`) as HTMLButtonElement).disabled=!current?.canChange||busy.has("browser")||(["forget","rename"].includes(suffix)&&!selected)||(suffix==="extension-folder"&&!current?.components?.find(c=>c.id==="browser-extension")?.installedVersion);
  if(editingName?.id!==selected?.id)editingName=undefined;
  document.getElementById("automation-browser-name-row")!.hidden=!visible||!editingName;
  const nameInput=document.getElementById("automation-browser-name") as HTMLInputElement;
  nameInput.value=editingName?.value??"";nameInput.disabled=busy.has("browser")||!current?.canChange;
  for(const key of ["save","cancel"])(document.getElementById(`automation-browser-name-${key}`) as HTMLButtonElement).disabled=busy.has("browser")||!current?.canChange;
  document.getElementById("automation-browser-launch-row")!.hidden=!visible||!selected;
  document.getElementById("automation-browser-launch-path")!.textContent=selected?.launchDirectory||t("automation.browser.launchUnset");
  for(const key of ["set","clear","open"]){
   const control=document.getElementById(`automation-browser-launch-${key}`) as HTMLButtonElement;
   control.disabled=!current?.canChange||busy.has("browser")||!!current?.profiles?.opening||!selected||(key==="set"?selected.status!=="connected":!selected.launchDirectory)||(key==="open"&&selected?.status!=="offline");
  }
  const candidates=document.getElementById("automation-browser-candidates")!;candidates.hidden=!visible;candidates.replaceChildren();
  if(expiryTimer)clearTimeout(expiryTimer);
  const pending=(current?.profiles?.pending??[]).filter(p=>Date.parse(p.expires)>Date.now());
  for(const p of pending){
   const row=document.createElement("div");row.className="row";
   const main=document.createElement("div");main.className="row-main";
   const title=document.createElement("strong");title.textContent=`${browserName(p.browser)} · ${p.name}`;
   const detail=document.createElement("span");detail.className="setting-detail";detail.textContent=t("automation.browser.confirmBinding")+` · ${p.id.slice(0,8).toUpperCase()}`;main.append(title,detail);
   const trail=document.createElement("div");trail.className="row-trail";
   for(const [key,action] of [["bind",()=>AutomationService.ConfirmBrowserBinding(p.id)],["cancel",()=>AutomationService.CancelBrowserBinding(p.id)]] as const){const button=document.createElement("button");button.className="button";button.type="button";button.textContent=t(`automation.browser.${key}`);button.disabled=!current?.canChange||busy.has("browser");button.addEventListener("click",()=>void setup(action));trail.append(button)}
   row.append(main,trail);candidates.append(row);
  }
  if(pending.length)expiryTimer=setTimeout(()=>void refresh(),Math.max(50,Math.min(...pending.map(p=>Date.parse(p.expires)))-Date.now()+25));
  const discovery=current?.profiles?.discovery;
  const discoveryText=document.getElementById("automation-browser-discovery")!;
  const known=rows.some(row=>row.status==="connected");
  const discoveryKey=discovery&&!discovery.chrome&&!discovery.edge&&!known?"notFound":discovery?.default==="other"?"unsupportedDefault":"";
  discoveryText.hidden=!visible||!discoveryKey;discoveryText.textContent=discoveryKey?t(`automation.browser.${discoveryKey}`):"";
  if(!visible)return;
  const failures=(current?.profiles?.registration??[]).filter(r=>r.failure);
  const status=document.getElementById("automation-browser-status")!;
  if(failures.length)status.textContent=failures.map(r=>`${browserName(r.browser)}: ${t(`automation.browser.setup.${r.failure}`)}`).join(" · ");
  else if(setupError)status.textContent=setupError;
  else if(stage==="update")status.textContent=t("automation.browser.reloadRequired");
  else if(current?.profiles?.opening)status.textContent=t("automation.browser.opening");

  else if(current?.canChange&&current.browser.enabled){
   if(current.browserAccess)status.textContent=selected?.status==="connected"&&current.browser.dependency==="prepared"?t(current.gatewayConnected?"automation.access.ready":"automation.access.reconnecting"):"";
   else status.textContent=t(selected?.status==="connected"?"automation.browser.connectedNoExecution":"automation.browser.noExecution");
  }
 }
 function render() {
  for (const c of capabilities) {
   const control = document.getElementById(`automation-${c}-enabled`) as HTMLInputElement;
   const profile = document.querySelector<HTMLElement>(`[data-automation-profile="${c}"]`)!;
   const row = control.closest<HTMLElement>(".row")!;
   const dependency = document.getElementById(`automation-${c}-dependency`)!;
   const detail = document.getElementById(`automation-${c}-enabled-detail`)!;
   const status = document.getElementById(`automation-${c}-status`)!;
   const retry = document.getElementById(`automation-${c}-retry`) as HTMLButtonElement;
   const value = current?.[c];
   if (c === "browser") {
    const defaultControl=document.getElementById("automation-default-browser-enabled") as HTMLInputElement;
    defaultControl.checked=current?.defaultBrowserEnabled ?? true;
    defaultControl.disabled=!current?.canChange || !value?.enabled || busy.has("browser");
    document.getElementById("automation-default-browser-row")!.hidden=!current?.availableProfile || !value?.enabled;
   }
   control.disabled = !current?.canChange || busy.has(c);
   control.checked = value?.enabled ?? false;
   profile.hidden = !current || current.availableProfile;
   row.hidden = !!current && !current.availableProfile;
   dependency.hidden = true;
   retry.hidden = value?.dependency !== "failed";
   retry.disabled = control.disabled;
   detail.textContent = t(c === "computer" && value?.dependency === "prepared" ? "automation.computer.detailPrepared" : `automation.${c}.detail`);
   status.textContent = "";
   if (!current) {status.textContent=t("view.loading");continue;}
   if (!current.availableProfile) continue;
   if (!current.canChange) status.textContent = t("automation.afterStart");

   const text = document.getElementById(`automation-${c}-dependency-text`)!;
   if (value?.dependency === "preparing") {
    text.textContent = t("automation.preparing", {step:t(`automation.step.${value.step || "download"}`)});
    if (value.totalBytes > 0) text.textContent += ` · ${Math.min(100, Math.round(100 * value.receivedBytes / value.totalBytes))}%`;
   } else if (value?.dependency === "failed") text.textContent = t("automation.failed", {reason:t(`automation.failure.${value.failure || "other"}`)});
   else text.textContent = t(value?.dependency === "prepared" ? "automation.prepared" : "automation.unprepared");
   if(value?.repairFailure)text.textContent += " · "+t("automation.browser.repairRetained",{reason:t(`automation.failure.${value.repairFailure}`)});
  }
  renderProfiles();
  const repair=document.getElementById("automation-browser-repair") as HTMLButtonElement;
 const runtime=document.getElementById("automation-browser-runtime") as HTMLButtonElement;
 runtime.hidden=current?.browser.failure!=="toolchain"&&current?.browser.repairFailure!=="toolchain";
 runtime.disabled=!current?.canChange;
 repair.disabled=!current?.canChange||!current.browser.enabled||current.browser.dependency==="preparing"||busy.has("browser");
 components.render(current);
 if(current){access.render(current);computerAccess.render(current);}
 }
 function accept(next: Snapshot) {if (!current || next.revision >= current.revision) {current=next;render();changed?.(next);}}
 async function refresh() {try {accept(await AutomationService.GetAutomation());} catch {for(const c of capabilities)document.getElementById(`automation-${c}-status`)!.textContent=t("error.loadSettings");}}
 async function update(c:Capability, operation:()=>Promise<Snapshot>) {
  busy.add(c);if(c==="browser")setupError="";render();
  let failed=false;
  try {accept(await operation());} catch {failed=true;}
  finally {busy.delete(c);render();if(failed)document.getElementById(`automation-${c}-status`)!.textContent=t("error.saveSettings");}
 }
 async function setup(operation:()=>Promise<Snapshot|void>) {
  busy.add("browser");setupError="";render();
  try {const result=await operation();if(result)accept(result);}catch(error){
   const message=String(error);
   const key=message.includes("UPDATE_BUSY")?"setup.update-busy":message.includes("DIRECTORY_")?"directoryError":message.includes("NOT_CONNECTED")?"connectTimeout":message.includes("LAUNCH_")?"launchUnavailable":message.includes("PROTOCOL")||message.includes("VERSION")?"protocolError":message.includes("CONFLICT")?"recoverConflict":message.includes("NAME")?"nameError":message.includes("OFFLINE")?"recoverOffline":"setupFailed";
   setupError=t(`automation.browser.${key}`);
  }
  finally{busy.delete("browser");render();}
 }
 for(const c of capabilities) {
  const control=document.getElementById(`automation-${c}-enabled`) as HTMLInputElement;
  control.addEventListener("change",async()=>{const enabled=control.checked;if(c==="browser"&&!enabled&&current?.browserAccess?.sessions?.length&&!(await access.confirmDisable())){render();return}void update(c,()=>AutomationService.SetAutomationEnabled(c,enabled));});
  document.getElementById(`automation-${c}-retry`)!.addEventListener("click",()=>void update(c,()=>AutomationService.RetryAutomation(c)));
 }
 const defaultControl=document.getElementById("automation-default-browser-enabled") as HTMLInputElement;
 defaultControl.addEventListener("change",()=>{const enabled=defaultControl.checked;void update("browser",()=>AutomationService.SetDefaultBrowserEnabled(enabled));});
 (document.getElementById("automation-browser-profile") as HTMLSelectElement).addEventListener("change",e=>{const id=(e.target as HTMLSelectElement).value;void setup(()=>AutomationService.SelectBrowserBinding(id));});
 document.getElementById("automation-browser-runtime")!.addEventListener("click",()=>void ShellService.OpenSettings("runtimes"));
 document.getElementById("automation-browser-repair")!.addEventListener("click",()=>void setup(()=>AutomationService.RepairAutomation("browser")));
 document.getElementById("automation-browser-check")!.addEventListener("click",()=>void setup(()=>AutomationService.PrepareBrowserConnection()));
 document.getElementById("automation-browser-extension-folder")!.addEventListener("click",()=>void setup(()=>AutomationService.OpenBrowserExtensionFolder()));
 document.getElementById("automation-browser-forget")!.addEventListener("click",()=>{const id=current?.profiles?.selected;if(id&&window.confirm(t("automation.browser.forgetConfirm")))void setup(()=>AutomationService.ForgetBrowserBinding(id));});
 document.getElementById("automation-browser-rename")!.addEventListener("click",()=>{const selected=current?.profiles?.bindings?.find(b=>b.id===current?.profiles?.selected);if(selected){editingName={id:selected.id,value:selected.name};renderProfiles();document.getElementById("automation-browser-name")!.focus();}});
 (document.getElementById("automation-browser-name") as HTMLInputElement).addEventListener("input",e=>{if(editingName)editingName.value=(e.target as HTMLInputElement).value;});
 document.getElementById("automation-browser-name-cancel")!.addEventListener("click",()=>{editingName=undefined;renderProfiles();});
 document.getElementById("automation-browser-name-save")!.addEventListener("click",()=>{const edit=editingName;if(edit)void setup(async()=>{const state=await AutomationService.RenameBrowserBinding(edit.id,edit.value.trim());editingName=undefined;return state;});});
 for(const key of ["set","clear","open"]){document.getElementById(`automation-browser-launch-${key}`)!.addEventListener("click",()=>{const id=current?.profiles?.selected;if(id)void setup(()=>key==="set"?AutomationService.ConfigureBrowserLaunch(id):key==="clear"?AutomationService.ClearBrowserLaunch(id):AutomationService.OpenBrowserProfile(id));});}
 Events.On("automation",event=>accept(event.data as Snapshot));
 subscribeLocale(render);
 return {refresh:async()=>{await refresh();void components.check(false);}};
}
