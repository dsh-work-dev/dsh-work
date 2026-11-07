import {spawn,execFileSync} from 'node:child_process';
import {readFileSync,writeFileSync,openSync,closeSync} from 'node:fs';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {startPosixSmoke} from './posix-smoke.mjs';
import {normalizeNativeState} from './windows.mjs';
import {waitFor,sleep} from '../lib/context.mjs';
const root=fileURLToPath(new URL('../',import.meta.url));
class Surface{
 constructor(owner,name){this.owner=owner;this.name=name;this.location='wails://localhost/'+(name==='float'?'float':'')}
 url(){return this.location}
 async evaluate(fn,arg){const r=await this.owner.call('control','/ui/eval',{surface:this.name,source:fn.toString(),arg});if(r.error)throw Error(r.error);return r.value}
 async waitForFunction(fn,arg,options={}){return waitFor(()=>this.evaluate(fn,arg),'trusted '+this.name+' UI predicate',options.timeout||10000)}
 async goto(url){await this.owner.call('control','/ui/navigate',{surface:this.name,URL:url});await this.waitForFunction(u=>location.href===u&&document.readyState==='complete',url,{timeout:15000});this.location=url}
 locator(selector){const self=this;return {
  locator(next){return self.locator(selector+' '+next)},
  async click(options={}){await waitFor(()=>self.evaluate(s=>{const el=document.querySelector(s);if(!el||el.disabled||!el.getClientRects().length)return false;el.click();return true},selector),'trusted UI button '+selector,options.timeout||7000)},
  async waitFor(options={}){await self.waitForFunction(({selector,state})=>{const el=document.querySelector(selector),visible=!!el&&el.getClientRects().length>0&&!el.hidden;return state==='hidden'?!visible:visible},{selector,state:options.state},{timeout:options.timeout||7000})}
 }}
}
export async function startNative(options){
 const manager=await startPosixSmoke({...options,fullNative:true}),children=[];
 const session={runId:options.runId,runRoot:options.runRoot,dataRoot:join(options.runRoot,'data'),evidenceRoot:options.evidenceRoot,token:options.token,build:manager.build,endpoints:Object.fromEntries(Object.entries({fixture:19478,cef:19479,shell:19480,control:19481,gateway:19482,backend:19483,artifact:19484}).map(([k,p])=>[k,'http://127.0.0.1:'+p]))};
 writeFileSync(join(options.runRoot,'session.json'),JSON.stringify({control:session.endpoints.control,dataRoot:session.dataRoot}));
 const a={session,consentChannel:'trusted-Wails-ExecJS-fixture',capabilities:new Set(['browser','gateway','nativeViews','nativeInput','nativeCapture','sandboxProbe','overlay','faultInjection','artifactRoot','hostStorage','profileRestart','popup','download','permissions',...(options.network?['network']:[])]),
  async api(role,path,body,{authenticate=true,origin}={}){
   const headers={'Content-Type':'application/json'};if(authenticate)headers['X-Browser-Lab-Token']=options.token;if(origin)headers.Origin=origin;
   const r=await fetch(session.endpoints[role]+path,{method:body===undefined?'GET':'POST',headers,body:body===undefined?undefined:JSON.stringify(body),signal:AbortSignal.timeout(45000)});
   const text=await r.text();let value;try{value=JSON.parse(text)}catch{value={text}}return {ok:r.ok,status:r.status,value};
  },
  async call(role,path,body,flags){const r=await this.api(role,path,body,flags);if(!r.ok)throw Error(role+path+': '+JSON.stringify(r.value));return r.value},
  normalize:normalizeNativeState,
  async state(){return this.normalize(await this.call('control','/state'))},
  docs(){return this.call('backend','/documents')},stats(){return this.call('backend','/stats')},
  async command(action,args={}){await this.call('control','/command?'+new URLSearchParams({action,...args}),{});await sleep(150)},
  async nativeInput(target){
   if(process.env.GITHUB_ACTIONS!=='true')throw Error('Native synthetic keyboard routing is restricted to the owned GitHub runner desktop');
   await this.command('focus-'+target);
   const state=await this.call('control','/state');
   if(!state.nativeFocus?.[target==='guest'?'inGuest':'inHost'])throw Error('Native focus is outside requested owned view: '+JSON.stringify(state.nativeFocus));
   if(process.platform==='linux')execFileSync('xdotool',['type','--clearmodifiers','--delay','50','r']);
   else await this.command('native-key');
   return {nativeFocus:state.nativeFocus,method:process.platform==='linux'?'XTEST on owned Xvfb':'NSEvent to owned NSWindow'};
  },
  async capture(name,which){
   if(process.platform==='darwin'){
    // Source layout/paint precedes CEF's OSR callback, then the 30Hz host
    // frame reader and Core Animation commit. Capture the completed action,
    // rather than an earlier in-flight input/caret frame.
    await this.call('backend','/presentation-barrier',{});
    await sleep(150);
   }
   return manager.capture(name,which);
  },visualMarkers:path=>manager.markers(path),
  async sandboxAssessment(){
   const processes=(await this.call('backend','/processes')).processInfo,renderers=processes.filter(p=>p.type==='renderer');
   const rows=[],canary=join(session.dataRoot,'private/outside.txt');
   for(const p of renderers){
    if(process.platform==='linux'){const text=readFileSync('/proc/'+p.id+'/status','utf8');const fields=Object.fromEntries(text.split('\n').filter(l=>l.includes(':')).map(l=>{const i=l.indexOf(':');return [l.slice(0,i),l.slice(i+1).trim()]}));rows.push({pid:p.id,noNewPrivs:fields.NoNewPrivs,seccomp:fields.Seccomp,capEff:fields.CapEff,nspid:fields.NSpid});}
    else rows.push({pid:p.id,...JSON.parse(execFileSync(join(options.bundle,'sandbox-probe'),[String(p.id),canary],{encoding:'utf8'}))});
   }
   let brokerPolicy;
   if(process.platform==='darwin'){const pid=processes.find(p=>p.type==='browser')?.id;if(!pid)throw Error('No browser PID in owned CEF process inventory');brokerPolicy=JSON.parse(execFileSync(join(options.bundle,'sandbox-probe'),[String(pid),canary],{encoding:'utf8'}));}
   const rendererSandboxed=rows.length>=2&&rows.every(r=>process.platform==='linux'?r.noNewPrivs==='1'&&r.seccomp==='2'&&/^0+$/.test(r.capEff):r.result===1)&&(!brokerPolicy||brokerPolicy.result===0);
   const evidence={rendererSandboxed,rows,brokerPolicy,scope:process.platform==='linux'?'actual renderer no_new_privs + seccomp-filter + zero effective capabilities':'Seatbelt SPI query denies this lab private canary for renderer and permits broker; not all sandbox operations'};
   writeFileSync(join(options.evidenceRoot,'sandbox-processes.json'),JSON.stringify(evidence,null,2));return evidence;
  },
  async recover(kind){await this.main.locator('#recover-'+kind).waitFor({state:'visible',timeout:10000});await this.main.locator('#recover-'+kind).click();return waitFor(async()=>{const f=await this.call('backend','/fault-state');return f.connected&&f.native.ready&&!f.tabs.some(t=>t.crashed)?{...f,native:this.normalize(f.native)}:false},'native explicit recovery',30000)},
  async close(){
   let failure;
   try{await this.call('gateway','/shutdown',{})}catch(e){failure=e}
   try{await manager.close()}catch(e){failure=e}
   try{await this.call('backend','/shutdown',{})}catch(e){failure=e}
   for(const child of children){if(child.exitCode===null&&!child.signalCode)try{await waitFor(()=>child.exitCode!==null||child.signalCode,'lab sidecar exit',10000)}catch(e){failure=e;child.kill('SIGTERM')}}
   if(failure)throw failure;
   const result=JSON.parse(readFileSync(join(options.evidenceRoot,'cleanup.json'),'utf8'));
   if(!result.normal||result.remainingOwnedGroup)throw Error('Native groups were not fully reclaimed');return result;
  }
 };
 a.main=new Surface(a,'main');a.float=new Surface(a,'float');
 function launch(name,exe,args){const out=openSync(join(options.evidenceRoot,name+'.stdout.log'),'w'),err=openSync(join(options.evidenceRoot,name+'.stderr.log'),'w');const child=spawn(exe,args,{cwd:options.runRoot,env:manager.env,stdio:['ignore',out,err]});closeSync(out);closeSync(err);children.push(child);return child}
 try{
  launch('backend',process.execPath,[join(root,'platforms/shared/backend.mjs')]);await waitFor(async()=>{try{return (await a.call('backend','/health')).ready}catch{return false}},'CEF backend',15000);
  launch('gateway',join(options.bundle,'gateway'),[]);await waitFor(async()=>{try{return !!(await a.call('gateway','/state')).browserAccess}catch{return false}},'real Go automation Service',20000);
  await a.main.waitForFunction(()=>!!document.querySelector('#requests'));
  await a.command('float');await a.float.waitForFunction(()=>!!document.querySelector('#requests'));await a.command('dock');
 }catch(e){try{await manager.capture('startup-trusted-ui')}catch(capture){writeFileSync(join(options.evidenceRoot,'startup-capture.log'),String(capture))}try{await a.close()}catch(cleanup){e.message+='; cleanup: '+cleanup.message}throw e}

 return a;
}

