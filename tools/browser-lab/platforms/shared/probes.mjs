import {randomUUID} from 'node:crypto';
import {readFileSync,copyFileSync} from 'node:fs';

// One Playwright owner for both actions and observation. These token-protected
// lab endpoints are fixed probes, not a new Agent evaluate capability.
export function batchOperations({getBrowser,tabs,hostState,root,session,refresh}) {
 const observations=new Map();let client,owner;
 const sleep=ms=>new Promise(r=>setTimeout(r,ms));
 const timeout=(p,ms)=>Promise.race([p,new Promise(r=>setTimeout(()=>r({timeout:true}),ms))]);
 async function cdp(){const b=getBrowser();if(owner!==b){client=await b.newBrowserCDPSession();owner=b}return client}
 async function targetList(){return (await(await cdp()).send('Target.getTargets')).targetInfos.filter(t=>t.type==='page')}
 async function handle(path,body){
  if(path==='/batch-start'){
   const id=randomUUID(),page=tabs.get('A').page;
   const item={id,kind:body.kind,page,targets:await targetList(),downloads:[]};
   item.listener=d=>item.downloads.push(d);if(body.kind==='download')page.on('download',item.listener);
   observations.set(id,item);return {id};
  }
  if(path==='/batch-finish'){
   const item=observations.get(body.id);if(!item)throw Error('Unknown observation');
   try{
    await sleep(900);
    if(item.kind==='popup'){
     const targets=await targetList(),added=targets.filter(t=>!item.targets.some(old=>old.targetId===t.targetId));
     const native=await hostState();const details=[];
     for(const t of added){const page=getBrowser().contexts().flatMap(c=>c.pages()).find(p=>p.url()===t.url&&t.url.includes('/popup'));if(page){try{details.push(await timeout(page.evaluate(()=>({url:location.href,title:document.title,openerPresent:opener!==null,wails:typeof window._wails,webview:typeof window.chrome?.webview,hostObjects:typeof window.chrome?.webview?.hostObjects})),1500))}catch(e){details.push({error:e.message})}}}
          return {addedTargets:added,documents:details,guestTopLevelWindows:native.guestTopLevelWindows,registeredAdapterTabs:[...tabs.values()].map(t=>({name:t.name,targetId:t.targetId}))};
    }
    const result=[];
    for(const d of item.downloads){const info={url:d.url(),suggestedFilename:d.suggestedFilename()};if(body.cancel){await d.cancel();info.canceledByLab=true}info.failure=await timeout(d.failure(),3000);if(info.failure===null){info.path=await d.path();info.savedWithinRuntime=info.path.replaceAll('\\','/').startsWith(session.dataRoot.replaceAll('\\','/')+'/');const text=readFileSync(info.path,'utf8');if(!text.startsWith('BROWSER_LAB isolated download'))throw Error('Unexpected fixture download');info.fixtureContent=text;copyFileSync(info.path,root+'evidence/'+body.name+'-download.txt')}result.push(info)}
    return {downloads:result};
   }finally{if(item.kind!=='popup'){item.page.off('download',item.listener);observations.delete(body.id)}}
  }
  if(path==='/popup-clean'){const item=observations.get(body.id);if(!item||item.kind!=='popup')throw Error('Unknown popup probe');for(const t of (await targetList()).filter(t=>!item.targets.some(old=>old.targetId===t.targetId)))await(await cdp()).send('Target.closeTarget',{targetId:t.targetId});observations.delete(body.id);await sleep(200);await refresh();return {closed:true}}
  if(path==='/download-mode'){if(!['default','deny'].includes(body.mode))throw Error('Invalid probe mode');await(await cdp()).send('Browser.setDownloadBehavior',{behavior:body.mode,eventsEnabled:true});return {mode:body.mode}}
  if(path==='/permissions')return tabs.get('A').page.evaluate(async()=>{const result={notificationDefault:Notification.permission,queried:{},notAccessed:['device streams','precise location','clipboard content']};for(const name of ['camera','microphone','geolocation','clipboard-read','notifications']){try{result.queried[name]=(await navigator.permissions.query({name})).state}catch(e){result.queried[name]={unsupported:String(e)}}}return result});
  if(path==='/permission-result')return tabs.get('A').page.evaluate(()=>({state:Notification.permission,request:window.lab.notificationRequest||null}));
  if(path==='/storage-get'||path==='/storage-set'){
   if(!/^browser_lab_[a-f0-9]+$/.test(body.key))throw Error('Invalid fixture key');const tab=tabs.get(body.tab||'A');if(!tab)throw Error('Invalid fixture tab');
   if(path==='/storage-set')await tab.page.evaluate(({key,value})=>{localStorage.setItem(key,value);document.cookie=key+'='+value+'; Path=/; Max-Age=3600; SameSite=Lax'},{key:body.key,value:String(body.value)});
   return tab.page.evaluate(key=>({localStorage:localStorage.getItem(key),cookie:document.cookie}),body.key);
  }
  throw Error('Unknown fixed batch probe');
 }
 return handle;
}
