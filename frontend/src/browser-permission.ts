import {AutomationService} from '../bindings/github.com/local/dsh-work/internal/desktopclient';
import {applyLocale,t} from './i18n';
import {applyAppearance} from './theme';

// Dedicated top-level native window: never loads DSH or its approval service.
export function mountBrowserPermission(){
 for(const e of Array.from(document.body.children))if(e.tagName!=='SCRIPT')e.setAttribute('hidden','');
 const root=document.createElement('main');root.className='browser-permission';
 const brand=document.createElement('p');brand.className='note';brand.textContent='DSH Work';
 const title=document.createElement('h1'),body=document.createElement('p'),destination=document.createElement('p'),detail=document.createElement('p'),error=document.createElement('p'),actions=document.createElement('div');
 destination.className='mono';detail.className='note';error.setAttribute('role','alert');actions.className='actions';
 const deny=document.createElement('button'),always=document.createElement('button'),allow=document.createElement('button');
 for(const button of [deny,always,allow]){button.type='button';button.className='button'}allow.classList.add('button-primary');
 deny.id='browser-permission-deny';allow.id='browser-permission-allow';always.id='browser-permission-always';
 actions.append(deny,always,allow);root.append(brand,title,body,destination,detail,error,actions);document.body.append(root);
 let id='',busy=false,loading=false,renderRevision=0,reported='',reporting='';
 const invalidate=()=>{renderRevision++;reported=''};
 async function reportRendered(request:string,revision:number){
  if(reported===request||reporting===request)return;reporting=request;
  try{
   await document.fonts.ready;
   await new Promise<void>(resolve=>requestAnimationFrame(()=>resolve()));
   await new Promise<void>(resolve=>requestAnimationFrame(()=>resolve()));
   if(id!==request||renderRevision!==revision||busy||error.textContent||document.visibilityState!=='visible'||deny.disabled||allow.disabled||allow.hidden)return;
   if(![title,body,destination,deny,allow].every(node=>{const r=node.getBoundingClientRect();return node.getClientRects().length>0&&r.width>0&&r.height>0&&r.top>=0&&r.bottom<=innerHeight}))return;
   await AutomationService.ReportBrowserPermissionRendered(request);
   if(id===request&&renderRevision===revision)reported=request;
  }catch{/* Readiness is retried by the next successful current-request render. */}
  finally{if(reporting===request)reporting=''}
 }
 async function answer(choice:string){if(!id||busy)return;busy=true;for(const b of [deny,always,allow])b.disabled=true;try{await AutomationService.AnswerBrowserPermission(id,choice);error.textContent=''}catch{error.textContent=t('automation.access.expired')}finally{busy=false;void refresh()}}
 deny.onclick=()=>void answer('deny');allow.onclick=()=>void answer('allow');always.onclick=()=>void answer('always');
 document.addEventListener('keydown',e=>{if(e.key==='Escape'){e.preventDefault();void answer('deny')}});
 async function refresh(){if(loading||busy)return;loading=true;try{
  const panel=await AutomationService.GetBrowserPermission();applyLocale(panel.locale);applyAppearance(panel.appearance);const c=panel.computerRequest;if(c){const changed=id!==c.id;id=c.id;invalidate();title.textContent=t('automation.computerAccess.title');body.textContent=t('automation.computerAccess.body',{title:c.title});destination.textContent=`${c.target.app} · ${c.target.title}`;detail.textContent=t('automation.computerAccess.level.'+c.target.level)+(c.target.warning?' · '+t('automation.computerAccess.warning'):'');deny.textContent=t('automation.access.deny');allow.textContent=t('automation.access.allow');always.hidden=false;always.textContent=t("automation.access.always");allow.hidden=false;deny.disabled=allow.disabled=always.disabled=Date.parse(c.expires)<=Date.now();if(changed){error.textContent='';deny.focus()}return}
  const r=panel.request;const changed=id!==(r?.id??'');id=r?.id??'';if(changed)invalidate();
  const expired=!r||Date.parse(r.expires)<=Date.now(),connecting=r?.phase==='connecting';
  if(expired||connecting)invalidate();
  deny.textContent=t(connecting?'automation.browser.cancel':'automation.access.deny');allow.textContent=t('automation.access.allow');always.textContent=t('automation.access.always');
  for(const b of [deny,allow,always])b.disabled=expired||busy;
  always.hidden=!r?.allowAlways||connecting;allow.hidden=connecting;
  if(!r){title.textContent=t('automation.permission.empty');body.textContent=destination.textContent=detail.textContent='';return}
  const embedded=r.browser==='embedded',conversation=embedded&&r.kind==='browser';
  title.textContent=t(connecting?'automation.access.connecting':r.kind==='action'?'automation.permission.action':r.kind==='site'?'automation.access.siteTitle':conversation?'automation.access.embeddedTitle':'automation.access.browserTitle');
  body.textContent=r.kind==='action'?`${r.title} · ${t('automation.access.category.'+r.category)}`:t(r.kind==='site'?'automation.access.siteBody':conversation?'automation.access.embeddedBody':'automation.access.browserBody',{title:r.title});
  destination.textContent=r.kind==='browser'?(embedded?t('automation.access.embeddedBrowser'):`${r.browser==='edge'?'Edge':'Chrome'} · ${r.profile}`):r.site;
  detail.textContent=r.kind==='action'?(r.action??''):t(r.kind==='site'?'automation.access.siteWarning':conversation?'automation.access.embeddedWarning':'automation.access.warning');
  if(changed){error.textContent='';deny.focus()}
  if(!expired&&!connecting&&!error.textContent)void reportRendered(r.id,renderRevision);
 }catch{invalidate();for(const b of [deny,allow,always])b.disabled=true;error.textContent=t('error.loadSettings')}finally{loading=false}}
 void refresh();const timer=setInterval(()=>void refresh(),400);window.addEventListener('pagehide',()=>{id='';invalidate();clearInterval(timer)});
}
