// HTTP discovery can precede Playwright's corresponding page objects. Wait for
// the exact discovered pair, not one snapshot of its asynchronous page cache.
export async function discoverCEFTargets(browser,expectedIds,observe,{timeout=15000}={}) {
 const sessions=new Map(),expected=new Set(expectedIds),started=Date.now(),deadline=started+timeout;
 let complete=false;
 try{
  if(expected.size!==2||expectedIds.length!==2)throw Error('Expected two distinct discovered CEF targets');
  while(Date.now()<deadline){
   const pages=browser.contexts().flatMap(context=>context.pages());
   for(const [page,value] of sessions)if(!pages.includes(page)){await value.cdp.detach();sessions.delete(page)}
   for(const page of pages)if(!sessions.has(page)){
    const cdp=await page.context().newCDPSession(page);
    sessions.set(page,{page,cdp});
    await cdp.send('Page.enable');
    const info=(await cdp.send('Target.getTargetInfo')).targetInfo;
    sessions.get(page).info=info;
   }
   const candidates=[...sessions.values()];
   observe({elapsedMs:Date.now()-started,expectedTargetIds:[...expected],pages:candidates.map(c=>({targetId:c.info.targetId,cachedUrl:c.page.url()}))});
   if(candidates.length>2||candidates.some(c=>!expected.has(c.info.targetId))||new Set(candidates.map(c=>c.info.targetId)).size!==candidates.length)throw Error('Unexpected or duplicate CEF page targets');
   if(candidates.length===2){complete=true;return candidates}
   await new Promise(resolve=>setTimeout(resolve,100));
  }
  throw Error('Timeout: exact discovered CEF page targets did not attach');
 }finally{
  if(!complete)await Promise.allSettled([...sessions.values()].map(value=>value.cdp.detach()));
 }
}
