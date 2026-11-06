import {spawn,execFileSync} from 'node:child_process';
import {mkdtempSync,mkdirSync,existsSync,readFileSync,writeFileSync,cpSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join,resolve} from 'node:path';
import {fileURLToPath} from 'node:url';
import {randomUUID} from 'node:crypto';
import {cases,validateCatalog} from './catalog.mjs';
import {assessResults} from './lib/report.mjs';
const root=fileURLToPath(new URL('./',import.meta.url));
const args=process.argv.slice(2),command=args.shift();
function option(name,fallback){const index=args.indexOf('--'+name);return index<0?fallback:args[index+1]}
if(command==='list'){
 validateCatalog();for(const c of cases)console.log(`${c.id}\t${c.manual?'manual':c.unimplemented?'unimplemented':c.knownGap?'known-gap':'automatic'}\t${c.title}`);
}else if(command==='run'||command==='manual'||command==='smoke'){
 const profile=option('profile','regression'),consent=command==='manual'?'manual':command==='smoke'?'not-applicable':option('consent');
 if(!['regression','qualification'].includes(profile)||(command!=='smoke'&&!['manual','scripted'].includes(consent)))throw Error('Use --profile regression|qualification and --consent manual|scripted');
 const runId=option('run-id',new Date().toISOString().replaceAll(/[:.]/g,'-')+'-'+randomUUID().slice(0,8));
 if(!/^[a-zA-Z0-9._-]+$/.test(runId))throw Error('Invalid run ID');
 const output=resolve(option('output',join(root,'results',runId)));
 if(existsSync(join(output,'report.json')))throw Error('Output already contains a report; choose a new directory');
 mkdirSync(output,{recursive:true});
 const runRoot=mkdtempSync(join(tmpdir(),'browser-lab-')),evidenceRoot=join(runRoot,'evidence');mkdirSync(evidenceRoot);
 const selectedCases=option('case')?.split(',');
 if(selectedCases?.some(id=>!cases.some(c=>c.id===id)))throw Error('Unknown --case ID');
 const options={runId,profile,consent,token:randomUUID()+randomUUID(),network:args.includes('--network'),selectedCases,bundle:resolve(option('bundle',join(root,command==='smoke'?'.work/posix/bundle':'.work/bundle'))),runRoot,evidenceRoot,output,manualEvidence:option('manual-evidence'),operator:option('operator')};
 if(command==='manual'){
  try{const {runManual}=await import('./manual.mjs');const result=await runManual(options);console.log('Manual evidence: '+join(output,'manual-evidence.json'));process.exitCode=result.results.some(r=>r.status==='fail')?1:result.results.some(r=>r.status==='blocked')?2:0}finally{cpSync(evidenceRoot,join(output,'evidence'),{recursive:true})}
 }else{
 let code,cleanupFailed=false;
 try{
 const child=spawn(process.execPath,['--test','--test-concurrency=1','--test-reporter=spec','--test-reporter=junit','--test-reporter-destination=stdout','--test-reporter-destination='+join(output,'junit.xml'),join(root,command==='smoke'?'tests/posix-smoke.test.mjs':'tests/native.test.mjs')],{stdio:'inherit',windowsHide:true,env:{...process.env,BROWSER_LAB_OPTIONS:JSON.stringify(options)}});
 code=await new Promise((resolve,reject)=>{child.on('error',reject);child.on('exit',(value)=>resolve(value??1))});
 }finally{
 if(process.platform==='win32'&&existsSync(join(runRoot,'private-session.json'))&&!existsSync(join(evidenceRoot,'cleanup.json'))){
  try{const log=execFileSync('pwsh',['-NoProfile','-File',join(root,'native/windows/stop.ps1'),'-RunRoot',runRoot],{encoding:'utf8',windowsHide:true});writeFileSync(join(evidenceRoot,'runner-fallback-cleanup.log'),log)}catch(e){cleanupFailed=true;writeFileSync(join(evidenceRoot,'runner-fallback-cleanup.log'),String(e.stdout||'')+String(e.stderr||'')+e.message)}
 }
 }
 cpSync(evidenceRoot,join(output,'evidence'),{recursive:true});
 const reportPath=join(output,'report.json');
 if(!existsSync(reportPath)){console.error('Runner stopped without a complete report. Native session: '+runRoot);process.exitCode=1}
 else{
  const report=JSON.parse(readFileSync(reportPath,'utf8'));
  const judgment=assessResults(report.results,profile);
  console.log(`Browser Lab: ${report.results.length} results; qualification=${judgment.qualification}; artifacts=${output}`);
  console.log('Manual checklist: '+join(output,'manual-checklist.json'));
  process.exitCode=code||cleanupFailed?1:judgment.exitCode;
 }
 }
}else{
 console.log('node cli.mjs list\nnode cli.mjs run --profile regression|qualification --consent manual|scripted [--bundle PATH] [--network] [--case ID,ID] [--output PATH]\nnode cli.mjs manual --operator NAME --run-id ID [--bundle PATH]');
 if(command)process.exitCode=1;
}
