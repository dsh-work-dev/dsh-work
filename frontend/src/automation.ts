import {Events} from "@wailsio/runtime";
import {AutomationService} from "../bindings/github.com/local/dsh-work/internal/desktopclient";
import type {Snapshot} from "../bindings/github.com/local/dsh-work/internal/automation";
import {subscribeLocale, t} from "./i18n";

type Capability = "browser" | "computer";
export function mountAutomation(changed?: (state: Snapshot)=>void) {
 let current: Snapshot | undefined;
 const busy = new Set<Capability>();
 const capabilities: Capability[] = ["browser", "computer"];
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
   dependency.hidden = !current?.availableProfile || !value?.enabled || c === "browser" && !current.defaultBrowserEnabled;
   retry.hidden = value?.dependency !== "failed";
   retry.disabled = control.disabled;
   detail.textContent = t(c === "computer" && value?.dependency === "prepared" ? "automation.computer.detailPrepared" : `automation.${c}.detail`);
   status.textContent = "";
   if (!current) {status.textContent=t("view.loading");continue;}
   if (!current.availableProfile) continue;
   if (!current.canChange) status.textContent = t("automation.afterStart");
   else if (value?.enabled && value.dependency === "prepared" && (c !== "browser" || current.defaultBrowserEnabled)) status.textContent = t("automation.notConnected");
   const text = document.getElementById(`automation-${c}-dependency-text`)!;
   if (value?.dependency === "preparing") {
    text.textContent = t("automation.preparing", {step:t(`automation.step.${value.step || "download"}`)});
    if (value.totalBytes > 0) text.textContent += ` · ${Math.min(100, Math.round(100 * value.receivedBytes / value.totalBytes))}%`;
   } else if (value?.dependency === "failed") text.textContent = t("automation.failed", {reason:t(`automation.failure.${value.failure || "other"}`)});
   else text.textContent = t(value?.dependency === "prepared" ? "automation.prepared" : "automation.unprepared");
  }
 }
 function accept(next: Snapshot) {if (!current || next.revision >= current.revision) {current=next;render();changed?.(next);}}
 async function refresh() {try {accept(await AutomationService.GetAutomation());} catch {for(const c of capabilities)document.getElementById(`automation-${c}-status`)!.textContent=t("error.loadSettings");}}
 async function update(c:Capability, operation:()=>Promise<Snapshot>) {
  busy.add(c);render();
  let failed=false;
  try {accept(await operation());} catch {failed=true;}
  finally {busy.delete(c);render();if(failed)document.getElementById(`automation-${c}-status`)!.textContent=t("error.saveSettings");}
 }
 for(const c of capabilities) {
  const control=document.getElementById(`automation-${c}-enabled`) as HTMLInputElement;
  control.addEventListener("change",()=>{const enabled=control.checked;void update(c,()=>AutomationService.SetAutomationEnabled(c,enabled));});
  document.getElementById(`automation-${c}-retry`)!.addEventListener("click",()=>void update(c,()=>AutomationService.RetryAutomation(c)));
 }
 const defaultControl=document.getElementById("automation-default-browser-enabled") as HTMLInputElement;
 defaultControl.addEventListener("change",()=>{const enabled=defaultControl.checked;void update("browser",()=>AutomationService.SetDefaultBrowserEnabled(enabled));});
 Events.On("automation",event=>accept(event.data as Snapshot));
 subscribeLocale(render);
 return {refresh};
}
