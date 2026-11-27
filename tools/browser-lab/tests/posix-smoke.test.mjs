import test from 'node:test';
import assert from 'node:assert/strict';
import {writeFileSync} from 'node:fs';
import {join,basename} from 'node:path';
import {startPosixSmoke} from '../platforms/posix-smoke.mjs';
import {createReport} from '../lib/report.mjs';
import {waitFor} from '../lib/context.mjs';
import {cases} from '../catalog.mjs';
const options=JSON.parse(process.env.BROWSER_LAB_OPTIONS||'{}');
if(!options.runId)throw Error('Use cli.mjs smoke');
const report=createReport(options.output,{runId:options.runId,layer:'native-posix-smoke',platform:process.platform,arch:process.arch,displayProtocol:process.platform==='linux'?'X11':'Cocoa',authorization:'Privileged lab CDP only; real Service is not exercised by this smoke'},'regression');
test('POSIX native minimum loop',{timeout:150000},async t=>{
 let a,startError;
 function finish(){for(const c of cases)if(!report.data.results.some(r=>r.id===c.id))report.record({id:c.id,status:'not-selected',reason:'This is the native minimum-loop smoke, not full platform qualification.'});report.finish()}
 t.after(async()=>{if(a)try{await a.close();report.record({id:'native.close',status:'pass',evidence:{artifact:'cleanup.json'}})}catch(e){report.record({id:'native.close',status:'fail',error:String(e.stack||e)});throw e}finally{finish()};if(!a)finish()});
 try{a=await startPosixSmoke(options);report.data.environment.build=a.build}catch(e){startError=e}
 for(const id of ['native.composition','content.localhost'])await t.test(id,async()=>{
  try{
   if(startError)throw startError;
   if(id==='native.composition'){
    let capture,markers;await waitFor(async()=>{capture=await a.capture(id);markers=a.markers(capture);return markers.shellPixels>=50&&markers.guestPixels>=50},'host and live guest native landmarks',10000);
    const state=await a.call('/state');assert(state.parentAttached);report.record({id,status:'pass',evidence:{state,markers,artifact:basename(capture)}});
   }else{
    const before=await a.page.evaluate(()=>lab.instance);await a.page.locator('#entry').fill('CI same-page '+options.runId);await a.page.locator('#apply').click();
    const after=await a.page.evaluate(()=>({instance:lab.instance,result:document.querySelector('#result').textContent}));assert.equal(after.instance,before);assert.equal(after.result,'CI same-page '+options.runId);
    report.record({id,status:'pass',evidence:{before,after,artifact:basename(await a.capture(id))}});
   }
  }catch(e){report.record({id,status:'fail',error:String(e.stack||e)});throw e}
 });
});
