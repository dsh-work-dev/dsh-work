import {randomUUID} from 'node:crypto';
import {mkdirSync,existsSync,readFileSync,readdirSync,lstatSync,statSync} from 'node:fs';
import {join,basename} from 'node:path';
export function nativePolicy({tabs,hostState,identity,display,native,record,dataRoot}){
 const requests=new Map(),reserved=new Set(),directory=join(dataRoot,'approved-downloads');
 mkdirSync(directory,{recursive:true});
 if(lstatSync(directory).isSymbolicLink())throw Error('Download directory must not be a symlink');
 async function binding(tab){const h=await hostState();if(!h.interactable||h.selected!==tab||!h.guestRunning)throw Error('NATIVE_POLICY_NOT_INTERACTABLE');return {document:await identity(tabs.get(tab)),generation:h.viewGeneration,selection:h.selectionEpoch,presentation:h.presentationEpoch}}
 async function valid(p){try{return JSON.stringify(p.binding)===JSON.stringify(await binding(p.tab))}catch{return false}}
 const publicRequest=p=>({id:p.id,kind:p.kind,tab:p.tab,target:p.target,choice:p.choice,status:p.status,filename:p.filename,result:p.result,revoked:p.revoked});
 async function endpoint(path,body){
  if(path==='request'){
   if(!['popup','download','notifications'].includes(body.kind)||!['A','B'].includes(body.tab))throw Error('Invalid native request');
   const b=await binding(body.tab);if(b.document.url!==body.source)throw Error('Native source document changed');
   const url=new URL(body.target);if(!['http:','https:'].includes(url.protocol)||url.username||url.password)throw Error('Invalid native target');
   if(body.kind==='notifications'&&url.origin!==new URL(body.source).origin)throw Error('Permission origin mismatch');
   const id=randomUUID(),p={...body,id,binding:b,choice:null,status:'pending',revoked:false,filename:'browser-lab-'+id+'.txt'};
   requests.set(id,p);record('native-policy-request',publicRequest(p));
   const until=Date.now()+30000;while(!p.choice&&Date.now()<until){if(!await valid(p)){p.choice='deny';p.status='expired';break}await new Promise(r=>setTimeout(r,100))}
   if(!p.choice){p.choice='deny';p.status='expired'}
   if(p.choice==='allow'&&!await valid(p)){p.choice='deny';p.status='expired'}
   if(p.choice==='allow'&&p.kind==='popup'){
    await display({tab:'B',url:p.target});await native('tab',{tab:'B'});p.status='opened';
   }
   record('native-policy-decision',publicRequest(p));
   return {id,choice:p.choice,path:p.kind==='download'&&p.choice==='allow'?join(directory,p.filename):''};
  }
  if(path==='state'){
   for(const p of requests.values())if(p.status==='pending'&&!await valid(p)){p.choice='deny';p.status='expired'}
   return {requests:[...requests.values()].map(publicRequest),files:readdirSync(directory).flatMap(name=>{
    try{return [{name,size:statSync(join(directory,name)).size,partial:name.endsWith('.partial')}]}
    catch(e){if(e.code==='ENOENT')return [];throw e} // broker can rename/cancel between readdir and stat
   })};
  }
  const p=requests.get(body.id);if(!p)throw Error('Unknown native policy request');
  if(path==='answer'){
   if(p.choice||p.status!=='pending')throw Error('Native decision already consumed');
   if(!['allow','deny'].includes(body.choice)||!await valid(p)){p.choice='deny';p.status='expired';throw Error('Native decision expired')}
   if(body.filename!==undefined){
    if(p.kind!=='download'||!/^[-a-zA-Z0-9_][-.a-zA-Z0-9_]{0,100}\.txt$/.test(body.filename)||body.filename.includes('..')||/^(CON|PRN|AUX|NUL|COM[1-9]|LPT[1-9])\./i.test(body.filename))throw Error('Invalid download filename');
    p.filename=body.filename;
   }
   if(p.kind==='download'&&body.choice==='allow'){
    if(reserved.has(p.filename)||existsSync(join(directory,p.filename)))throw Error('Destination already exists');
    reserved.add(p.filename);
   }
   p.choice=body.choice;p.status=p.choice==='allow'?'authorized':'denied';return publicRequest(p);
  }
  if(path==='revoke'){
   p.revoked=true;p.status='revoked';if(!p.choice)p.choice='deny';record('native-policy-revoked',publicRequest(p));return publicRequest(p);
  }
  if(path==='watch'){
   for(let i=0;i<10&&!p.revoked;i++){if(p.kind==='download'&&!await valid(p)){p.revoked=true;p.status='expired';break}await new Promise(r=>setTimeout(r,100))}
   return {revoked:p.revoked,finished:!!p.result};
  }
  if(path==='result'){
   p.result={state:body.state,bytes:body.bytes,autoOpened:false};
   if(body.state==='complete'){
    const path=join(directory,p.filename),text=readFileSync(path,'utf8');
    p.result.filename=basename(path);p.result.fixtureContent=text;
   }
   p.status=body.state;record('native-policy-result',publicRequest(p));return publicRequest(p);
  }
  throw Error('Unknown native policy operation');
 }
 return endpoint;
}
