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
 let id='',busy=false,loading=false;
 async function answer(choice:string){if(!id||busy)return;busy=true;for(const b of [deny,always,allow])b.disabled=true;try{await AutomationService.AnswerBrowserPermission(id,choice);error.textContent=''}catch{error.textContent=t('automation.access.expired')}finally{busy=false;void refresh()}}
 deny.onclick=()=>void answer('deny');allow.onclick=()=>void answer('allow');always.onclick=()=>void answer('always');
 document.addEventListener('keydown',e=>{if(e.key==='Escape'){e.preventDefault();void answer('deny')}});
 async function refresh(){if(loading||busy)return;loading=true;try{
  const panel=await AutomationService.GetBrowserPermission();applyLocale(panel.locale);applyAppearance(panel.appearance);const r=panel.request;const changed=id!==(r?.id??'');id=r?.id??'';
  const expired=!r||Date.parse(r.expires)<=Date.now(),connecting=r?.phase==='connecting';
  deny.textContent=t(connecting?'automation.browser.cancel':'automation.access.deny');allow.textContent=t('automation.access.allow');always.textContent=t('automation.access.always');
  for(const b of [deny,allow,always])b.disabled=expired||busy;
  always.hidden=!r?.allowAlways||connecting;allow.hidden=connecting;
  if(!r){title.textContent=t('automation.permission.empty');body.textContent=destination.textContent=detail.textContent='';return}
  title.textContent=t(connecting?'automation.access.connecting':r.kind==='action'?'automation.permission.action':r.kind==='site'?'automation.access.siteTitle':'automation.access.browserTitle');
  body.textContent=r.kind==='action'?`${r.title} · ${t('automation.access.category.'+r.category)}`:t(r.kind==='site'?'automation.access.siteBody':'automation.access.browserBody',{title:r.title});
  destination.textContent=r.kind==='browser'?`${r.browser==='edge'?'Edge':'Chrome'} · ${r.profile}`:r.site;
  detail.textContent=r.kind==='action'?(r.action??''):t(r.kind==='site'?'automation.access.siteWarning':'automation.access.warning');
  if(changed){error.textContent='';deny.focus()}
 }catch{for(const b of [deny,allow,always])b.disabled=true;error.textContent=t('error.loadSettings')}finally{loading=false}}
 void refresh();const timer=setInterval(()=>void refresh(),400);window.addEventListener('pagehide',()=>clearInterval(timer));
}
