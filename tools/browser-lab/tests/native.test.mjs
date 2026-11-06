import test from 'node:test';
import {readFileSync,writeFileSync,existsSync} from 'node:fs';
import {join} from 'node:path';
import {cases,validateCatalog} from '../catalog.mjs';
import {createReport,requireManualResult} from '../lib/report.mjs';
import {makeContext} from '../lib/context.mjs';
import {nativeCases} from '../cases/native.mjs';
import {platformSupport} from '../platforms/index.mjs';

const options=JSON.parse(process.env.BROWSER_LAB_OPTIONS||'{}');
if(!options.runId||!options.output)throw Error('Use cli.mjs run to create an isolated session');
const support=platformSupport();
const report=createReport(options.output,{runId:options.runId,layer:'native',platform:process.platform,arch:process.arch,node:process.version,consent:options.consent,syntheticAdmission:true},options.profile);

test('Browser Lab native requirements',{timeout:600000},async t=>{
 let adapter,ctx,startError;
 // Node versions may resolve t.test() when queued, before its body finishes.
 // The runner's after hook owns cleanup, after every registered child settles.
 t.after(async()=>{
  if(ctx){ctx.stopConsent();report.data.scriptedConsent=ctx.consentEvents;report.data.releaseEvents=ctx.releaseEvents}
  if(adapter){try{report.data.cleanup=await adapter.close()}catch(e){report.data.cleanupError=String(e.stack||e);report.record({id:'harness.cleanup',status:'fail',error:report.data.cleanupError})}}
  if(report.data.releaseError)report.record({id:'harness.release',status:'fail',error:report.data.releaseError});
  for(const item of cases)if(!report.data.results.some(r=>r.id===item.id))report.record({id:item.id,status:'blocked',reason:'Runner interrupted before this case completed'});
  report.finish();
  const manual={runId:options.runId,candidate:adapter?.session.build,results:cases.filter(c=>c.manual).map(c=>({id:c.id,status:'manual-required',operator:'',at:'',observation:''}))};
  writeFileSync(join(options.output,'manual-checklist.json'),JSON.stringify(manual,null,2));
  if(report.data.cleanupError||report.data.releaseError)throw Error('Native harness cleanup/release failed: '+(report.data.cleanupError||report.data.releaseError));
 });
 try{
  if(!support.implemented)throw Error(support.reason);
  const {startNative}=await import(new URL(support.module,new URL('../platforms/',import.meta.url)));
  adapter=await startNative(options);ctx=makeContext(adapter,options);
  report.data.environment.build=adapter.session.build;
  report.data.environment.runtime=await adapter.call('backend','/version');
 }catch(e){startError=String(e.stack||e);report.data.startupError=startError}
  for(const item of validateCatalog())await t.test(item.id+' — '+item.title,{timeout:90000},async st=>{
   const started=Date.now();
   function skip(status,reason){report.record({id:item.id,status,reason,durationMs:Date.now()-started});st.skip(reason)}
   if(options.selectedCases&&!options.selectedCases.includes(item.id)){skip('not-selected','Not selected by --case; not a full-suite run.');return}
   if(item.manual){
    if(options.manualEvidence){
     try{const input=JSON.parse(readFileSync(options.manualEvidence,'utf8'));
      const result=requireManualResult(input,options.runId,item.id,adapter?.session.build);report.record(result);if(result.status==='fail')throw Error(result.observation);if(result.status==='blocked')st.skip(result.observation);return;
     }catch(e){if(!report.data.results.some(r=>r.id===item.id))report.record({id:item.id,status:'fail',error:String(e.message)});throw e}
    }
    skip('manual-required','Real keyboard/IME or physical mixed-DPI evidence is required.');return;
   }
   if(item.unimplemented){skip('unimplemented','Native permission/download lifecycle is not implemented; not a passing test.');return}
   if(item.optional&&!options.network){skip(options.profile==='qualification'?'blocked':'not-selected','External HTTPS probe requires --network.');return}
   if(startError){skip('blocked',startError);return}
   if(!adapter.capabilities.has(item.capability)){skip('blocked','Adapter does not provide '+item.capability);return}
   let result;
   try{
    await ctx.prepare(item.id);result=await (adapter.caseOverrides?.[item.id]||nativeCases[item.id])(ctx);
    if(ctx.consentError)throw Error(ctx.consentError);
    if(result?.gap){
     if(result.gap!==item.knownGap&&result.gap!==item.platformGaps?.[process.platform])throw Error('Unexpected requirement violation: '+result.gap);
     report.record({id:item.id,status:'known-gap',gap:result.gap,evidence:result.evidence,durationMs:Date.now()-started});st.skip('Observed known gap: '+result.gap);return;
    }
    if((process.platform==='win32'&&item.knownGap)||item.platformGaps?.[process.platform])throw Error('Known gap no longer reproduced; review its platform baseline');
    report.record({id:item.id,status:'pass',evidence:result?.evidence,durationMs:Date.now()-started});
   }catch(e){report.record({id:item.id,status:'fail',error:String(e.stack||e),durationMs:Date.now()-started});throw e}
   finally{
    try{await ctx.revoke()}catch(e){report.data.releaseError=String(e.message)}
   }
  });
});
