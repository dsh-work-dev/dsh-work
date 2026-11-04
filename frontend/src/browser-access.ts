import {AutomationService, ShellService} from "../bindings/github.com/local/dsh-work/internal/desktopclient";
import type {Snapshot} from "../bindings/github.com/local/dsh-work/internal/automation";
import type {BrowserPolicy} from "../bindings/github.com/local/dsh-work/internal/settings";
import {icon} from "./ui/icons";
import {currentLocale,subscribeLocale,t} from "./i18n";

const categories=["payment","send","publish","delete","install","settings","submit","upload","unknown"];
export function mountBrowserAccess(accept:(snapshot:Snapshot)=>void){
 let current:Snapshot|undefined,busy=false,shown="",policyStamp="",expiry:ReturnType<typeof setTimeout>|undefined;
 const root=document.getElementById("automation-browser-policy")!;
 function element<K extends keyof HTMLElementTagNameMap>(tag:K,className=""){const node=document.createElement(tag);node.className=className;return node;}
 function button(key:string,action:()=>unknown){const node=element("button","button");node.type="button";node.dataset.copy=key;node.addEventListener("click",()=>void action());return node;}
 function dialog(id:string){const node=element("dialog");node.id=id;document.body.append(node);return node;}
 const consent=dialog("automation-browser-consent"),heading=element("h2"),body=element("p"),place=element("p","mono"),warning=element("p","note"),error=element("p","result"),actions=element("div","actions");
 const warningText=element("span");warning.append(icon("triangle-alert"),warningText);warning.classList.add("browser-access-warning");
 heading.id="automation-browser-consent-title";consent.setAttribute("aria-labelledby",heading.id);error.setAttribute("role","status");
 const settings=button("access.settings",async()=>{await answer("deny");await ShellService.OpenSettings("browser");});settings.classList.add("button-quiet");
 const deny=button("access.deny",()=>answer("deny")),always=button("access.always",()=>answer("always")),allow=button("access.allow",()=>answer("allow"));allow.classList.add("button-primary");actions.append(settings,deny,always,allow);consent.append(heading,body,place,warning,error,actions);
 consent.addEventListener("cancel",event=>{event.preventDefault();void answer("deny");});
 async function answer(choice:string){if(!shown||busy)return;busy=true;error.textContent="";const id=shown;renderConsent();try{accept(await AutomationService.AnswerBrowserConsent(id,choice));}catch(failure){error.textContent=t(String(failure).includes("REVOKED")||String(failure).includes("CONSENT_INVALID")?"automation.access.expired":"error.saveSettings");try{accept(await AutomationService.GetAutomation());}catch{}}finally{busy=false;disable();renderConsent();}}
 function renderConsent(){
  const request=current?.browserAccess?.nativePrompts?undefined:current?.browserAccess?.requests?.[0];
  if(expiry)clearTimeout(expiry);
  if(!request||Date.parse(request.expires)<=Date.now()){if(consent.open)consent.close();shown="";return;}
  const changed=shown!==request.id;shown=request.id;if(changed)error.textContent="";
  const connecting=request.phase==="connecting";
  heading.textContent=t(connecting?"automation.access.connecting":request.kind==="site"?"automation.access.siteTitle":"automation.access.browserTitle");
  body.textContent=t(request.kind==="site"?"automation.access.siteBody":"automation.access.browserBody",{title:request.title||t("automation.access.conversation")});
  place.textContent=request.kind==="site"?request.site:`${request.browser==="edge"?"Edge":"Chrome"} · ${request.profile}`;
  warningText.textContent=t(request.kind==="site"?"automation.access.siteWarning":"automation.access.warning");
  for(const node of [settings,deny,always,allow])node.disabled=busy;
  deny.textContent=t(connecting?"automation.browser.cancel":"automation.access.deny");always.hidden=connecting||!request.allowAlways;allow.hidden=settings.hidden=connecting;
  for(const node of [settings,always,allow])node.textContent=t(`automation.${node.dataset.copy}`);
  if(!consent.open&&document.visibilityState==="visible"&&document.hasFocus()){consent.showModal();allow.focus();}
  expiry=setTimeout(renderConsent,Math.max(50,Date.parse(request.expires)-Date.now()+5));
 }
 window.addEventListener("focus",renderConsent);document.addEventListener("visibilitychange",renderConsent);

 const status=element("p","note");status.setAttribute("role","status");
 function row(label:string,control:HTMLElement,detail?:string){const node=element("div","row"),main=element("div","row-main"),title=element("label");title.textContent=t(label);title.dataset.i18n=label;if(control.id)title.htmlFor=control.id;main.append(title);if(detail){const text=element("span","setting-detail");text.textContent=t(detail);text.dataset.i18n=detail;main.append(text)}const trail=element("div","row-trail");trail.append(control);node.append(main,trail);return node;}
 function select(id:string,options:string[],key:string){const node=element("select");node.id=id;for(const value of options){const item=element("option");item.value=value;item.textContent=t(`${key}.${value}`);node.append(item)}return node;}
 const ask=element("input");ask.type="checkbox";ask.id="automation-browser-ask-access";
 const newSites=select("automation-browser-new-sites",["allow","ask"],"automation.access.newSites"),duration=select("automation-browser-grant-lasts",["conversation","turn"],"automation.access.lasts");
 const policyControls=new Map<string,HTMLSelectElement>();for(const category of categories)policyControls.set(category,select(`automation-browser-risk-${category}`,category==="payment"?["ask","block"]:["ask","allow","block"],"automation.access.action"));
 const rules=element("div"),add=button("access.addSite",()=>{siteError.textContent="";siteInput.value="";siteMode.value=current?.browserAccess?.policy.newSites==="allow"?"block":"allow";siteDialog.showModal();siteInput.focus();});add.classList.add("button-quiet");
 const siteDialog=dialog("automation-browser-site-rule"),siteTitle=element("h2"),siteInput=element("input"),siteMode=select("automation-browser-site-rule-mode",["allow","block"],"automation.access.rule"),siteError=element("p","result"),siteActions=element("div","actions");siteInput.id="automation-browser-site-rule-host";siteInput.type="text";siteInput.autocomplete="off";siteError.setAttribute("role","status");
 const siteSave=button("access.add",async()=>{if(busy)return;const host=siteInput.value.trim();if(!host){siteError.textContent=t("automation.access.invalidSite");return}const policy=copyPolicy();if(!policy)return;policy.sites=[...(policy.sites??[]),{host,rule:siteMode.value}];if(await save(policy,siteError))siteDialog.close();});
 const siteCancel=button("browser.cancel",()=>siteDialog.close());siteActions.append(siteCancel,siteSave);siteDialog.append(siteTitle,row("automation.access.site",siteInput),row("automation.access.ruleLabel",siteMode),siteError,siteActions);
 const history=dialog("automation-browser-history"),historyTitle=element("h2"),historyList=element("div"),historyClose=button("access.close",()=>history.close());history.append(historyTitle,historyList,historyClose);
 const viewHistory=button("access.view",()=>{renderHistory();history.showModal();});
 function copyPolicy():BrowserPolicy|undefined{return current?.browserAccess?structuredClone(current.browserAccess.policy):undefined;}
 async function save(policy:BrowserPolicy,target=status){busy=true;disable();target.textContent="";try{accept(await AutomationService.SetBrowserPolicy(current?.browserAccess?.policyRevision??0,policy));target.textContent=t("automation.access.saved");return true;}catch(error){const changed=String(error).includes("BROWSER_POLICY_CHANGED");target.textContent=t(changed?"automation.access.policyChanged":target===siteError?String(error).includes("BROWSER_SITE_DUPLICATE")?"automation.access.duplicateSite":"automation.access.invalidSite":"error.saveSettings");if(changed)try{accept(await AutomationService.GetAutomation())}catch{};return false;}finally{busy=false;disable();renderConsent();}}
 ask.addEventListener("change",()=>{const p=copyPolicy();if(p){p.askEachConversation=ask.checked;void save(p)}});
 newSites.addEventListener("change",()=>{const p=copyPolicy();if(p){p.newSites=newSites.value;void save(p)}});
 duration.addEventListener("change",()=>{const p=copyPolicy();if(p){p.grantLasts=duration.value;void save(p)}});
 for(const [category,control] of Array.from(policyControls))control.addEventListener("change",()=>{const p=copyPolicy();if(p){p.confirm={...p.confirm,[category]:control.value};void save(p)}});
 function disable(){for(const node of [ask,newSites,duration,add,siteSave,...Array.from(policyControls.values()),...Array.from(rules.querySelectorAll<HTMLSelectElement|HTMLButtonElement>("select,button"))])node.disabled=busy||!current?.canChange;}
 function section(key:string,rows:HTMLElement[]){const node=element("section","section"),title=element("h2"),group=element("div","group");title.textContent=t(key);group.append(...rows);node.append(title,group);return node;}
 function renderPolicy(){
  const access=current?.browserAccess;root.hidden=!access||!current?.browser.enabled||!current.defaultBrowserEnabled||!current.availableProfile;if(!access)return;
  const stamp=JSON.stringify([access.policy,current?.canChange,current?.browser.enabled,current?.availableProfile,currentLocale()]);if(stamp===policyStamp){disable();return}policyStamp=stamp;
  for(const [node,key] of [[newSites,"newSites"],[duration,"lasts"],[siteMode,"rule"],...Array.from(policyControls.values(),node=>[node,"action"])] as [HTMLSelectElement,string][]){for(const option of Array.from(node.options))option.textContent=t(`automation.access.${key}.${option.value}`);}

  ask.checked=access.policy.askEachConversation;newSites.value=access.policy.newSites;duration.value=access.policy.grantLasts;
  for(const [category,control] of Array.from(policyControls))control.value=access.policy.confirm?.[category]??"ask";
  rules.replaceChildren();
  if(!access.policy.sites?.length){const empty=element("p","note");empty.textContent=t("automation.access.noRules");rules.append(empty)}
  for(const site of access.policy.sites??[]){const control=select("",["allow","block"],"automation.access.rule"),remove=button("access.remove",()=>{const p=copyPolicy();if(p){p.sites=(p.sites??[]).filter(v=>v.host!==site.host);void save(p)}});control.value=site.rule;control.setAttribute("aria-label",site.host);control.addEventListener("change",()=>{const p=copyPolicy();if(p){p.sites=(p.sites??[]).map(v=>v.host===site.host?{...v,rule:control.value}:v);void save(p)}});const node=element("div","row"),host=element("span","row-main mono"),trail=element("div","row-trail");host.textContent=site.host;trail.append(control,remove);node.append(host,trail);rules.append(node)}
  const addRow=element("div","row");addRow.append(add);rules.append(addRow);
  const password=element("div","row"),passwordText=element("div","row-main"),passwordTitle=element("strong"),passwordNote=element("span","setting-detail");passwordTitle.textContent=t("automation.access.secret");passwordNote.textContent=t("automation.access.secretDetail");passwordText.append(passwordTitle,passwordNote);password.append(passwordText);
  root.replaceChildren(section("automation.access.browser",[row("automation.access.ask",ask)]),section("automation.access.sites",[row("automation.access.newSitesLabel",newSites),rules]),section("automation.access.actions",[...Array.from(policyControls,([key,control])=>row(`automation.access.category.${key}`,control)),password]),section("automation.access.access",[row("automation.access.lastsLabel",duration,duration.value==="turn"?"automation.access.turnDetail":"automation.access.idleDetail"),row("automation.access.activity",viewHistory)]),status);
  siteTitle.textContent=t("automation.access.addSite");siteInput.placeholder="example.com";disable();
 }
 function renderHistory(){historyTitle.textContent=t("automation.access.activity");historyList.replaceChildren();for(const item of [...(current?.browserAccess?.activity??[])].reverse()){const line=element("div","row"),text=element("div","row-main");text.textContent=`${new Date(item.at).toLocaleString()} · ${item.title||t("automation.access.conversation")} · ${item.site} · ${t(`automation.access.event.${item.event}`)}`;line.append(text);historyList.append(line)}if(!historyList.childElementCount)historyList.textContent=t("automation.access.noActivity");}

 const disableDialog=dialog("automation-browser-disable"),disableTitle=element("h2"),disableBody=element("p"),disableActions=element("div","actions");let disableDecision:((allowed:boolean)=>void)|undefined;
 const settleDisable=(allowed:boolean)=>{disableDialog.close();disableDecision?.(allowed);disableDecision=undefined;};
 const disableCancel=button("browser.cancel",()=>settleDisable(false)),disableAccept=button("access.turnOff",()=>settleDisable(true));disableAccept.classList.add("button-danger");disableActions.append(disableCancel,disableAccept);disableDialog.append(disableTitle,disableBody,disableActions);
 disableDialog.addEventListener("cancel",event=>{event.preventDefault();settleDisable(false);});
 function confirmDisable(){disableTitle.textContent=t("automation.access.turnOffTitle");disableBody.textContent=t("automation.access.turnOffBody");for(const node of [disableCancel,disableAccept])node.textContent=t(`automation.${node.dataset.copy}`);disableDialog.showModal();disableCancel.focus();return new Promise<boolean>(resolve=>{disableDecision=resolve});}
 const uses=dialog("automation-browser-uses"),usesTitle=element("h2"),usesList=element("div"),usesClose=button("access.close",()=>uses.close());uses.append(usesTitle,usesList,usesClose);
 const activeButton=button("access.using",()=>{renderUses();uses.showModal();});activeButton.id="automation-browser-active";activeButton.className="shell-menu-button";activeButton.hidden=true;document.body.append(activeButton);
 async function stop(session:string,lifetime:string){try{accept(await AutomationService.StopBrowserUse(session,lifetime));status.textContent=t("automation.access.stopped");}catch{status.textContent=t("error.saveSettings");}}
 function renderUses(){usesTitle.textContent=t("automation.access.using");usesList.replaceChildren();for(const use of current?.browserAccess?.sessions??[]){const node=element("div","row"),text=element("div","row-main"),name=element("strong"),site=element("span","setting-detail");name.textContent=use.title||t("automation.access.conversation");site.textContent=use.site;text.append(name,site);const stopButton=button("access.stop",()=>stop(use.session,use.lifetime));stopButton.textContent=t("automation.access.stop");const open=button("access.openConversation",()=>AutomationService.OpenBrowserConversation(use.session));open.textContent=t("automation.access.openConversation");node.append(text,open,stopButton);usesList.append(node)}if(!usesList.childElementCount&&uses.open)uses.close();}
 function render(next:Snapshot){current=next;const bar=document.querySelector(".shell-bar");if(bar&&activeButton.parentElement!==bar)bar.insertBefore(activeButton,bar.querySelector(".shell-drag"));activeButton.hidden=!next.browserAccess?.sessions?.length;activeButton.textContent=t("automation.access.using");renderConsent();renderPolicy();if(uses.open)renderUses();if(history.open)renderHistory();for(const node of Array.from(document.querySelectorAll<HTMLElement>("[data-copy]")))if(node!==deny)node.textContent=t(`automation.${node.dataset.copy}`);}
 subscribeLocale(()=>{if(current)render(current)});
 return {render,confirmDisable};
}
