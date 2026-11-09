import test from 'node:test';
import assert from 'node:assert/strict';
import {mkdtempSync,readFileSync,rmSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {cases,validateCatalog,catalogVersion} from '../catalog.mjs';
import {nativeCases} from '../cases/native.mjs';
import {assessResults,createReport,evidencePath,requireManualResult} from '../lib/report.mjs';
import {platformSupport,validateSession} from '../platforms/index.mjs';
import {createFixtureServer} from '../fixtures/server.mjs';
import {evaluateIME,evaluateMixedDPI} from '../lib/manual.mjs';
import {makeContext} from '../lib/context.mjs';
import https from 'node:https';
import {createInvalidTLSFixture} from '../fixtures/invalid-tls.mjs';

test('TLS negative fixture: default verification rejects its own certificate before HTTP',async()=>{
 const fixture=await createInvalidTLSFixture();
 const request=options=>new Promise((resolve,reject)=>{
  const req=https.get(fixture.url,options,res=>{res.resume();res.once('end',()=>resolve(res.statusCode))});
  req.setTimeout(3000,()=>req.destroy(Error('TLS fixture timeout')));req.once('error',reject);
 });
 try{
  await assert.rejects(request({}),error=>error.code==='DEPTH_ZERO_SELF_SIGNED_CERT');
  assert(fixture.audit.connections>0);assert.equal(fixture.audit.requests,0);
  // Trust only this fixture in this separate control client, proving that the
  // listener is functional. No browser or global TLS configuration is changed.
  assert.equal(await request({ca:readFileSync(new URL('../fixtures/tls-untrusted-cert.pem',import.meta.url))}),200);
  assert.equal(fixture.audit.requests,1);
 }finally{await fixture.close()}
});

test('catalog: stable unique IDs, every automatic requirement has an executable case',()=>{
 assert.equal(validateCatalog().length,cases.length);
 for(const item of cases)if(!item.manual&&!item.unimplemented)assert.equal(typeof nativeCases[item.id],'function',item.id);
 assert.throws(()=>validateCatalog([cases[0],cases[0]]),/duplicate/);
 assert.throws(()=>validateCatalog([{id:'broken',title:'x',capability:'browser',knownGap:'missing'}]),/Unregistered/);
});
test('runner lifecycle: cleanup waits for actual child completion',async t=>{
 const completed=[];
 t.after(()=>assert.deepEqual(completed,[1,2]));
 for(const value of [1,2])await t.test('settled child '+value,async()=>{await new Promise(resolve=>setTimeout(resolve,20));completed.push(value)});
});
test('qualification: fail, blocked, known gaps and all-skipped cannot become green',()=>{
 assert.equal(assessResults([],'regression').exitCode,2);
 assert.equal(assessResults([{status:'manual-required'}],'regression').exitCode,2);
 assert.equal(assessResults([{status:'pass'},{status:'blocked'}],'regression').exitCode,2);
 assert.equal(assessResults([{status:'known-gap'}],'regression').exitCode,0);
 assert.equal(assessResults([{status:'known-gap'}],'qualification').exitCode,2);
 assert.equal(assessResults([{status:'pass'},{status:'manual-required'}],'qualification').qualification,'not-ready');
 assert.equal(assessResults([{status:'pass'},{status:'fail'}],'regression').exitCode,1);
 assert.equal(assessResults([{status:'pass'}],'qualification').qualification,'ready');
});
test('report: one result per ID, bounded evidence path, explicit non-ready summary',()=>{
 const dir=mkdtempSync(join(tmpdir(),'browser-lab-contract-'));
 try{const report=createReport(dir,{runId:'contract-only',layer:'contract'},'regression');report.record({id:'a',status:'known-gap'});assert.throws(()=>report.record({id:'a',status:'pass'}),/Duplicate/);assert.throws(()=>evidencePath(dir,'../outside.png'),/escapes/);assert.throws(()=>evidencePath(dir,'.'),/escapes/);assert.equal(report.finish().qualification,'not-ready');assert.equal(JSON.parse(readFileSync(join(dir,'report.json'))).catalogVersion,catalogVersion)}finally{rmSync(dir,{recursive:true})}
});
test('manual evidence cannot be silently reused from a prior run',()=>{
 const value={runId:'this-run',results:[{id:'input.native-ime',status:'pass',operator:'operator',at:'2026-11-06T00:00:00Z',observation:'Actual composition events and correct focus in docked/floating input'}]};
 assert.equal(requireManualResult(value,'this-run','input.native-ime').status,'pass');
 assert.throws(()=>requireManualResult(value,'another-run','input.native-ime'),/another run/);
 assert.throws(()=>requireManualResult({...value,results:[{id:'input.native-ime',status:'pass'}]},'this-run','input.native-ime'),/requires/);
});
test('platform/session: no macOS or Linux placeholder successes, no remote control endpoint',()=>{
 for(const platform of ['win32','darwin','linux']){assert(platformSupport(platform).implemented);assert(platformSupport(platform).module)}assert(!platformSupport('freebsd').implemented);
 const endpoints={fixture:'http://127.0.0.1:19478',cef:'http://127.0.0.1:19479',shell:'http://127.0.0.1:19480',control:'http://127.0.0.1:19481',gateway:'http://127.0.0.1:19482',backend:'http://127.0.0.1:19483',artifact:'http://127.0.0.1:19484'};
 const session={schemaVersion:1,runId:'test',dataRoot:'/private',evidenceRoot:'/evidence',token:'x'.repeat(32),endpoints};assert.equal(validateSession(session),session);assert.throws(()=>validateSession({...session,endpoints:{...endpoints,backend:'https://remote.example'}}),/Unexpected/);
});
test('physical evidence: paste/synthetic events and single-scale moves do not qualify',()=>{
 const time=Date.now(),events=[{type:'compositionstart',trusted:true,time},{type:'compositionend',trusted:true,time,data:'中文'}];
 const documents={A:{value:'中文甲',events},B:{value:'中文乙',events}},host={value:'宿主中文',events};
 const samples=[{time,views:[{name:'A',focused:true},{name:'B',focused:true}],hostInputFocused:true}];
 assert(evaluateIME(documents,host,samples).passed);
 assert(!evaluateIME({...documents,A:{value:'中文甲',events:[]}},host,samples).passed);
 assert(!evaluateIME({...documents,A:{value:'中文甲',events:events.map(e=>({...e,trusted:false}))}},host,samples).passed);
 assert(!evaluateIME(documents,host,[]).passed);
 const monitor=(hostMonitor,hostDpi)=>({hostMonitor,guestMonitor:hostMonitor,hostDpi,guestDpi:hostDpi,dpr:hostDpi/96});
 assert(!evaluateMixedDPI([monitor(1,192),monitor(2,192)]).passed);
 assert(evaluateMixedDPI([monitor(1,192),monitor(2,144)]).passed);
 assert(!evaluateMixedDPI([monitor(1,192),{...monitor(2,144),guestDpi:192}]).passed);
});
test('release: already-invalidated scope requires absence from real state, other errors fail',async()=>{
 let sessions=[],error='AUTOMATION_REVOKED';
 const ctx=makeContext({api:async()=>({ok:false,value:{error}}),call:async()=>({browserAccess:{sessions},generation:'next'})},{consent:'manual'});
 ctx.base={session:'fixture',lifetime:'life'};await ctx.revoke();assert.equal(ctx.base,null);assert.equal(ctx.releaseEvents.length,1);
 sessions=[{session:'fixture',lifetime:'life'}];ctx.base={...sessions[0]};await assert.rejects(()=>ctx.revoke(),/still active/);
 error='NETWORK_ERROR';await assert.rejects(()=>ctx.revoke(),/NETWORK_ERROR/);
});
test('real fixture: HTTP controls/download headers and cancel close audit',async()=>{
 const server=createFixtureServer();await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));const url='http://127.0.0.1:'+server.address().port;
 try{
  const page=await(await fetch(url+'/interactions?tab=A')).text();assert(page.includes('id="guest-marker"'));assert(page.includes('target="_blank"'));assert(page.includes('id="password"'));
  const script=await(await fetch(url+'/batch.js')).text();assert(script.includes('Browser Lab Tab'));
  const normal=await fetch(url+'/download?kind=normal');assert(normal.headers.get('content-disposition').includes('attachment'));assert((await normal.text()).startsWith('BROWSER_LAB isolated download'));
  const slow=await fetch(url+'/download?kind=slow');await slow.body.cancel();
  await new Promise(resolve=>setTimeout(resolve,100));const audit=await(await fetch(url+'/audit')).json();assert(audit.downloads.find(d=>d.kind==='slow').closed);
 }finally{server.closeAllConnections();await new Promise(resolve=>server.close(resolve))}
});
