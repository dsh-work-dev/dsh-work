// Optional BrowserPage provider for the exact runtime projection. It owns only
// presentation intent and observable toolbar state; Go owns native authority.
(() => {
 globalThis.__DSH_WORK_EMBEDDED_VERSION__='0.2.2-rc.1';
 const shellOrigin=location.ancestorOrigins?.[0];if(!shellOrigin||shellOrigin==='null'||parent===window)return;
 const documentToken=decodeURIComponent(location.hash.slice(1).match(/(?:^|&)__dsh_work_doc=([^&]+)/)?.[1]||'');
 const pending=new Map();let blocked=false;
 const pages=new Set();
 function request(action,data={}){return new Promise((resolve,reject)=>{const id=crypto.randomUUID();const timer=setTimeout(()=>{pending.delete(id);reject(Error('Browser request timed out'))},12000);pending.set(id,{resolve,reject,timer});parent.postMessage({version:1,type:'dsh-work/browser',id,action,document:documentToken,...data},shellOrigin)})}
 addEventListener('message',event=>{if(event.source!==parent||event.origin!==shellOrigin)return;const m=event.data;if(m?.version!==1)return;if(m.type==='dsh-work/browser-layout'){for(const page of pages)page.schedule();return}if(m.type==='dsh-work/browser-blocked'){blocked=!!m.blocked;for(const page of pages)page.schedule();return}if(m.type!=='dsh-work/browser-result')return;const held=pending.get(m.id);if(!held)return;pending.delete(m.id);clearTimeout(held.timer);m.error?held.reject(Error(m.error)):held.resolve(m.value)});
 // New modals cannot paint or receive input before native Hide is ACKed.
 // Visibility preserves layout so the observer can read the display intent.
 const modalSelector='[role="dialog"][aria-modal="true"], [data-shortcut-modal][role="dialog"], dialog[open]';
 const modalStyle=document.createElement('style');const waiting=modalSelector.split(',').map(s=>s.trim()+':not([data-dsh-work-modal-ready])');waiting.push('[data-dsh-work-modal-pending]');modalStyle.textContent=waiting.flatMap(s=>[s,s+' *']).join(',')+'{visibility:hidden!important;pointer-events:none!important}';document.head.append(modalStyle);
 let modalRevision=0,modalKey=null,modalQueue=Promise.resolve();const modalIds=new WeakMap(),readyModals=new Set(),pendingMasks=new Set();let nextModal=0;
 const maskFor=modal=>{let current=modal;while(current?.parentElement&&current.parentElement!==document.body){const parent=current.parentElement;const mask=[...parent.children].find(e=>e!==current&&e.getAttribute('aria-hidden')==='true'&&/mask|backdrop/i.test(typeof e.className==='string'?e.className:''));if(mask)return mask;current=parent}};
 const scanModals=()=>{
  const all=[...document.querySelectorAll(modalSelector)],active=all.filter(e=>!e.hidden&&e.getClientRects().length>0&&getComputedStyle(e).display!=='none');
  for(const e of readyModals)if(!active.includes(e)){e.removeAttribute('data-dsh-work-modal-ready');readyModals.delete(e)}
  const key=active.map(e=>{if(!modalIds.has(e))modalIds.set(e,++nextModal);return modalIds.get(e)}).join(',');if(key===modalKey)return;modalKey=key;const revision=++modalRevision;
  for(const e of active)if(!e.hasAttribute('data-dsh-work-modal-ready')){const mask=maskFor(e);if(mask){mask.setAttribute('data-dsh-work-modal-pending','');pendingMasks.add(mask)}}
  modalQueue=modalQueue.catch(()=>{}).then(async()=>{if(revision!==modalRevision)return;await request('modal-overlay',{blocked:active.length>0});if(revision!==modalRevision)return;for(const e of active)if(e.isConnected){e.setAttribute('data-dsh-work-modal-ready','');readyModals.add(e)}for(const mask of pendingMasks)mask.removeAttribute('data-dsh-work-modal-pending');pendingMasks.clear()}).catch(error=>console.error('Browser modal pause failed',String(error)));
 };
 const modalObserver=new MutationObserver(()=>{scanModals();for(const page of pages)page.schedule()});modalObserver.observe(document.documentElement,{subtree:true,childList:true,attributes:true,attributeFilter:['role','aria-modal','data-shortcut-modal','hidden','open','class','style','aria-hidden','data-sidebar-right-open']});
 addEventListener('DOMContentLoaded',scanModals,{once:true});
 // A webpage dialog belongs to its viewport, never the application's top layer.
 const dialogStyle=document.createElement('style');dialogStyle.textContent=`
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
 const target=url=>{try{const parsed=new URL(url);if(!['http:','https:'].includes(parsed.protocol))return;return{kind:parsed.protocol.slice(0,-1),url:parsed.href,title:parsed.hostname}}catch{}};
 globalThis.__DSH_WORK_BROWSER_PROVIDER__={protocolVersion:1,async selectPage(key){
  const candidates=[...pages].filter(page=>page.key===key&&page.visible());if(candidates.length!==1)throw Error('Visible browser page required');
  return candidates[0].select();
 },createPage(session,options){
  const slot=`${session}/${options.key}`;let container,observer,intersection,intersecting=false,disposed=false,mounted=false,registered=false,issued=false,mountGeneration=0,state={target:undefined,address:'empty',loading:false,canGoBack:false,canGoForward:false,error:undefined,sandboxEnabled:undefined};
  let view,queued=Promise.resolve(),pendingURL,revision=0,lastGeometry='',persistedKey='',layoutQueued=false,pollQueued=false;const listeners=new Set();
  let dialogCard,dialogSync=false,dialogRound=0;
  const clearDialog=()=>{dialogRound++;dialogCard?.layer.remove();dialogCard=undefined};
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
    if(dialogCard!==current||!current.ready||current.busy||!geometry().visible)return;
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
  const syncDialog=()=>{
   if(disposed||!mounted||!view?.dialog||!geometry().visible){if(dialogCard)clearDialog();return}
   if(view.dialogVisible){renderDialog(view.dialog,true);return}
   if(dialogSync)return;
   dialogSync=true;
   void enqueue(async()=>{
    const dialog=view?.dialog,generation=mountGeneration;if(!dialog||!geometry().visible)return;
    await request('dialog-suspend',{slot,dialog});
    if(disposed||generation!==mountGeneration||!geometry().visible)return;
    renderDialog(dialog,false);const round=dialogRound;
    await new Promise(resolve=>requestAnimationFrame(resolve));
    if(disposed||round!==dialogRound||generation!==mountGeneration||!geometry().visible)return;
    await request('dialog-ready',{slot,dialog});accept(await request('state',{slot}));
   }).catch(error=>{if(!String(error).includes('CEF_DIALOG_AUTHORIZATION_CHANGED')&&!disposed)fail(error)})
   .finally(()=>{dialogSync=false});
  };
  const publish=(next,force=false)=>{if(!force&&JSON.stringify(state)===JSON.stringify(next))return;state=next;for(const listener of listeners)listener()};
  const accept=value=>{view=value;registered=value.state!=='empty';const page=target(value.url),needsReload=value.state==='NeedsReload';if(page&&value.title)page.title=value.title;
   // A safe descriptor is not a live frame. Feed the existing DSH restore
   // checkpoint before notifying so its standard title/address/action render
   // even when the provider initially has the same empty frame snapshot.
   let saved=false;if(page&&['ready','NeedsReload'].includes(value.state)&&persistedKey!==page.url+'|'+page.title){persistedKey=page.url+'|'+page.title;options.persist({entries:[page],index:0,request:undefined,navigation:{status:'known',revision:++revision},failure:undefined});saved=true}
   syncDialog();
   publish({target:needsReload?undefined:page,address:!needsReload&&page?'observed':'empty',loading:value.state==='loading',canGoBack:value.state!=='controlPaused'&&!!value.canGoBack,canGoForward:value.state!=='controlPaused'&&!!value.canGoForward,error:value.state==='controlPaused'?{code:-32099,description:'Browser control is paused.'}:value.state==='presentationFailed'?{code:undefined,description:'Reload to continue.'}:undefined,sandboxEnabled:undefined},needsReload&&saved);return value};
  const fail=error=>{if(String(error).includes('CEF_BROKER_CONTROL_PAUSED')){if(view)accept({...view,state:'controlPaused'});return}console.error('Browser presentation failed',String(error));publish({...state,loading:false,error:{code:undefined,description:'The page is unavailable. Reload to continue.'}})};
  const geometry=()=>{const rect=container?.getBoundingClientRect();if(!rect)return{rect:{x:0,y:0,width:0,height:0},visible:false};const dock=container.closest?.('[data-dockkit-host="dock"]'),panel=dock?.closest('[data-sidebar-right-panel]'),collapsed=panel&&!panel.hasAttribute('data-sidebar-right-open');const left=Math.min(innerWidth,Math.max(0,rect.left)),top=Math.min(innerHeight,Math.max(0,rect.top)),right=Math.min(innerWidth,rect.right),bottom=Math.min(innerHeight,rect.bottom);return{rect:{x:left,y:top,width:Math.max(0,right-left),height:Math.max(0,bottom-top)},visible:mounted&&!collapsed&&intersecting&&!blocked&&container.isConnected&&container.getClientRects().length>0&&getComputedStyle(container).visibility!=='hidden'&&right>left&&bottom>top}};
  const enqueue=task=>{const result=queued.then(task);queued=result.catch(error=>{if(!disposed)fail(error)});return result};
  const update=(address='',explicit=false)=>{const generation=mountGeneration;return enqueue(async()=>{if(disposed)return;const g=geometry();lastGeometry=JSON.stringify(g);issued=true;let value=await request('update',{slot,address,...g});if(value.state==='NeedsReload'&&explicit&&generation===mountGeneration&&mounted){value=await request('recover',{slot,address});value=await request('update',{slot,address:'',...geometry()})}if(generation!==mountGeneration){await request('update',{slot,address:'',rect:g.rect,visible:false});return value}return accept(value)})};
  const page={key:options.key,visible:()=>mounted&&geometry().visible,select:()=>{if(!view?.lease||view.state!=='ready'||!geometry().visible)throw Error('Reload the visible page first');return request('select-control',{session,slot,lease:view.lease})},schedule(){if(disposed||!mounted)return;syncDialog();
   // ResizeObserver already follows layout frames. Timers are throttled in
   // covered WebViews, so dispatch directly and coalesce while native is busy.
   if(layoutQueued)return;const key=JSON.stringify(geometry());if(key===lastGeometry)return;lastGeometry=key;layoutQueued=true;void update().then(()=>{layoutQueued=false;page.schedule()},()=>{layoutQueued=false})
  }};pages.add(page);
  const polling=setInterval(()=>{if(disposed||!registered||pollQueued)return;pollQueued=true;void enqueue(async()=>accept(await request('state',{slot}))).then(()=>{pollQueued=false},()=>{pollQueued=false})},250);
  const frame={getSnapshot:()=>state,subscribe(listener){listeners.add(listener);return()=>listeners.delete(listener)},loadUrl(value){if(view?.state==='controlPaused')return;pendingURL=value.url;publish({...state,target:value,address:'requested',loading:true,error:undefined});if(mounted){const url=pendingURL;pendingURL=undefined;void update(url,true)}},goBack(){if(view?.state==='controlPaused')return;void enqueue(async()=>{await request('action',{slot,command:'back'});accept(await request('state',{slot}))})},goForward(){if(view?.state==='controlPaused')return;void enqueue(async()=>{await request('action',{slot,command:'forward'});accept(await request('state',{slot}))})},reload(){if(view?.state==='controlPaused')return;if(view?.state==='presentationFailed'){void update()}else if(view?.state==='NeedsReload'){void enqueue(async()=>{accept(await request('recover',{slot,address:''}));accept(await request('update',{slot,address:'',...geometry()}))})}else if(registered){void enqueue(async()=>{await request('action',{slot,command:'reload'});accept(await request('state',{slot}))})}else if(state.target){void update(state.target.url,true)}},async dispose(){if(disposed)return;clearDialog();disposed=true;mounted=false;mountGeneration++;pages.delete(page);clearInterval(polling);observer?.disconnect();intersection?.disconnect();await queued.catch(()=>{});if(issued)await request('detach',{slot});listeners.clear()}};
  const presentation={mount(id){container=document.getElementById(id);if(!container)throw Error('Browser viewport unavailable');mounted=true;const generation=++mountGeneration;intersecting=false;lastGeometry='';observer?.disconnect();intersection?.disconnect();observer=new ResizeObserver(()=>page.schedule());observer.observe(container);
   // Ancestor overflow/transforms can hide a retained pane without resizing
   // its viewport. A native child must not outlive that DOM clip. Keep it
   // hidden until the browser confirms the whole slot is visible again.
   intersection=new IntersectionObserver(entries=>{if(generation!==mountGeneration||!mounted)return;for(const entry of entries)if(entry.target===container){intersecting=entry.isIntersecting&&entry.intersectionRatio>=0.9999;page.schedule()}},{threshold:[0,0.9999]});intersection.observe(container);
   const scroll=()=>page.schedule();addEventListener('scroll',scroll,true);addEventListener('resize',scroll);if(pendingURL){const url=pendingURL;pendingURL=undefined;void update(url,true)}else{page.schedule()}return()=>{clearDialog();mounted=false;intersecting=false;mountGeneration++;lastGeometry='';observer?.disconnect();intersection?.disconnect();removeEventListener('scroll',scroll,true);removeEventListener('resize',scroll);void update()}}};
  return{frame,presentation};
 }};
})();
