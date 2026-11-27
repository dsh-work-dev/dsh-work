// Optional BrowserPage provider for the exact runtime projection. It owns only
// presentation intent and observable toolbar state; Go owns native authority.
(() => {
 globalThis.__DSH_WORK_EMBEDDED_VERSION__='0.2.2-rc.1';
 const shellOrigin=location.ancestorOrigins?.[0];if(!shellOrigin||shellOrigin==='null'||parent===window)return;
 const documentToken=decodeURIComponent(location.hash.slice(1).match(/(?:^|&)__dsh_work_doc=([^&]+)/)?.[1]||'');
 const pending=new Map();let blocked=false,layoutRevision=0;
 const pages=new Set(),artifactPages=new Map();let artifactLabels,artifactLabelRevision=0;
 const applyArtifactLabels=value=>{artifactLabels=value;artifactLabelRevision++;for(const page of pages)page.labelsChanged()};
 function request(action,data={}){return new Promise((resolve,reject)=>{const id=crypto.randomUUID();const timer=action==='artifact-open'?undefined:setTimeout(()=>{pending.delete(id);reject(Error('Browser request timed out'))},12000);pending.set(id,{resolve,reject,timer});parent.postMessage({version:1,type:'dsh-work/browser',id,action,document:documentToken,...data},shellOrigin)})}
 addEventListener('message',event=>{
  if(event.source!==parent||event.origin!==shellOrigin)return;
  const m=event.data;if(m?.version!==1)return;
  if(m.type==='dsh-work/browser-artifact-labels'&&m.document===documentToken){applyArtifactLabels(m.value);return}
  if(m.type==='dsh-work/browser-previews'&&m.document===documentToken){for(const page of pages)page.applyPreview(m.value?.find(p=>p.slot===page.slot));return}
  if(m.type==='dsh-work/browser-layout'&&m.document===documentToken){for(const page of pages)page.schedule(m.remeasure===true);return}
  if(m.type==='dsh-work/browser-blocked'&&m.document===documentToken){
   const nextRevision=m.layoutRevision??layoutRevision;
   if(!Number.isSafeInteger(nextRevision)||nextRevision<layoutRevision)return;
   const remeasure=nextRevision!==layoutRevision||(blocked&&!m.blocked);
   blocked=!!m.blocked;layoutRevision=nextRevision;
   for(const page of pages)page.schedule(remeasure);
   return;
  }
  if(m.type!=='dsh-work/browser-result')return;
  const held=pending.get(m.id);if(!held)return;pending.delete(m.id);clearTimeout(held.timer);
  if(m.error){const failure=Error(m.error);if(typeof m.errorCode==='string')failure.code=m.errorCode;held.reject(failure)}else held.resolve(m.value);
 });
 // New modals cannot paint or receive input before native Hide is ACKed.
 // Visibility preserves layout so the observer can read the display intent.
 const modalSelector='[role="dialog"][aria-modal="true"], [data-shortcut-modal][role="dialog"], dialog[open]';
 const popoverSelector='[role="menu"], [role="listbox"], [data-radix-popper-content-wrapper], [popover]:popover-open';
 const modalStyle=document.createElement('style');const waiting=modalSelector.split(',').map(s=>s.trim()+':not([data-dsh-work-modal-ready])');waiting.push('[data-dsh-work-modal-pending]');modalStyle.textContent=waiting.flatMap(s=>[s,s+' *']).join(',')+'{visibility:hidden!important;pointer-events:none!important}';document.head.append(modalStyle);
 let modalRevision=0,modalKey=null,modalQueue=Promise.resolve();const modalIds=new WeakMap(),readyModals=new Set(),pendingMasks=new Set();let nextModal=0;
 const maskFor=modal=>{let current=modal;while(current?.parentElement&&current.parentElement!==document.body){const parent=current.parentElement;const mask=[...parent.children].find(e=>e!==current&&e.getAttribute('aria-hidden')==='true'&&/mask|backdrop/i.test(typeof e.className==='string'?e.className:''));if(mask)return mask;current=parent}};
 const scanModals=()=>{
  const all=[...document.querySelectorAll(modalSelector)],modals=all.filter(e=>!e.hidden&&e.getClientRects().length>0&&getComputedStyle(e).display!=='none');
  const popovers=[...document.querySelectorAll(popoverSelector)].filter(e=>!e.hidden&&e.getClientRects().length>0&&getComputedStyle(e).visibility!=='hidden'&&[...pages].some(page=>page.intersectsOverlay(e)));
  const active=[...new Set([...modals,...popovers])];
  for(const e of readyModals)if(!active.includes(e)){e.removeAttribute('data-dsh-work-modal-ready');readyModals.delete(e)}
  const key=active.map(e=>{if(!modalIds.has(e))modalIds.set(e,++nextModal);return modalIds.get(e)}).join(',');if(key===modalKey)return;modalKey=key;const revision=++modalRevision;
  for(const e of modals)if(!e.hasAttribute('data-dsh-work-modal-ready')){const mask=maskFor(e);if(mask){mask.setAttribute('data-dsh-work-modal-pending','');pendingMasks.add(mask)}}
  modalQueue=modalQueue.catch(()=>{}).then(async()=>{if(revision!==modalRevision)return;await request('modal-overlay',{blocked:active.length>0});if(revision!==modalRevision)return;for(const e of active)if(e.isConnected){e.setAttribute('data-dsh-work-modal-ready','');readyModals.add(e)}for(const mask of pendingMasks)mask.removeAttribute('data-dsh-work-modal-pending');pendingMasks.clear()}).catch(error=>console.error('Browser modal pause failed',String(error)));
 };
 const modalObserver=new MutationObserver(()=>{scanModals();for(const page of pages)page.schedule()});modalObserver.observe(document.documentElement,{subtree:true,childList:true,attributes:true,attributeFilter:['role','aria-modal','data-shortcut-modal','hidden','open','class','style','aria-hidden','data-sidebar-right-open']});
 // A CSS transition can finish without a resize or a new intersection entry.
 const onLayoutTransition=event=>{
  if(event.propertyName==='visibility')for(const page of pages)page.schedule();
  if(event.propertyName==='transform')for(const page of pages)page.refreshIntersection(event.target);
 };
 addEventListener('transitionend',onLayoutTransition,true);
 addEventListener('transitioncancel',onLayoutTransition,true);
 addEventListener('DOMContentLoaded',scanModals,{once:true});
 // A webpage dialog belongs to its viewport, never the application's top layer.
 const dialogStyle=document.createElement('style');dialogStyle.textContent=`
 .dsh-downloads{position:absolute;inset:auto 0 0;z-index:1;box-sizing:border-box;max-height:128px;overflow:auto;border-top:1px solid var(--group-border,#c6ced8);background:var(--canvas,#fff);color:var(--text,#18212f);font:12px/1.4 system-ui,sans-serif}
 .dsh-browser-preview{position:absolute;inset:0;z-index:0;pointer-events:none;background-position:center;background-repeat:no-repeat;background-size:100% 100%;display:grid;place-items:center;color:var(--muted,#667085);background-color:var(--canvas,#f5f7fb);font:13px system-ui,sans-serif}
 .dsh-download-row{display:grid;grid-template-columns:minmax(0,1fr) auto;gap:3px 12px;padding:8px 12px}
 .dsh-download-name{font-weight:600;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
 .dsh-download-status{color:var(--muted,#667085);overflow-wrap:anywhere}
 .dsh-download-row progress{width:100%;height:4px;grid-column:1/-1;accent-color:var(--accent,#2563eb)}
 .dsh-download-row button{grid-column:2;grid-row:1/3;align-self:center;font:inherit;padding:5px 9px;border:1px solid var(--group-border,#bac4d0);border-radius:6px;background:var(--canvas,#fff);color:inherit;cursor:pointer}
 .dsh-download-row button:focus-visible{outline:2px solid var(--accent,#2563eb);outline-offset:2px}
 .dsh-web-dialog-layer{position:absolute;inset:0;z-index:2;display:grid;place-items:center;padding:16px;box-sizing:border-box;overflow:hidden;background:color-mix(in srgb,var(--canvas,#f5f7fb) 78%,transparent)}
 .dsh-web-dialog{box-sizing:border-box;width:min(400px,100%);max-height:100%;overflow:auto;padding:20px;border:1px solid var(--dialog-border,var(--group-border,#c6ced8));border-radius:12px;background:var(--dialog-bg,var(--canvas,#fff));color:var(--text,#18212f);box-shadow:var(--dialog-shadow,0 12px 36px #0002);font:14px/1.5 system-ui,sans-serif}
 .dsh-web-dialog h2{font-size:16px;line-height:1.4;margin:0 0 4px;font-weight:650}
 .dsh-web-dialog-origin{margin:0 0 16px;color:var(--muted,#667085);overflow-wrap:anywhere}
 .dsh-web-dialog-message{margin:0 0 18px;white-space:pre-wrap;overflow-wrap:anywhere}
 .dsh-web-dialog input{box-sizing:border-box;width:100%;padding:9px 11px;margin:0 0 18px;border:1px solid var(--input-border,var(--group-border,#b8c2cf));border-radius:7px;background:var(--input-bg,var(--canvas,#fff));color:var(--text,#18212f);font:inherit}
 .dsh-web-dialog-actions{display:flex;justify-content:flex-end;gap:8px;flex-wrap:wrap}
 .dsh-web-dialog button{font:inherit;min-height:34px;padding:6px 14px;border-radius:7px;border:1px solid var(--button-border,var(--group-border,#bac4d0));background:var(--button-bg,var(--canvas,#fff));color:var(--text,#18212f);cursor:pointer}
 .dsh-web-dialog button[data-primary]{background:var(--accent,#2563eb);color:var(--on-accent,#fff);border-color:transparent}
 .dsh-web-dialog button:focus-visible,.dsh-web-dialog input:focus-visible{outline:2px solid var(--accent,#2563eb);outline-offset:2px}
 .dsh-web-dialog button:disabled{opacity:.5;cursor:default}
 `;document.head.append(dialogStyle);
 const popupStyle=document.createElement('style');popupStyle.textContent=`
 .dsh-popup-layer{position:absolute;inset:0;z-index:3;display:grid;place-items:center;padding:16px;box-sizing:border-box;overflow:auto;background:color-mix(in srgb,var(--canvas,#f5f7fb) 88%,transparent)}
 .dsh-popup-card{border-top:4px solid var(--accent,#2563eb)}
 .dsh-popup-label{font-size:12px;font-weight:650;color:var(--accent,#2563eb);margin:0 0 12px}
 .dsh-popup-target{white-space:pre-wrap;overflow-wrap:anywhere;margin:12px 0 20px}
 `;document.head.append(popupStyle);
 const target=url=>{try{const parsed=new URL(url);if(!['http:','https:'].includes(parsed.protocol))return;return{kind:parsed.protocol.slice(0,-1),url:parsed.href,title:parsed.hostname}}catch{}};
 globalThis.__DSH_WORK_BROWSER_PROVIDER__={protocolVersion:1,createArtifactPage(session,key,signal){const id=session+"/"+key;let pair=artifactPages.get(id);if(!pair){pair=this.createPage(session,{key:"artifact/"+key,persist(){},openRequested(){}});artifactPages.set(id,pair);if(!artifactLabels){const revision=artifactLabelRevision;void request("artifact-labels",{slot:session+"/artifact/"+key}).then(value=>{if(revision===artifactLabelRevision)applyArtifactLabels(value)},()=>{})}const close=()=>{artifactPages.delete(id);void pair.frame.dispose()};if(signal.aborted)close();else signal.addEventListener("abort",close,{once:true})}return pair},async selectPage(key){
  const candidates=[...pages].filter(page=>page.key===key&&page.visible());if(candidates.length!==1)throw Error('Visible browser page required');
  return candidates[0].select();
 },createPage(session,options){
  const slot=`${session}/${options.key}`;let container,observer,intersection,intersecting=false,intersectionClip,choosing=false,disposed=false,mounted=false,registered=false,issued=false,mountGeneration=0,state={target:undefined,address:'empty',loading:false,canGoBack:false,canGoForward:false,error:undefined,sandboxEnabled:undefined};
  let view,queued=Promise.resolve(),pendingURL,revision=0,lastGeometry='',persistedKey='',layoutQueued=false,pollQueued=false,previewLayer,previewIdentity='',acknowledgedLayout=0,acknowledgedMount=0;const listeners=new Set();
  const clearPreview=()=>{previewLayer?.remove();previewLayer=undefined;previewIdentity=''};
  const identity=v=>v&&[v.lease,v.document,v.frame,v.url].join('|');
  let dialogCard,popupCard,overlayPreparation;const dispatchedPopups=new Set();
  let downloadTray,downloadHeight=0;const downloadRows=new Map(),hiddenDownloads=new Set();
  const syncPreviewBounds=()=>{if(previewLayer)previewLayer.style.bottom=`${downloadHeight}px`};
  const clearDownloads=()=>{downloadTray?.remove();downloadTray=undefined;downloadRows.clear();downloadHeight=0;syncPreviewBounds()};
  const downloadError=code=>({
   DOWNLOAD_NETWORK_FAILED:'连接中断，请重新下载。',DOWNLOAD_HTTP_FAILED:'网站未能提供文件，请稍后重试。',DOWNLOAD_REQUEST_FAILED:'无法连接下载，请重试。',
   DOWNLOAD_BUDGET_EXCEEDED:'文件超过当前下载上限，请选择较小文件。',DOWNLOAD_DISK_SPACE_REQUIRED:'磁盘空间不足，请释放空间后重试。',
   DOWNLOAD_WRITE_FAILED:'无法写入文件，请检查磁盘后重试。',DOWNLOAD_DESTINATION_CHANGED:'目标文件已改变，请重新选择保存位置。',DOWNLOAD_DESTINATION_BUSY:'目标已被占用，请重新选择保存位置。',
   DOWNLOAD_OWNED_FILE_CHANGED:'临时文件已改变，请重新下载。',DOWNLOAD_REDIRECT_NOT_APPROVED:'下载转向其他地址，请打开该地址后重新下载。',
   DOWNLOAD_FINALIZATION_FAILED:'无法完成文件检查，请检查文件来源后重试。',DOWNLOAD_RISK_CHECK_FAILED:'文件检查未通过，未保存文件。',DOWNLOAD_RISK_DECLINED:'已取消保存此文件。',
   DOWNLOAD_PUBLISH_FAILED:'无法保存文件，请检查目标位置后重试。',DOWNLOAD_BROKER_INTERRUPTED:'下载已中断，请重新下载。',DOWNLOAD_RECOVERY_REQUIRED:'保存未完成，请检查保留的原文件。',DOWNLOAD_CLEANUP_REQUIRED:'临时文件清理待完成。'
  }[code]||'下载未完成，请重试。');
  const renderDownloads=()=>{
   const items=(view?.downloads||[]).filter(d=>!hiddenDownloads.has(d.id));if(!mounted||!container||!items.length){if(downloadTray){clearDownloads();page.schedule()}return}
   if(!downloadTray){downloadTray=document.createElement('section');downloadTray.className='dsh-downloads';downloadTray.setAttribute('aria-label','下载');if(getComputedStyle(container).position==='static')container.style.position='relative';container.append(downloadTray)}
   const ids=new Set(items.map(d=>d.id));for(const [id,row]of downloadRows)if(!ids.has(id)){row.element.remove();downloadRows.delete(id)}
   const sizes=n=>n>=1048576?(n/1048576).toFixed(1)+' MB':n>=1024?(n/1024).toFixed(0)+' KB':n+' B';
   const labels={approved:'准备下载',starting:'正在连接',transferring:'正在下载',finalizing:'正在保存',stopping:'正在取消',complete:'下载完成',canceled:'已取消',revoked:'已撤销',failed:'下载失败',interrupted:'下载已中断'};
   for(const d of items){let row=downloadRows.get(d.id);if(!row){
    const element=document.createElement('div'),name=document.createElement('div'),status=document.createElement('div'),progress=document.createElement('progress'),cancel=document.createElement('button');
    element.className='dsh-download-row';name.className='dsh-download-name';status.className='dsh-download-status';cancel.type='button';cancel.textContent='取消';cancel.onclick=()=>{if(!downloadRows.get(d.id)?.active){hiddenDownloads.add(d.id);renderDownloads();return}cancel.disabled=true;void enqueue(async()=>{
     let localError='';try{await request('download-cancel',{slot,download:d.id})}catch{localError='取消未完成，请重试。'}
     try{accept(await request('state',{slot}))}catch{localError='无法读取下载状态，请稍后重试。'}
     const current=downloadRows.get(d.id);if(current){current.localError=localError;renderDownloads()}
    }).finally(()=>{cancel.disabled=false})};
    element.append(name,status,progress,cancel);downloadTray.append(element);row={element,name,status,progress,cancel};downloadRows.set(d.id,row);
   }
   const text=labels[d.state]||'等待下载',reason=d.error&&['failed','interrupted'].includes(d.state)?downloadError(d.error):text,detail=(row.localError||reason)+(d.recoveryPath?' · 原文件保留于 '+d.recoveryPath:d.cleanupRequired?' · 临时文件清理待完成':'')+(d.received?' · '+sizes(d.received)+(d.total>0?' / '+sizes(d.total):''):'');
   if(row.name.textContent!==d.filename)row.name.textContent=d.filename;row.name.title=d.filename;
   if(row.status.textContent!==detail)row.status.textContent=detail;
   const active=['approved','starting','transferring','finalizing','stopping'].includes(d.state);row.active=active;if(!active)row.localError='';row.cancel.hidden=false;row.cancel.textContent=active?'取消':'隐藏';row.cancel.setAttribute('aria-label',active?'取消下载':'隐藏此下载');row.cancel.disabled=d.state==='stopping';row.progress.hidden=!active;row.progress.setAttribute('aria-label',d.filename+' '+text);
   if(d.total>0){row.progress.max=d.total;row.progress.value=d.received}else row.progress.removeAttribute('value');
   }
   const height=downloadTray.offsetHeight;if(height!==downloadHeight){downloadHeight=height;syncPreviewBounds();page.schedule()}
  };
  const clearDialog=()=>{dialogCard?.layer.remove();dialogCard=undefined};
  const renderDialog=(dialog,ready)=>{
   if(dialogCard?.dialog.epoch===dialog.epoch){dialogCard.dialog=dialog;dialogCard.ready=ready;for(const b of dialogCard.buttons)b.disabled=!ready||dialogCard.busy;return}
   clearDialog();
   const element=(tag,cls,text)=>{const e=document.createElement(tag);if(cls)e.className=cls;if(text!==undefined)e.textContent=text;return e};
   const layer=element('div','dsh-web-dialog-layer'),card=element('section','dsh-web-dialog');
   card.setAttribute('role','dialog');card.setAttribute('aria-modal','false');
   const title=element('h2','',dialog.kind==='alert'?'网页提示':dialog.kind==='prompt'?'网页输入':dialog.kind==='beforeunload'?'离开此页面？':'网页确认');
   title.id='dsh-web-dialog-'+crypto.randomUUID();card.setAttribute('aria-labelledby',title.id);
   const origin=element('p','dsh-web-dialog-origin',dialog.origin||'来源无法确认');
   const message=element('p','dsh-web-dialog-message',dialog.kind==='beforeunload'?'未保存的更改可能会丢失。':dialog.message);
   const actions=element('div','dsh-web-dialog-actions');card.append(title,origin,message);
   if(dialog.truncated)card.append(element('p','dsh-web-dialog-origin','内容过长，已截取显示。'));
   const input=dialog.kind==='prompt'?element('input'):undefined;
   if(input){input.type='text';input.value=dialog.defaultPrompt;input.setAttribute('aria-label','回复网页');card.append(input)}
   const current={layer,dialog,ready,busy:false,buttons:[],input};
   const submit=async allowed=>{
    if(dialogCard!==current||!current.ready||current.busy||!canInteract())return;
    current.busy=true;for(const b of current.buttons)b.disabled=true;
    try{await enqueue(async()=>{await request('dialog-answer',{slot,dialog:current.dialog,accept:allowed,text:input?.value||''});accept(await request('state',{slot}))})}
    catch(error){
     // A stale card cannot answer a successor. Re-read it instead of turning a
     // harmless expired click into a page-load failure.
     if(String(error).includes('CEF_DIALOG_AUTHORIZATION_CHANGED')){void enqueue(async()=>accept(await request('state',{slot})))}else fail(error)
    }finally{current.busy=false;if(dialogCard===current)for(const b of current.buttons)b.disabled=!current.ready}
   };
   const button=(text,allowed)=>{const b=element('button','',text);b.type='button';b.disabled=!ready;b.onclick=()=>void submit(allowed);current.buttons.push(b);actions.append(b);return b};
   if(dialog.kind!=='alert')button(dialog.kind==='beforeunload'?'留在页面':'取消',false);
   button(dialog.kind==='beforeunload'?'离开页面':'确定',true).dataset.primary='';
   card.addEventListener('keydown',event=>{if(event.key==='Escape'){event.preventDefault();event.stopPropagation();void submit(false)}else if(event.key==='Enter'&&event.target===input){event.preventDefault();void submit(true)}});
   card.append(actions);layer.append(card);
   if(getComputedStyle(container).position==='static')container.style.position='relative';
   container.append(layer);dialogCard=current;
  };
  const clearPopup=()=>{popupCard?.layer.remove();popupCard=undefined};
  const renderPopup=(popup,ready)=>{
   if(popupCard?.popup.id===popup.id){popupCard.popup=popup;popupCard.ready=ready;for(const b of popupCard.buttons)b.disabled=!ready||popupCard.busy;return}
   clearPopup();
   const element=(tag,cls,text)=>{const e=document.createElement(tag);if(cls)e.className=cls;if(text!==undefined)e.textContent=text;return e};
   const layer=element('div','dsh-popup-layer'),card=element('section','dsh-web-dialog dsh-popup-card');
   card.setAttribute('role','dialog');card.setAttribute('aria-modal','false');
   const title=element('h2','','在新标签页打开？');title.id='dsh-popup-'+crypto.randomUUID();card.setAttribute('aria-labelledby',title.id);
   card.append(element('p','dsh-popup-label','DSH Work · 新窗口请求'),title,
    element('p','dsh-web-dialog-origin','来源标签：'+(view.title||popup.sourceURL)),
    element('p','dsh-web-dialog-origin',popup.origin),element('p','dsh-popup-target',popup.url));
   const actions=element('div','dsh-web-dialog-actions'),current={layer,popup,ready,busy:false,buttons:[]};
   const submit=async allowed=>{
    if(popupCard!==current||!current.ready||current.busy||!canInteract()||dispatchedPopups.has(current.popup.id))return;
    current.busy=true;for(const b of current.buttons)b.disabled=true;
    const source=current.popup,generation=mountGeneration;
    try{await enqueue(async()=>{
     // Native takes the request exactly once. There is no retry/replay path
     // when this reply is lost; a void DSH callback is not an open receipt.
     const url=await request('popup',{slot,popup:source,command:allowed?'allow':'deny'});
     if(!allowed){clearPopup();if(!disposed)accept(await request('state',{slot}));return}
     if(dispatchedPopups.has(source.id))return;
     dispatchedPopups.add(source.id);
     if(disposed||generation!==mountGeneration||!mounted||!canInteract())return;
     const latest=await request('state',{slot});
     if(disposed||generation!==mountGeneration||!mounted||!canInteract())return;
     accept(latest);
     if(url!==source.url||latest.state!=='ready'||latest.document!==source.document||latest.lease!==source.view)return;
     clearPopup();
     options.openRequested(url);
    })}catch(error){
     if(!disposed){clearPopup();void enqueue(async()=>accept(await request('state',{slot}))).catch(fail)}
    }finally{current.busy=false;if(popupCard===current)for(const b of current.buttons)b.disabled=!current.ready}
   };
   const button=(text,allowed)=>{const b=element('button','',text);b.type='button';b.disabled=!ready;b.onclick=()=>void submit(allowed);current.buttons.push(b);actions.append(b);return b};
   button('拒绝',false);button('打开新标签页',true).dataset.primary='';
   card.addEventListener('keydown',event=>{if(event.key==='Escape'){event.preventDefault();event.stopPropagation();void submit(false)}});
   card.append(actions);layer.append(card);if(getComputedStyle(container).position==='static')container.style.position='relative';container.append(layer);popupCard=current;
  };
  const currentOverlay=()=>{
   if(disposed||!canInteract())return;
   if(view?.dialog)return {kind:'dialog',value:view.dialog,ready:view.dialogVisible};
   if(view?.popup&&!dispatchedPopups.has(view.popup.id))return {kind:'popup',value:view.popup,ready:view.popupVisible};
  };
  const sameOverlay=(first,second)=>first&&second&&first.kind===second.kind&&
   (first.value.epoch??first.value.id)===(second.value.epoch??second.value.id)&&
   first.value.document===second.value.document&&first.value.view===second.value.view&&first.value.presentation===second.value.presentation;
  const overlayCommand=(overlay,command)=>overlay.kind==='dialog'
   ?request('dialog-'+command,{slot,dialog:overlay.value})
   :request('popup',{slot,popup:overlay.value,command});
  const syncOverlay=()=>{
   const overlay=currentOverlay();
   if(overlay?.kind!=='dialog'&&dialogCard)clearDialog();
   if(overlay?.kind!=='popup'&&popupCard)clearPopup();
   if(!overlay||overlay.ready){
    overlayPreparation=undefined;
    if(overlay?.kind==='dialog')renderDialog(overlay.value,true);
    if(overlay?.kind==='popup')renderPopup(overlay.value,true);
    return;
   }
   const generation=mountGeneration;
   if(overlayPreparation?.generation===generation&&sameOverlay(overlayPreparation.overlay,overlay))return;
   const preparation={overlay,generation};
   overlayPreparation=preparation;
   const current=()=>overlayPreparation===preparation&&mountGeneration===generation&&sameOverlay(overlay,currentOverlay());
   const currentCard=()=>overlay.kind==='dialog'?dialogCard:popupCard;
   const prepare=async()=>{
    try{
     const card=await enqueue(async()=>{
      if(!current())return;
      await overlayCommand(overlay,'suspend');
      if(!current())return;
      if(overlay.kind==='dialog')renderDialog(overlay.value,false);else renderPopup(overlay.value,false);
      return currentCard();
     });
     if(!card)return;
     // Visible paint may be delayed when a workspace or its tab is hidden.
     // Leave the presentation queue free to hide, switch, or retire the slot.
     await new Promise(resolve=>requestAnimationFrame(resolve));
     if(!current())return;
     await enqueue(async()=>{
      if(!current()||currentCard()!==card||!card.layer.isConnected)return;
      await overlayCommand(overlay,'ready');
      if(!current())return;
      const state=await request('state',{slot});
      if(current())accept(state);
     });
    }catch{
     if(current()){
      if(overlay.kind==='dialog')clearDialog();else clearPopup();
     }
    }finally{
     if(overlayPreparation===preparation)overlayPreparation=undefined;
    }
   };
   void prepare();
  };
  const publish=(next,force=false)=>{if(artifactLabels)next={...next,artifactLabels};if(!force&&JSON.stringify(state)===JSON.stringify(next))return;state=next;for(const listener of listeners)listener()};
  const accept=value=>{const changedPreview=!!previewLayer&&identity(value)!==previewIdentity;if(previewLayer&&(changedPreview||value.visible&&canInteract()))clearPreview();view=value;if(changedPreview&&blocked)page.applyPreview();registered=value.state!=='empty';const entry=target(value.url),needsReload=value.state==='NeedsReload';if(entry&&value.title)entry.title=value.title;
   // A safe descriptor is not a live frame. Feed the existing DSH restore
   // checkpoint before notifying so its standard title/address/action render
   // even when the provider initially has the same empty frame snapshot.
   let saved=false;if(entry&&['ready','NeedsReload'].includes(value.state)&&persistedKey!==entry.url+'|'+entry.title){persistedKey=entry.url+'|'+entry.title;options.persist({entries:[entry],index:0,request:undefined,navigation:{status:'known',revision:++revision},failure:undefined});saved=true}
   renderDownloads();syncOverlay();
   publish({artifactName:value.artifactName,needsReload,target:needsReload?undefined:entry,address:!needsReload&&entry?'observed':'empty',loading:value.state==='loading',canGoBack:value.state!=='controlPaused'&&!!value.canGoBack,canGoForward:value.state!=='controlPaused'&&!!value.canGoForward,error:value.state==='controlPaused'?{code:-32099,description:'Browser control is paused.'}:['presentationFailed','navigationFailed'].includes(value.state)?{code:undefined,description:'Reload to continue.'}:undefined,sandboxEnabled:undefined},needsReload&&saved);return value};
  const fail=error=>{if(String(error).includes('CEF_BROKER_CONTROL_PAUSED')){if(view)accept({...view,state:'controlPaused'});return}console.error('Browser presentation failed',String(error));const unavailable=['CEF_INSTALL_MISSING','CEF_INSTALL_CORRUPT','CEF_INSTALL_INCOMPATIBLE','CEF_DSH_RUNTIME_UNSUPPORTED','CEF_PLATFORM_UNSUPPORTED'].includes(error?.code);publish({...state,loading:false,error:{code:undefined,description:unavailable?error.message:'The page is unavailable. Reload to continue.'}})};
  const geometry=()=>{
   const rect=container?.getBoundingClientRect();
   if(!rect)return{rect:{x:0,y:0,width:0,height:0},visible:false};
   const dock=container.closest?.('[data-dockkit-host="dock"]'),panel=dock?.closest('[data-sidebar-right-panel]');
   const collapsed=panel&&!panel.hasAttribute('data-sidebar-right-open');
   const left=Math.min(innerWidth,Math.max(0,rect.left+(intersectionClip?.left??0)));
   const top=Math.min(innerHeight,Math.max(0,rect.top+(intersectionClip?.top??0)));
   const right=Math.min(innerWidth,rect.right-(intersectionClip?.right??0));
   const bottom=Math.min(innerHeight,rect.bottom-downloadHeight,rect.bottom-(intersectionClip?.bottom??0));
   return{rect:{x:left,y:top,width:Math.max(0,right-left),height:Math.max(0,bottom-top)},visible:mounted&&!choosing&&!collapsed&&intersecting&&container.isConnected&&container.getClientRects().length>0&&getComputedStyle(container).visibility!=='hidden'&&right>left&&bottom>top};
  };
  const canInteract=()=>!blocked&&acknowledgedLayout===layoutRevision&&acknowledgedMount===mountGeneration&&geometry().visible;
  const enqueue=task=>{const result=queued.then(task);queued=result.catch(error=>{if(!disposed&&error?.code!=='CEF_PRESENTATION_LAYOUT_STALE'&&!/CEF_(DIALOG|POPUP)_AUTHORIZATION_CHANGED/.test(String(error)))fail(error)});return result};
  const present=async(address,g)=>{
   const sampledRevision=layoutRevision,generation=mountGeneration;
   const value=await request('update',{slot,address,...g,layoutRevision:sampledRevision});
   if(generation===mountGeneration&&sampledRevision===layoutRevision&&value.visible){acknowledgedLayout=sampledRevision;acknowledgedMount=generation}
   return value;
  };
  const update=(address='',explicit=false)=>{
   let generation=mountGeneration;
   return enqueue(async()=>{
    if(disposed||generation!==mountGeneration&&!address)return;
    const g=geometry();lastGeometry=JSON.stringify(g);issued=true;
    let value=await present(address,g);
    if(value.state==='NeedsReload'&&explicit&&mounted&&!disposed){
     // Restoring belongs to this tab. DSH may replace its viewport while Update waits.
     generation=mountGeneration;
     await request('recover',{slot,address});
     if(generation===mountGeneration)value=await present('',geometry());
    }
    if(generation!==mountGeneration){
     if(!mounted&&!disposed)await request('update',{slot,address:'',rect:g.rect,visible:false,layoutRevision});
     return value;
    }
    return accept(value);
   });
  };
  const page={key:options.key,slot,intersectsOverlay(e){if(!mounted||!container||!container.isConnected)return false;const a=container.getBoundingClientRect(),b=e.getBoundingClientRect();const l=Math.max(a.left,b.left),t=Math.max(a.top,b.top),r=Math.min(a.right,b.right),bottom=Math.min(a.bottom,b.bottom);if(r<=l||bottom<=t)return false;const x=(l+r)/2,y=(t+bottom)/2;const top=document.elementFromPoint(x,y);return !!top&&(e===top||e.contains(top))},labelsChanged:()=>publish({...state}),applyPreview(p){if(!mounted||!container||!view)return;clearPreview();previewIdentity=identity(view);previewLayer=document.createElement('div');previewLayer.className='dsh-browser-preview';previewLayer.setAttribute('aria-hidden','true');if(p&&identity(p)===previewIdentity&&/^[-A-Za-z0-9+/=]+$/.test(p.data)&&p.data.length<=6*1024*1024)previewLayer.style.backgroundImage=`url(data:image/png;base64,${p.data})`;else previewLayer.textContent=view.state==='loading'?'页面加载中…':'页面暂时隐藏';if(getComputedStyle(container).position==='static')container.style.position='relative';syncPreviewBounds();container.prepend(previewLayer)},visible:canInteract,select:()=>{if(!view?.lease||view.state!=='ready'||!canInteract())throw Error('Reload the visible page first');return request('select-control',{session,slot,lease:view.lease})},schedule(remeasure=false){if(disposed||!mounted)return;if(remeasure)lastGeometry='';scanModals();syncOverlay();
   // ResizeObserver already follows layout frames. Timers are throttled in
   // covered WebViews, so dispatch directly and coalesce while native is busy.
   if(layoutQueued)return;const key=JSON.stringify(geometry());if(key===lastGeometry)return;
   const scheduledRevision=layoutRevision;lastGeometry=key;layoutQueued=true;
   void update().then(()=>{layoutQueued=false;page.schedule()},()=>{layoutQueued=false;if(scheduledRevision!==layoutRevision||lastGeometry==='')page.schedule()});
  },refreshIntersection(ancestor){
   if(!shellOrigin.startsWith('wails:')||!mounted||!intersection||!ancestor?.contains?.(container))return;
   // The one-pixel clip check can change within an area-ratio threshold band.
   intersection.takeRecords();
   intersection.unobserve(container);
   intersection.observe(container);
  }};pages.add(page);
  const polling=setInterval(()=>{if(disposed||!registered||pollQueued||layoutQueued)return;pollQueued=true;void enqueue(async()=>accept(await request('state',{slot}))).then(()=>{pollQueued=false},()=>{pollQueued=false})},250);
  const frame={async openArtifact(){if(disposed||!mounted||choosing)throw Error("Artifact view unavailable");choosing=true;issued=true;try{await update();if(disposed)return;const value=await request("artifact-open",{slot});if(disposed)return;accept(value)}finally{choosing=false;if(!disposed)await update()}},getSnapshot:()=>state,subscribe(listener){listeners.add(listener);return()=>listeners.delete(listener)},loadUrl(value){if(view?.state==='controlPaused')return;clearPreview();pendingURL=value.url;publish({...state,target:value,address:'requested',loading:true,error:undefined});if(mounted){const url=pendingURL;pendingURL=undefined;void update(url,true)}},goBack(){if(view?.state==='controlPaused')return;clearPreview();void enqueue(async()=>{await request('action',{slot,command:'back'});accept(await request('state',{slot}))})},goForward(){if(view?.state==='controlPaused')return;clearPreview();void enqueue(async()=>{await request('action',{slot,command:'forward'});accept(await request('state',{slot}))})},reload(){if(view?.state==='controlPaused')return;clearPreview();if(view?.state==='presentationFailed'){void update()}else if(view?.state==='NeedsReload'){void enqueue(async()=>{accept(await request('recover',{slot,address:''}));accept(await present('',geometry()))})}else if(registered){void enqueue(async()=>{await request('action',{slot,command:'reload'});accept(await request('state',{slot}))})}else if(state.target){void update(state.target.url,true)}},async dispose(){if(disposed)return;clearPreview();clearDownloads();clearDialog();clearPopup();disposed=true;mounted=false;mountGeneration++;pages.delete(page);clearInterval(polling);observer?.disconnect();intersection?.disconnect();await queued.catch(()=>{});if(issued)await request('detach',{slot});listeners.clear()}};
  const presentation={mount(id){container=document.getElementById(id);if(!container)throw Error('Browser viewport unavailable');mounted=true;const generation=++mountGeneration;intersecting=false;intersectionClip=undefined;lastGeometry='';observer?.disconnect();intersection?.disconnect();observer=new ResizeObserver(()=>page.schedule());observer.observe(container);
   // Ancestor overflow/transforms can hide a retained pane without resizing
   // its viewport. A native child must not outlive that DOM clip. Keep it
   // hidden until the browser confirms the whole slot is visible again.
   const webkit=shellOrigin.startsWith('wails:');
   intersection=new IntersectionObserver(entries=>{
    if(generation!==mountGeneration||!mounted)return;
    for(const entry of entries){
     if(entry.target!==container)continue;
     const bounds=entry.boundingClientRect,clip=entry.intersectionRect;
     // WKWebView can quantize the edges inward before reporting intersection.
     // Allow a fractional edge only with bounds cropped to that measurement.
     const subpixelClip=webkit&&bounds&&clip&&
      clip.left-bounds.left<1&&clip.top-bounds.top<1&&bounds.right-clip.right<1&&bounds.bottom-clip.bottom<1;
     intersecting=entry.isIntersecting&&(entry.intersectionRatio>=0.9999||!!subpixelClip);
     intersectionClip=intersecting&&subpixelClip&&entry.intersectionRatio<1?{
      left:Math.max(0,clip.left-bounds.left),top:Math.max(0,clip.top-bounds.top),
      right:Math.max(0,bounds.right-clip.right),bottom:Math.max(0,bounds.bottom-clip.bottom),
     }:undefined;
     page.schedule();
    }
   },{threshold:webkit?[0,0.99,0.995,0.9975,0.999,0.9999,1]:[0,0.9999]});intersection.observe(container);
   const scroll=()=>{if(generation===mountGeneration)page.schedule()};
   addEventListener('scroll',scroll,true);addEventListener('resize',scroll);
   if(pendingURL){const url=pendingURL;pendingURL=undefined;void update(url,true)}else{page.schedule()}
   return()=>{
    removeEventListener('scroll',scroll,true);removeEventListener('resize',scroll);
    if(generation!==mountGeneration)return;
    clearPreview();clearDownloads();clearDialog();clearPopup();mounted=false;intersecting=false;mountGeneration++;lastGeometry='';observer?.disconnect();intersection?.disconnect();void update();
   };
  }};
  return{frame,presentation};
 }};
})();
