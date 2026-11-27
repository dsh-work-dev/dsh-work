import {spawn,execFileSync} from 'node:child_process';
import {readFileSync,writeFileSync,openSync,closeSync,mkdirSync,cpSync,symlinkSync} from 'node:fs';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {PNG} from 'pngjs';
import {attach} from '../lib/attach.mjs';
import {createFixtureServer} from '../fixtures/server.mjs';
import {waitFor,sleep} from '../lib/context.mjs';
const suite=fileURLToPath(new URL('../',import.meta.url));
export async function startPosixSmoke(options){
 if(!['darwin','linux'].includes(process.platform))throw Error('POSIX native verification requires macOS or Linux/X11');
 const build=JSON.parse(readFileSync(join(options.bundle,'build.json'),'utf8'));
 if(build.platform!==process.platform||build.arch!==process.arch)throw Error('Bundle platform/architecture mismatch');
 mkdirSync(join(options.runRoot,'data/private'),{recursive:true});
 cpSync(join(suite,'fixtures/artifact'),join(options.runRoot,'data/artifact'),{recursive:true});
 writeFileSync(join(options.runRoot,'data/private/outside.txt'),'BROWSER_LAB_PRIVATE_SENTINEL');
 symlinkSync(join(options.runRoot,'data/private'),join(options.runRoot,'data/artifact/escape'),'dir');
 const fixture=createFixtureServer();await new Promise((resolve,reject)=>{fixture.once('error',reject);fixture.listen(19478,'127.0.0.1',resolve)});
 const headers={'X-Browser-Lab-Token':options.token};
 let child,browser,closed=false,spawnError,lastState;
 const children=[],env={...process.env,BROWSER_LAB_RUN_ROOT:options.runRoot,BROWSER_LAB_ROOT:options.runRoot,BROWSER_LAB_DATA_ROOT:join(options.runRoot,'data'),BROWSER_LAB_TOKEN:options.token,BROWSER_LAB_BRIDGE:build.bridge,BROWSER_LAB_HELPER:build.helper,LD_LIBRARY_PATH:process.platform==='linux'?options.bundle:process.env.LD_LIBRARY_PATH};
 function spawnHost(){
  const number=children.length,out=openSync(join(options.evidenceRoot,'host-'+number+'.stdout.log'),'w'),err=openSync(join(options.evidenceRoot,'host-'+number+'.stderr.log'),'w');
  child=spawn(build.executable,[],{cwd:options.runRoot,detached:true,stdio:['ignore',out,err],env});closeSync(out);closeSync(err);children.push(child);
  child.on('error',e=>{spawnError=e});const current=child;
  child.on('exit',(code,signal)=>writeFileSync(join(options.evidenceRoot,'host-'+number+'-exit.json'),JSON.stringify({pid:current.pid,code,signal},null,2)));return child;
 }
 const groupAlive=p=>{try{process.kill(-p.pid,0);return true}catch(e){if(e.code==='ESRCH')return false;if(e.code==='EPERM')return true;throw e}};
 async function api(path,body,extra={}){
  if(spawnError)throw spawnError;if(child.exitCode!==null||child.signalCode)throw Error('Native host exited before '+path);
  const r=await fetch('http://127.0.0.1:19481'+path,{method:body===undefined?'GET':'POST',headers:{...headers,'Content-Type':'application/json',...extra},body:body===undefined?undefined:JSON.stringify(body),signal:AbortSignal.timeout(20000)});
  const text=await r.text();let value;try{value=JSON.parse(text)}catch{value={text}}return {ok:r.ok,status:r.status,value};
 }
 async function call(path,body){const r=await api(path,body);if(!r.ok)throw Error(path+': '+JSON.stringify(r.value));return r.value}
 function captureState(s,name,which='main'){
  const id=s[which]?.windowId||s.windowId;if(!/^\d+$/.test(id)||id==='0')throw Error('Invalid owned native window ID');
  const path=join(options.evidenceRoot,name+'.png');if(process.platform==='linux')execFileSync('import',['-window',id,path]);else execFileSync('/usr/sbin/screencapture',['-x','-o','-l',id,path]);return path;
 }
 async function ready(){
  await waitFor(async()=>{try{lastState=await call('/state');writeFileSync(join(options.evidenceRoot,'startup-state.json'),JSON.stringify(lastState,null,2));return lastState.ready&&lastState.tabs.length===2}catch(e){if(child.exitCode!==null||child.signalCode||spawnError)throw e;return false}},'both native CEF tabs',45000);
 }
 async function close(){
  if(closed)return;closed=true;let failure;
  try{
   if(browser)await browser.close();
   if(child.exitCode===null&&!child.signalCode){await call('/close',{});await waitFor(()=>child.exitCode!==null||child.signalCode,'host normal exit',25000)}
   if(!(options.architectureFault&&child.pid===options.architectureFaultPid)){
    const closure=JSON.parse(readFileSync(join(options.evidenceRoot,'host-closed.json'),'utf8'));
    if(!closure.createdClosed||!closure.cefShutdownReturned||child.exitCode!==0)throw Error('CEF/host close lifecycle incomplete');
   }
   await waitFor(()=>children.every(p=>!groupAlive(p)),'all owned process groups exited',10000);
  }catch(e){failure=e;
   for(const p of children)if(groupAlive(p))try{process.kill(-p.pid,'SIGTERM');await sleep(1000);if(groupAlive(p))process.kill(-p.pid,'SIGKILL')}catch(signal){writeFileSync(join(options.evidenceRoot,'cleanup-signal.log'),String(signal))}
  }finally{
   fixture.closeAllConnections();await new Promise(resolve=>fixture.close(resolve));
   writeFileSync(join(options.evidenceRoot,'cleanup.json'),JSON.stringify({normal:!failure,remainingOwnedGroup:children.some(groupAlive),hostExitCode:child.exitCode,error:failure?.message},null,2));
  }if(failure)throw failure;
 }
 spawnHost();
 try{
  await ready();let page;
  if(!options.fullNative){browser=await attach(19479);page=browser.contexts().flatMap(c=>c.pages()).find(p=>p.url().includes('tab=A'));if(!page)throw Error('Missing embedded A');await page.waitForFunction(()=>!!window.lab?.instance)}
  return {build,page,env,api,call,close,get child(){return child},
   async restartHost(){await waitFor(()=>child.exitCode!==null||child.signalCode,'old host exit');spawnError=null;spawnHost();await ready()},
   async capture(name,which='main'){return captureState(await call('/state'),name,which)},
   markers(path){const p=PNG.sync.read(readFileSync(path));let shell=0,guest=0;for(let i=0;i<p.data.length;i+=4){const[r,g,b]=p.data.subarray(i,i+3);if(Math.abs(r-57)<3&&Math.abs(g-226)<3&&Math.abs(b-181)<3)shell++;if(Math.abs(r-237)<3&&Math.abs(g-60)<3&&Math.abs(b-165)<3)guest++}return {shellPixels:shell,guestPixels:guest,width:p.width,height:p.height}}
  };
 }catch(e){
  if(lastState?.windowId&&lastState.windowId!=='0')try{captureState(lastState,'startup-native')}catch{}
  if(process.platform==='darwin'&&child.pid&&child.exitCode===null)try{execFileSync('/usr/bin/sample',[String(child.pid),'3','-file',join(options.evidenceRoot,'startup-native-sample.log')],{timeout:15000})}catch{}
  try{await close()}catch(cleanup){e.message+='; cleanup: '+cleanup.message}throw e;
 }
}
