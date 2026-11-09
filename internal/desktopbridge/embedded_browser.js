// Optional BrowserPage provider for the exact runtime projection. It owns only
// presentation intent and observable toolbar state; Go owns native authority.
(() => {
 globalThis.__DSH_WORK_EMBEDDED_VERSION__='0.2.2-rc.1';
 const shellOrigin=location.ancestorOrigins?.[0];if(!shellOrigin||shellOrigin==='null'||parent===window)return;
 const documentToken=decodeURIComponent(location.hash.slice(1).match(/(?:^|&)__dsh_work_doc=([^&]+)/)?.[1]||'');
 const pending=new Map();let blocked=false;
 const pages=new Set();
 function request(action,data={}){return new Promise((resolve,reject)=>{const id=crypto.randomUUID();const timer=setTimeout(()=>{pending.delete(id);reject(Error('Browser request timed out'))},12000);pending.set(id,{resolve,reject,timer});parent.postMessage({version:1,type:'dsh-work/browser',id,action,document:documentToken,...data},shellOrigin)})}
 addEventListener('message',event=>{if(event.source!==parent||event.origin!==shellOrigin)return;const m=event.data;if(m?.version!==1)return;if(m.type==='dsh-work/browser-blocked'){blocked=!!m.blocked;for(const page of pages)page.schedule();return}if(m.type!=='dsh-work/browser-result')return;const held=pending.get(m.id);if(!held)return;pending.delete(m.id);clearTimeout(held.timer);m.error?held.reject(Error(m.error)):held.resolve(m.value)});
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
 const modalObserver=new MutationObserver(scanModals);modalObserver.observe(document.documentElement,{subtree:true,childList:true,attributes:true,attributeFilter:['role','aria-modal','data-shortcut-modal','hidden','open','class','style']});
 addEventListener('DOMContentLoaded',scanModals,{once:true});
 const target=url=>{try{const parsed=new URL(url);if(!['http:','https:'].includes(parsed.protocol))return;return{kind:parsed.protocol.slice(0,-1),url:parsed.href,title:parsed.hostname}}catch{}};
 globalThis.__DSH_WORK_BROWSER_PROVIDER__={protocolVersion:1,async selectPage(key){
  const candidates=[...pages].filter(page=>page.key===key&&page.visible());if(candidates.length!==1)throw Error('Visible browser page required');
  return candidates[0].select();
 },createPage(session,options){
  const slot=`${session}/${options.key}`;let container,observer,intersection,intersecting=false,disposed=false,mounted=false,registered=false,issued=false,mountGeneration=0,state={target:undefined,address:'empty',loading:false,canGoBack:false,canGoForward:false,error:undefined,sandboxEnabled:undefined};
  let view,queued=Promise.resolve(),timer,pendingURL,revision=0,lastGeometry='',persistedKey='';const listeners=new Set();
  const publish=(next,force=false)=>{if(!force&&JSON.stringify(state)===JSON.stringify(next))return;state=next;for(const listener of listeners)listener()};
  const accept=value=>{view=value;registered=value.state!=='empty';const page=target(value.url),needsReload=value.state==='NeedsReload';if(page&&value.title)page.title=value.title;
   // A safe descriptor is not a live frame. Feed the existing DSH restore
   // checkpoint before notifying so its standard title/address/action render
   // even when the provider initially has the same empty frame snapshot.
   let saved=false;if(page&&['ready','NeedsReload'].includes(value.state)&&persistedKey!==page.url+'|'+page.title){persistedKey=page.url+'|'+page.title;options.persist({entries:[page],index:0,request:undefined,navigation:{status:'known',revision:++revision},failure:undefined});saved=true}
   publish({target:needsReload?undefined:page,address:!needsReload&&page?'observed':'empty',loading:value.state==='loading',canGoBack:value.state!=='controlPaused'&&!!value.canGoBack,canGoForward:value.state!=='controlPaused'&&!!value.canGoForward,error:value.state==='controlPaused'?{code:-32099,description:'Browser control is paused.'}:value.state==='presentationFailed'?{code:undefined,description:'Reload to continue.'}:undefined,sandboxEnabled:undefined},needsReload&&saved);return value};
  const fail=error=>{if(String(error).includes('CEF_BROKER_CONTROL_PAUSED')){if(view)accept({...view,state:'controlPaused'});return}console.error('Browser presentation failed',String(error));publish({...state,loading:false,error:{code:undefined,description:'The page is unavailable. Reload to continue.'}})};
  const geometry=()=>{const rect=container?.getBoundingClientRect();if(!rect)return{rect:{x:0,y:0,width:0,height:0},visible:false};const left=Math.min(innerWidth,Math.max(0,rect.left)),top=Math.min(innerHeight,Math.max(0,rect.top)),right=Math.min(innerWidth,rect.right),bottom=Math.min(innerHeight,rect.bottom);return{rect:{x:left,y:top,width:Math.max(0,right-left),height:Math.max(0,bottom-top)},visible:mounted&&intersecting&&!blocked&&container.isConnected&&container.getClientRects().length>0&&getComputedStyle(container).visibility!=='hidden'&&right>left&&bottom>top}};
  const enqueue=task=>{const result=queued.then(task);queued=result.catch(error=>{if(!disposed)fail(error)});return result};
  const update=(address='',explicit=false)=>{const generation=mountGeneration;return enqueue(async()=>{if(disposed)return;const g=geometry();issued=true;let value=await request('update',{slot,address,...g});if(value.state==='NeedsReload'&&explicit&&generation===mountGeneration&&mounted){value=await request('recover',{slot,address});value=await request('update',{slot,address:'',...geometry()})}if(generation!==mountGeneration){await request('update',{slot,address:'',rect:g.rect,visible:false});return value}return accept(value)})};
  const page={key:options.key,visible:()=>mounted&&geometry().visible,select:()=>{if(!view?.lease||view.state!=='ready'||!geometry().visible)throw Error('Reload the visible page first');return request('select-control',{session,slot,lease:view.lease})},schedule(){if(disposed||!mounted)return;clearTimeout(timer);timer=setTimeout(()=>{const g=geometry(),key=JSON.stringify(g);if(key===lastGeometry)return;lastGeometry=key;void update()},25)}};pages.add(page);
  const polling=setInterval(()=>{if(disposed||!registered)return;void enqueue(async()=>accept(await request('state',{slot})))},700);
  const frame={getSnapshot:()=>state,subscribe(listener){listeners.add(listener);return()=>listeners.delete(listener)},loadUrl(value){if(view?.state==='controlPaused')return;pendingURL=value.url;publish({...state,target:value,address:'requested',loading:true,error:undefined});if(mounted){const url=pendingURL;pendingURL=undefined;void update(url,true)}},goBack(){if(view?.state==='controlPaused')return;void enqueue(async()=>{await request('action',{slot,command:'back'});accept(await request('state',{slot}))})},goForward(){if(view?.state==='controlPaused')return;void enqueue(async()=>{await request('action',{slot,command:'forward'});accept(await request('state',{slot}))})},reload(){if(view?.state==='controlPaused')return;if(view?.state==='presentationFailed'){void update()}else if(view?.state==='NeedsReload'){void enqueue(async()=>{accept(await request('recover',{slot,address:''}));accept(await request('update',{slot,address:'',...geometry()}))})}else if(registered){void enqueue(async()=>{await request('action',{slot,command:'reload'});accept(await request('state',{slot}))})}else if(state.target){void update(state.target.url,true)}},async dispose(){if(disposed)return;disposed=true;mounted=false;mountGeneration++;pages.delete(page);clearInterval(polling);clearTimeout(timer);observer?.disconnect();intersection?.disconnect();await queued.catch(()=>{});if(issued)await request('detach',{slot});listeners.clear()}};
  const presentation={mount(id){container=document.getElementById(id);if(!container)throw Error('Browser viewport unavailable');mounted=true;const generation=++mountGeneration;intersecting=false;lastGeometry='';observer?.disconnect();intersection?.disconnect();observer=new ResizeObserver(()=>page.schedule());observer.observe(container);
   // Ancestor overflow/transforms can hide a retained pane without resizing
   // its viewport. A native child must not outlive that DOM clip. Keep it
   // hidden until the browser confirms the whole slot is visible again.
   intersection=new IntersectionObserver(entries=>{if(generation!==mountGeneration||!mounted)return;for(const entry of entries)if(entry.target===container){intersecting=entry.isIntersecting&&entry.intersectionRatio>=0.9999;page.schedule()}},{threshold:[0,0.9999]});intersection.observe(container);
   const scroll=()=>page.schedule();addEventListener('scroll',scroll,true);if(pendingURL){const url=pendingURL;pendingURL=undefined;void update(url,true)}else{void update()}page.schedule();return()=>{mounted=false;intersecting=false;mountGeneration++;lastGeometry='';observer?.disconnect();intersection?.disconnect();removeEventListener('scroll',scroll,true);void update()}}};
  return{frame,presentation};
 }};
})();
