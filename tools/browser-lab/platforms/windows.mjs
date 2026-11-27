import {spawn, execFileSync} from 'node:child_process';
import {readFileSync, writeFileSync, existsSync} from 'node:fs';
import {resolve, join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {PNG} from 'pngjs';
import {attach} from '../lib/attach.mjs';
import {validateSession} from './index.mjs';
const root = fileURLToPath(new URL('../', import.meta.url));
export const capabilities = new Set(['browser','gateway','nativeViews','nativeInput','nativeCapture','sandboxProbe','overlay','faultInjection','artifactRoot','hostStorage','profileRestart','popup','download','permissions']);
export function normalizeNativeState(s) {
 return {ready:s.ready,running:s.guestRunning,selected:s.selected,covered:s.covered,floating:s.floating,
  viewGeneration:s.viewGeneration,presentationEpoch:s.presentationEpoch,
  host:{id:String(s.main.hwnd),dpi:s.main.dpi,pid:s.hostPID},brokerPID:s.guestPID,floatingHost:{id:String(s.float.hwnd),dpi:s.float.dpi,visible:s.float.visible},
  views:s.tabs.map(t=>({name:t.name,id:String(t.hwnd),parentId:String(t.parentHWND),visible:t.visible,dpi:t.dpi,fault:t.fault})),
  monitors:s.monitors};
}

export async function startWindows(options) {
 const args = ['-NoProfile','-File', join(root,'native/windows/start.ps1'), '-Bundle',resolve(options.bundle), '-RunRoot',resolve(options.runRoot), '-RunId',options.runId];
 const start = spawn('pwsh',args,{stdio:['ignore','pipe','pipe'],windowsHide:true});
 const logs=[];start.stdout.on('data',d=>logs.push(d));start.stderr.on('data',d=>logs.push(d));
 const code=await new Promise((resolve,reject)=>{start.on('error',reject);start.on('exit',resolve)});
 writeFileSync(join(options.evidenceRoot,'startup.log'),Buffer.concat(logs));
 if(code!==0) {
  if(existsSync(join(options.runRoot,'private-session.json'))){try{const result=execFileSync('pwsh',['-NoProfile','-File',join(root,'native/windows/stop.ps1'),'-RunRoot',options.runRoot],{encoding:'utf8',windowsHide:true});writeFileSync(join(options.evidenceRoot,'startup-cleanup.log'),result)}catch(e){writeFileSync(join(options.evidenceRoot,'startup-cleanup.log'),String(e.stdout||'')+String(e.stderr||'')+e.message)}}
  throw Error('Windows startup blocked/failed; see startup.log (ordinary unpackaged interactive session required).');
 }
 let shell;
 try {
 const session=validateSession(JSON.parse(readFileSync(join(options.runRoot,'private-session.json'),'utf8')));
 const headers={'X-Browser-Lab-Token':session.token,'Content-Type':'application/json'};
 shell=await attach(19480);
 const shellPages=shell.contexts().flatMap(c=>c.pages());
 const main=shellPages.find(p=>new URL(p.url()).pathname==='/');
 const float=shellPages.find(p=>new URL(p.url()).pathname==='/float');
 if(!main||!float)throw Error('Missing trusted host surfaces');
 const adapter={session,main,float,capabilities:new Set([...capabilities,...(options.network?['network']:[])]),
  async api(role,path,body,{authenticate=true,origin}={}) {
   const requestHeaders=authenticate?{...headers}:{'Content-Type':'application/json'};
   if(origin)requestHeaders.Origin=origin;
   const r=await fetch(session.endpoints[role]+path,{method:body===undefined?'GET':'POST',headers:requestHeaders,body:body===undefined?undefined:JSON.stringify(body),signal:AbortSignal.timeout(45000)});
   const text=await r.text();let value;try{value=JSON.parse(text)}catch{value={text}}return {ok:r.ok,status:r.status,value};
  },
  async call(role,path,body,options) {const r=await this.api(role,path,body,options);if(!r.ok)throw Error(`${role}${path}: ${JSON.stringify(r.value)}`);return r.value},
  normalize:normalizeNativeState,
  async state(){return normalizeNativeState(await this.call('control','/state'))},
  docs(){return this.call('backend','/documents')},
  stats(){return this.call('backend','/stats')},
  async command(action,args={}) {await this.call('control','/command?'+new URLSearchParams({action,...args}),{});await new Promise(r=>setTimeout(r,100))},
  async nativeInput(target){if(process.env.GITHUB_ACTIONS!=='true')throw Error('Native keyboard routing requires the owned GitHub desktop');await this.command('native-focus',{target});if(target==='guest')await this.call('backend','/native-entry-focus',{});else await main.evaluate(()=>document.querySelector('#shell-input').focus());await this.command('native-input',{target});const state=await this.call('control','/state');return {method:'SendInput after owned foreground + native focus-tree validation',transition:state.transitions.filter(e=>e.kind==='native-input').at(-1)}},
  async capture(name,which='main') {
   const state=await this.call('control','/state'),path=join(options.evidenceRoot,name+'.png');
   const text=execFileSync(join(session.dataRoot,'capture.exe'),[String(state[which].hwnd),path],{encoding:'utf8',windowsHide:true});
   writeFileSync(join(options.evidenceRoot,name+'.capture.log'),text);return path;
  },
  visualMarkers(path) {
   const png=PNG.sync.read(readFileSync(path));let shell=0,guest=0;
   for(let i=0;i<png.data.length;i+=4){const [r,g,b]=png.data.subarray(i,i+3);if(Math.abs(r-57)<3&&Math.abs(g-226)<3&&Math.abs(b-181)<3)shell++;if(Math.abs(r-237)<3&&Math.abs(g-60)<3&&Math.abs(b-165)<3)guest++;}
   return {width:png.width,height:png.height,shellPixels:shell,guestPixels:guest};
  },
  async sandbox() {
   const path=join(options.evidenceRoot,'sandbox-processes.json');
   execFileSync('pwsh',['-NoProfile','-File',join(root,'native/windows/probe-processes.ps1'),'-CefExe',session.cefExe,'-OutputPath',path],{windowsHide:true});return JSON.parse(readFileSync(path,'utf8').replace(/^\uFEFF/,''));
  },
  async sandboxAssessment() {
   const rows=await this.sandbox(),renderers=rows.filter(r=>r.commandLine.includes('--type=renderer'));
   return {rendererSandboxed:renderers.length>0&&renderers.every(r=>r.token?.restricted&&r.token.integrityRid===0),rendererCount:renderers.length,
    scope:'Windows restricted-token/Untrusted renderer; not all services or AppContainer',artifact:'sandbox-processes.json'};
  },
  async recover(kind) {
   await main.locator('#recover-'+kind).waitFor({state:'visible',timeout:5000});await main.locator('#recover-'+kind).click();
   for(let i=0;i<200;i++){const f=await this.call('backend','/fault-state');if(f.connected&&f.native.ready&&!f.native.starting&&!f.tabs.some(t=>t.crashed))return {...f,native:normalizeNativeState(f.native)};await new Promise(r=>setTimeout(r,100));}
   throw Error('Explicit '+kind+' recovery did not complete');
  },
  async close() {
   try{await shell.close()}finally{
   const result=execFileSync('pwsh',['-NoProfile','-File',join(root,'native/windows/stop.ps1'),'-RunRoot',session.runRoot],{encoding:'utf8',windowsHide:true});
   writeFileSync(join(options.evidenceRoot,'cleanup.log'),result);
   const cleanup=JSON.parse(readFileSync(join(options.evidenceRoot,'cleanup.json'),'utf8').replace(/^\uFEFF/,''));
   if(cleanup.remainingProcesses.length||cleanup.remainingListeners.length)throw Error('Native cleanup left lab resources; see cleanup.json');return cleanup;}
  }
 };
 return adapter;
 }catch(error){
  if(shell)try{await shell.close()}catch{}
  if(!existsSync(join(options.evidenceRoot,'cleanup.json')))try{execFileSync('pwsh',['-NoProfile','-File',join(root,'native/windows/stop.ps1'),'-RunRoot',options.runRoot],{windowsHide:true})}catch(cleanup){writeFileSync(join(options.evidenceRoot,'startup-cleanup.log'),String(cleanup.stdout||'')+String(cleanup.stderr||'')+cleanup.message)}
  throw error;
 }
}
export const startNative=startWindows;
