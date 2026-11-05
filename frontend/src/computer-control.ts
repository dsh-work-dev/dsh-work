import {AutomationService} from '../bindings/github.com/local/dsh-work/internal/desktopclient';
import {applyAppearance} from './theme';
import {applyLocale,t} from './i18n';
export function mountComputerControl(pointer:boolean){
 for(const e of Array.from(document.body.children))if(e.tagName!=='SCRIPT')e.setAttribute('hidden','');
 document.body.classList.add(pointer?'computer-pointer-surface':'computer-control-surface');
 if(pointer){const img=document.createElement('img');img.src='/branding/agent-pointer.svg';img.alt='';img.className='computer-agent-pointer';document.body.append(img);void fetch('/branding/agent-pointer.json').then(r=>r.json()).then(meta=>{img.style.width=meta.width+'px';img.style.height=meta.height+'px'});return}
 const root=document.createElement('main'),text=document.createElement('strong'),app=document.createElement('span'),stop=document.createElement('button'),error=document.createElement('span');root.className='computer-control';stop.className='button button-primary';stop.type='button';error.setAttribute('role','status');root.append(text,app,stop,error);document.body.append(root);
 let current:Awaited<ReturnType<typeof AutomationService.GetComputerControl>>|undefined,loading=false,busy=false;
 stop.onclick=async()=>{if(!current?.state.use||busy)return;busy=true;stop.disabled=true;try{await AutomationService.StopComputerUse(current.state.use.session,current.state.use.lifetime);error.textContent=''}catch{error.textContent=t('error.saveSettings')}finally{busy=false;await refresh()}};
 async function refresh(){if(loading)return;loading=true;try{current=await AutomationService.GetComputerControl();applyLocale(current.locale);applyAppearance(current.appearance);const u=current.state.use;stop.textContent=t('automation.access.stop');text.textContent='DSH Work';root.title=u?.target?.title||u?.title||'';const state=u?.stopping?t('automation.computerAccess.stopping'):t(u?.running?'automation.computerAccess.operating':'automation.computerAccess.controlling');app.textContent=state+(u?.target?.app?' · '+u.target.app:'');if(current.state.failure)error.textContent=t('automation.computerAccess.cleanupFailed');stop.disabled=busy||!u;}catch{stop.disabled=true}finally{loading=false}}
 void refresh();const timer=setInterval(()=>void refresh(),250);window.addEventListener('pagehide',()=>clearInterval(timer));
}
