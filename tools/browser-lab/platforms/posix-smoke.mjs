import {spawn,execFileSync} from 'node:child_process';
import {readFileSync,writeFileSync,openSync,closeSync,existsSync} from 'node:fs';
import {join} from 'node:path';
import {PNG} from 'pngjs';
import {attach} from '../lib/attach.mjs';
import {createFixtureServer} from '../fixtures/server.mjs';
import {waitFor,sleep} from '../lib/context.mjs';

export async function startPosixSmoke(options){
 if(!['darwin','linux'].includes(process.platform))throw Error('POSIX native smoke requires macOS or Linux/X11');
 const build=JSON.parse(readFileSync(join(options.bundle,'build.json'),'utf8'));
 if(build.platform!==process.platform||build.arch!==process.arch)throw Error('Bundle platform/architecture mismatch');
 const fixture=createFixtureServer();await new Promise((resolve,reject)=>{fixture.once('error',reject);fixture.listen(19478,'127.0.0.1',resolve)});
 const token=options.token,headers={'X-Browser-Lab-Token':token};
 const out=openSync(join(options.evidenceRoot,'host.stdout.log'),'w'),err=openSync(join(options.evidenceRoot,'host.stderr.log'),'w');
 const child=spawn(build.executable,[],{cwd:options.runRoot,detached:true,stdio:['ignore',out,err],env:{...process.env,BROWSER_LAB_RUN_ROOT:options.runRoot,BROWSER_LAB_TOKEN:token,BROWSER_LAB_BRIDGE:build.bridge,BROWSER_LAB_HELPER:build.helper,LD_LIBRARY_PATH:process.platform==='linux'?options.bundle:process.env.LD_LIBRARY_PATH}});closeSync(out);closeSync(err);
 let browser,closed=false,spawnError;child.on('error',e=>{spawnError=e});
 const groupAlive=()=>{try{process.kill(-child.pid,0);return true}catch(e){if(e.code==='ESRCH')return false;throw e}};
 async function call(path,method='GET'){
  if(spawnError)throw spawnError;if(child.exitCode!==null||child.signalCode)throw Error('Native host exited before '+path);
  const r=await fetch('http://127.0.0.1:19481'+path,{method,headers,signal:AbortSignal.timeout(15000)});if(!r.ok)throw Error('Native '+path+': '+await r.text());return r.json();
 }
 async function close(){
  if(closed)return;closed=true;let failure;
  try{
   if(browser)await browser.close();
   if(child.exitCode===null&&!child.signalCode){await call('/close','POST');await waitFor(()=>child.exitCode!==null||child.signalCode,'host normal exit',20000)}
   const closure=JSON.parse(readFileSync(join(options.evidenceRoot,'host-closed.json'),'utf8'));
   if(!closure.createdClosed||!closure.cefShutdownReturned||child.exitCode!==0)throw Error('Native close did not complete its CEF lifecycle');
   await waitFor(()=>!groupAlive(),'owned process group exit',5000);
  }catch(e){failure=e;if(child.pid&&groupAlive()){process.kill(-child.pid,'SIGTERM');await sleep(1000);if(groupAlive())process.kill(-child.pid,'SIGKILL')}}
  finally{fixture.closeAllConnections();await new Promise(resolve=>fixture.close(resolve));writeFileSync(join(options.evidenceRoot,'cleanup.json'),JSON.stringify({normal:!failure,remainingOwnedGroup:child.pid?groupAlive():false,hostExitCode:child.exitCode,error:failure?.message},null,2))}
  if(failure)throw failure;
 }
 try{
  await waitFor(async()=>{try{const s=await call('/state');return s.parentAttached&&s.created===1}catch(e){if(child.exitCode!==null||spawnError)throw e;return false}},'native attached CEF browser',45000);
  browser=await attach(19479);const page=browser.contexts().flatMap(c=>c.pages()).find(p=>p.url().includes('/interactions'));
  if(!page)throw Error('Missing live embedded CEF page');await page.waitForFunction(()=>!!window.lab?.instance);
  return {build,page,call,close,
   async capture(name){const s=await call('/state');if(!/^\d+$/.test(s.windowId)||s.windowId==='0')throw Error('Invalid owned window capture ID');const path=join(options.evidenceRoot,name+'.png');if(process.platform==='linux')execFileSync('import',['-window',s.windowId,path]);else execFileSync('/usr/sbin/screencapture',['-x','-o','-l',s.windowId,path]);return path},
   markers(path){const p=PNG.sync.read(readFileSync(path));let shell=0,guest=0;for(let i=0;i<p.data.length;i+=4){const[r,g,b]=p.data.subarray(i,i+3);if(Math.abs(r-57)<3&&Math.abs(g-226)<3&&Math.abs(b-181)<3)shell++;if(Math.abs(r-237)<3&&Math.abs(g-60)<3&&Math.abs(b-165)<3)guest++}return {shellPixels:shell,guestPixels:guest,width:p.width,height:p.height}}
  };
 }catch(e){try{await close()}catch(cleanup){e.message+='; cleanup: '+cleanup.message}throw e}
}
