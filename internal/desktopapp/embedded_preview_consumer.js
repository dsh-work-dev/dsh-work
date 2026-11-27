// Compiled only into the explicit isolated preview consumer candidate.
(async()=>{
 const cfg=__MAC_PREVIEW_CONFIG__;
 if(parent===window||globalThis.__MAC_PREVIEW_CONSUMER__)return;
 globalThis.__MAC_PREVIEW_CONSUMER__=true;
 const shell=location.ancestorOrigins?.[0];
 if(!shell||shell==='null')return;
 const documentToken=decodeURIComponent(location.hash.slice(1).match(/(?:^|&)__dsh_work_doc=([^&]+)/)?.[1]||'');
 let step='provider';
 const report=(stage,extra={})=>parent.postMessage({version:1,type:'dsh-work/mac-preview-report',nonce:cfg.nonce,stage,...extra},shell);
 const until=async(label,fn)=>{step=label;const end=performance.now()+30000;while(performance.now()<end){const v=await fn();if(v)return v;await new Promise(r=>setTimeout(r,100))}throw Error(label)};
 const click=pattern=>{const r=new RegExp(pattern,'i');const e=[...document.querySelectorAll('button,[role="button"]')].find(e=>!e.disabled&&e.getClientRects().length&&r.test(e.getAttribute('aria-label')||e.getAttribute('title')||e.textContent.trim()));if(!e)return false;e.click();return true};
 const addressSelector='input[aria-label="Enter an HTTP(S) address"],input[aria-label="输入 HTTP(S) 地址"]';
 const guideSelector='button[data-sidebar-right-guide-entry="browser"]';
 const expandSelector='button[data-sidebar-right-expand]';
 const hitTested=selector=>[...document.querySelectorAll(selector)].filter(element=>{
  if(element.disabled)return false;
  const rect=element.getBoundingClientRect();
  return rect.width>0&&rect.height>0&&element.contains(document.elementFromPoint(rect.left+rect.width/2,rect.top+rect.height/2));
 });
 const browserSidebarState=()=>{
  const controls=selector=>{
   const elements=document.querySelectorAll(selector),element=elements[0];
   const result={mounted:elements.length,hit:hitTested(selector).length};
   if(!element||result.hit)return result;
   const rect=element.getBoundingClientRect(),style=getComputedStyle(element);
   const hit=document.elementFromPoint(rect.left+rect.width/2,rect.top+rect.height/2);
   result.sample={rect:[rect.x,rect.y,rect.width,rect.height],viewport:[innerWidth,innerHeight],
    visibility:style.visibility,pointerEvents:style.pointerEvents,hit:hit?.tagName,hitRole:hit?.getAttribute('role'),
    panelOpen:!!element.closest('[data-sidebar-right-open]'),dialogs:document.querySelectorAll('[role="dialog"],[role="alertdialog"]').length};
   return result;
  };
  const layout=selector=>{
   const element=document.querySelector(selector);
   if(!element)return undefined;
   const rect=element.getBoundingClientRect();
   return {rect:[rect.x,rect.y,rect.width,rect.height],transform:getComputedStyle(element).transform,
    animations:element.getAnimations().map(animation=>({time:animation.currentTime,state:animation.playState}))};
  };
  return {address:controls(addressSelector),guide:controls(guideSelector),expand:controls(expandSelector),
   focused:document.hasFocus(),visibility:document.visibilityState,panel:layout('[data-sidebar-right-panel]'),dock:layout('[data-dockkit-host="dock"]')};
 };
 const sidebarReopened=()=>{
  const addresses=hitTested(addressSelector);
  if(addresses.length!==1)return false;
  const dock=addresses[0].closest('[data-dockkit-host="dock"]');
  if(!dock?.closest('[data-sidebar-right-panel][data-sidebar-right-open]'))return false;
  return getComputedStyle(dock).transform==='none'&&!dock.getAnimations().some(animation=>
   animation.transitionProperty==='transform'&&animation.playState!=='finished'&&animation.playState!=='idle');
 };
 try{
  await until('provider',()=>globalThis.__DSH_WORK_BROWSER_PROVIDER__?.protocolVersion===1);
  let reached=false;try{void parent.document.body;reached=true}catch{}if(reached)throw Error('origin-isolation');
  let requested=false;
  const session=await until('real-session',()=>{
   const rows=[...document.querySelectorAll('[role="treeitem"][data-row-key^="session:"]')].filter(e=>e.getClientRects().length);
   const selected=rows.find(e=>e.getAttribute('aria-selected')==='true');
   if(selected)return selected.getAttribute('data-row-key');
   if(!requested&&rows.length===1){rows[0].click();requested=true}
   if(!requested)requested=click('^(New session in workspace|在“workspace”中新建会话)$');
   return false;
  });
  const surface=await until('browser-sidebar',()=>{
   if(hitTested(addressSelector).length===1)return 'page';
   if(hitTested(guideSelector).length===1)return 'guide';
   const expand=hitTested(expandSelector);
   if(expand.length===1)expand[0].click();
   return false;
  });
  if(surface==='guide')await until('open-browser',()=>{
   const guide=hitTested(guideSelector);
   if(guide.length!==1)return false;
   guide[0].click();
   return true;
  });
  const address=await until('address-toolbar',()=>{
   const addresses=hitTested(addressSelector);
   return addresses.length===1&&addresses[0].form&&addresses[0];
  });
  Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(address,cfg.url);address.dispatchEvent(new Event('input',{bubbles:true}));address.dispatchEvent(new Event('change',{bubbles:true}));address.form.requestSubmit();
  report('slot-requested',{session});
  const selectedTab=()=>document.querySelector('[data-dockkit-tab][aria-selected="true"]')?.dataset.dockkitTab;
  let browserTab;
  let overlayExpected,providerRequestID,layoutRevision=0,staleRequestID;
  addEventListener('message',event=>{
   const message=event.data;
   if(event.source!==parent||event.origin!==shell||message?.version!==1)return;
   if(message.type==='dsh-work/browser-blocked'&&message.document===documentToken&&overlayExpected&&message.blocked===overlayExpected.blocked){
    layoutRevision=message.layoutRevision;
    report(overlayExpected.stage);overlayExpected=undefined;return;
   }
   if(message.type==='dsh-work/browser-result'&&providerRequestID&&message.id===providerRequestID){
    const opened=providerRequestID.endsWith('-open');
    providerRequestID=undefined;
    if(message.error||message.value!==true)report('failure',{step:'provider-overlay-request'});
    else if(opened)report('provider-overlay-opened');
    return;
   }
   if(message.type==='dsh-work/browser-result'&&staleRequestID&&message.id===staleRequestID){
    staleRequestID=undefined;
    if(message.errorCode==='CEF_PRESENTATION_LAYOUT_STALE')report('stale-layout-rejected');
    else report('failure',{step:'stale-layout-admitted'});
    return;
   }
   if(message.type!=='dsh-work/mac-preview-command'||message.nonce!==cfg.nonce)return;
   const command=message.command;
   if(command==='open-workspace-picker')void until('workspace-picker-button',()=>click('^(Add workspace|添加工作区)$'))
    .catch(()=>report('failure',{step}));
   if(command==='open-shell-overlay'||command==='close-shell-overlay'){
    const active=command==='open-shell-overlay';
    overlayExpected={blocked:true,stage:active?'shell-overlay-opened':'shell-overlay-still-blocked'};
    parent.postMessage({version:1,type:'dsh-work/modal-mask',document:documentToken,active,background:'transparent',blur:''},shell);
   }
   if(command==='open-provider-overlay'||command==='close-provider-overlay'){
    const blocked=command==='open-provider-overlay';
    providerRequestID=cfg.nonce+(blocked?'-provider-open':'-provider-close');
    if(!blocked)overlayExpected={blocked:false,stage:'shell-overlay-closed'};
    parent.postMessage({version:1,type:'dsh-work/browser',id:providerRequestID,action:'modal-overlay',document:documentToken,blocked},shell);
   }
   if(command==='send-stale-layout'&&message.slot&&Number.isSafeInteger(layoutRevision)&&layoutRevision>0){
    staleRequestID=cfg.nonce+'-stale-layout';
    parent.postMessage({version:1,type:'dsh-work/browser',id:staleRequestID,action:'update',document:documentToken,
     slot:message.slot,address:'',visible:false,rect:{x:0,y:0,width:0,height:0},layoutRevision:layoutRevision-1},shell);
   }
   if(command==='hide')void until('collapse-sidebar',()=>click('^(Collapse right sidebar|收起右侧边栏)$')).then(()=>report('hidden-requested')).catch(()=>report('failure',{step}));
   if(command==='show')void until('expand-sidebar',()=>{const e=document.querySelector('button[data-sidebar-right-expand]');if(!e||e.disabled||!e.getClientRects().length)return false;e.click();return true})
    .then(()=>until('sidebar-reopened-layout',sidebarReopened)).then(()=>report('sidebar-reopened')).catch(()=>report('failure',{step,browserSidebar:browserSidebarState()}));
   if(command==='answer-prompt')void until('answer-prompt',()=>{
    const layer=document.querySelector('.dsh-web-dialog-layer'),input=layer?.querySelector('input'),button=layer?.querySelector('button[data-primary]');
    if(!input||!button||button.disabled||!layer.textContent.includes('Fixture prompt')||input.value!=='默认')return false;
    input.value='已确认';button.click();return true;
   }).then(()=>report('prompt-answered')).catch(()=>report('failure',{step}));
   if(command==='switch-to-guide')void until('new-guide-tab-button',()=>{
    browserTab=selectedTab();
    const buttons=hitTested('button[data-dockkit-add-tab]');
    if(!browserTab||buttons.length!==1)return false;
    buttons[0].click();return true;
   }).then(()=>until('selected-guide-tab',()=>selectedTab()&&selectedTab()!==browserTab&&hitTested(guideSelector).length===1))
    .then(()=>report('guide-tab-selected')).catch(()=>report('failure',{step,browserSidebar:browserSidebarState()}));
   if(command==='switch-to-browser')void until('reactivate-browser-tab',()=>{
    const tab=hitTested('[data-dockkit-tab]').find(element=>element.dataset.dockkitTab===browserTab);
    if(!tab)return false;
    tab.click();return true;
   }).then(()=>until('selected-browser-tab',()=>selectedTab()===browserTab&&sidebarReopened()))
    .then(()=>report('browser-tab-selected')).catch(()=>report('failure',{step,browserSidebar:browserSidebarState()}));
   if(command==='deny-popup'||command==='allow-popup')void until(command,()=>{
    const layer=document.querySelector('.dsh-popup-layer'),button=layer?.querySelector(command==='allow-popup'?'button[data-primary]':'button:not([data-primary])');
    if(!button||button.disabled||layer.querySelector('.dsh-popup-target')?.textContent!==cfg.url+'popup')return false;
    button.click();return true;
   }).then(()=>report('popup-answered')).catch(()=>report('failure',{step}));
  });
 }catch{report('failure',{step,browserSidebar:browserSidebarState()})}
})();
