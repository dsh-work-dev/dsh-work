import {randomUUID} from 'node:crypto';
export const sleep = ms => new Promise(r => setTimeout(r, ms));
export async function waitFor(check, label, ms = 15000) {
 for (const end=Date.now()+ms;Date.now()<end;) {const result=await check();if(result)return result;await sleep(100)}
 throw Error('Timeout: '+label);
}

export function makeContext(adapter, options) {
 const ctx={adapter,options,caseId:'',base:null,consentError:null,consentEvents:[],
  async prepare(id) {
   this.caseId=id;this.base=null;
   const s=await adapter.state();if(!s.running||!s.ready)throw Error('Lab not healthy before '+id);
   if(s.covered)await adapter.command('uncover');
   await adapter.command('dock');await adapter.command('tab',{tab:'A'});
   for(const tab of ['A','B'])await adapter.call('backend','/display',{tab,url:adapter.session.endpoints.fixture+'/interactions?tab='+tab});
  },
  async begin() {
   const b=await adapter.call('gateway','/begin',{session:'browser-lab:'+this.caseId,lifetime:'life-'+randomUUID(),turn:'turn-'+randomUUID()});this.base=b.request;return b;
  },
  async request(tool='browser_type',selector='#entry',args={}) {
   if(!this.base)await this.begin();
   const observed=await adapter.call('backend','/observe',{selector});
   return {...this.base,tool,arguments:{target:observed.ref,...args},permit:undefined};
  },
  async permit(req) {
   const d=await adapter.call('gateway','/authorize',req);
   if(!d.permit||!['allow','ask'].includes(d.kind))throw Error('No real gateway permit');
   this.base.epoch=d.epoch;return {...req,epoch:d.epoch,permit:d.permit};
  },
  async action(tool,selector,args={}) {return this.execute(await this.permit(await this.request(tool,selector,args)))},
  execute(req){return adapter.call('gateway','/execute',req)},
  async denied(req) {
   const before=await adapter.stats(),r=await adapter.api('gateway','/execute',req),after=await adapter.stats();
   if(r.ok||before.dispatches!==after.dispatches)throw Error('Rejected request caused a dispatch');return r;
  },
  async hold(req) {
   const before=await adapter.stats(),operation=adapter.api('gateway','/execute',req);
   await waitFor(async()=>{const s=await adapter.stats();return s.events.slice(before.events.length).some(e=>e.event==='held-before-dispatch')},'pre-dispatch hold');return {before,operation};
  },
  async revoke() {
   if(this.base){
    const scope={session:this.base.session,lifetime:this.base.lifetime},r=await adapter.api('gateway','/revoke',scope);
    if(!r.ok){
     if(r.value?.error!=='AUTOMATION_REVOKED')throw Error('gateway/revoke: '+JSON.stringify(r.value));
     const state=await adapter.call('gateway','/state');
     if(!Array.isArray(state.browserAccess?.sessions)||state.browserAccess.sessions.some(s=>s.session===scope.session&&s.lifetime===scope.lifetime))throw Error('Revoked scope is still active or cannot be verified');
     this.releaseEvents.push({...scope,reason:'already-invalidated',generation:state.generation});
    }
   }
   this.base=null;
  },
  async cover(name) {
   const s=await adapter.call('backend','/cover',{}),owner=s.floating?adapter.float:adapter.main;
   await owner.waitForFunction(epoch=>{const c=document.querySelector('#cover'),p=document.querySelector('#placeholder');return !c.hidden&&p.complete&&p.naturalWidth>0&&p.dataset.epoch===String(epoch)},s.presentationEpoch,{timeout:7000});
   const path=await adapter.capture(name,s.floating?'float':'main');return {state:adapter.normalize(s),path};
  },
  async uncover() {
   const s=await adapter.state(),owner=s.floating?adapter.float:adapter.main;
   await owner.locator('#close-cover').click();await waitFor(async()=>{const s=await adapter.state();return !s.covered&&s.views.find(t=>t.name===s.selected).visible},'native guest restored');
  }
 };
 ctx.releaseEvents=[];
 let busy=false;
 const timer=options.consent==='scripted'?setInterval(async()=>{
  if(busy||ctx.consentError)return;busy=true;
  try{
   const s=await adapter.call('gateway','/state');
   for(const r of s.browserAccess?.requests||[]) {
    if(r.phase!=='waiting')continue;
    const siteAllowed=['','127.0.0.1','localhost',...(options.network?['example.com']:[])].includes(r.site||'');
    if(r.session!=='browser-lab:'+ctx.caseId||r.title!=='Browser Lab isolated fixture'||!siteAllowed)throw Error('Refusing scripted consent outside current fixture scope');
    const row=adapter.main.locator('[data-consent-id="'+r.id+'"]');
    await row.locator('[data-choice="allow"]').click({timeout:5000});
    ctx.consentEvents.push({caseId:ctx.caseId,kind:r.kind,site:r.site,choice:'allow',channel:adapter.consentChannel||'trusted-Wails-CDP-fixture'});
   }
  }catch(e){ctx.consentError=String(e.stack||e)}finally{busy=false}
 },150):null;
 ctx.stopConsent=()=>{if(timer)clearInterval(timer)};
 return ctx;
}
