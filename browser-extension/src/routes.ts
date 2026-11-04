// Copyright (c) DSH Work contributors. Apache-2.0; see LICENSE and NOTICE.
import {RelayConnection} from '../playwright/relayConnection';
import {ConnectedTabGroup, uniqueGroupStyle, ungroupTabs} from '../playwright/connectedTabGroup';

const MAX_FROM_EXTENSION = 8 * 1024 * 1024;
const routePattern = /^s-[a-f0-9]{32}$/;
let storageTail: Promise<void> = Promise.resolve();

// There is one manager per authenticated NativePort, not another native port
// per session. Go supplies opaque routes; the popup never invokes this API.
export function createRoutes(post: (message: any)=>void) {
 const channels = new Map<string, any>(), groups = new Map<string, ConnectedTabGroup>();
 const openings = new Set<string>();
 const ownership = new Map<string,{group:number,tabs:number[]}>();
 let connected = true;
 const saveOwnership = () => { const rows=[...ownership.values()]; const write=storageTail.then(()=>chrome.storage.session.set({ownedGroups:rows}));storageTail=write.catch(console.error);return write; };
 // Query only recorded tab IDs, never all profiles/tabs or groups by title.
 const recovery = storageTail.then(async()=>{
  const {ownedGroups=[]}=await chrome.storage.session.get('ownedGroups');
  for(const row of ownedGroups) {
   if(!Number.isInteger(row.group)||!Array.isArray(row.tabs))continue;
   const ids=[];
   for(const id of row.tabs)try{const tab=await chrome.tabs.get(id);try{await chrome.debugger.detach({tabId:id})}catch{};if(row.group>=0&&tab.groupId===row.group)ids.push(id)}catch{}
   if(ids.length)await ungroupTabs(ids);
  }
  await chrome.storage.session.set({ownedGroups:[]});
 });
 storageTail=recovery.catch(console.error);
 function notify(message:any) { if(connected)try{post(message)}catch(e){disconnect();console.error(e)} }
 function send(message:any) {
  if(!connected)throw Error('BROWSER_NATIVE_DISCONNECTED');
  if(new TextEncoder().encode(JSON.stringify(message)).length>MAX_FROM_EXTENSION)throw Error('BROWSER_NATIVE_MESSAGE_TOO_LARGE');
  post(message);
 }
 function disconnect() {
  if(!connected)return;connected=false;
  for(const socket of [...channels.values()])try{socket.releaseLocal()}catch(e){console.error(e)}
 }
 async function receive(m:any) {
  if(!['open','wire','close'].includes(m.kind))return false;
  if(!routePattern.test(m.route))throw Error('BROWSER_ROUTE_INVALID');
  try {
   if(m.kind==='open') {
    if(m.wireProtocol!==2||channels.has(m.route)||openings.has(m.route))throw Error('BROWSER_UPDATE_REQUIRED');
    // Chrome has nine group colors; never silently give two sessions one color.
    if(channels.size+openings.size>=9)throw Error('BROWSER_ROUTE_LIMIT');
    openings.add(m.route);
    const token={};
    const pending=token;
    openingTokens.set(m.route,pending);
    await recovery;
    if(!connected||openingTokens.get(m.route)!==pending)return true;
    const tab=await chrome.tabs.create({url:'about:blank',active:true});
    if(!connected||openingTokens.get(m.route)!==pending)return true;
    const socket:any={readyState:1,onmessage:null,onclose:null,
     send:(data:string)=>{try{send({kind:'wire',route:m.route,payload:JSON.parse(data)})}catch(e:any){try{socket.releaseLocal()}finally{notify({kind:'route-error',route:m.route,error:e.message})}}},
     releaseLocal:()=>{
      if(socket.readyState!==1)return;socket.readyState=3;
      try{socket.onclose?.()}finally{channels.delete(m.route);groups.delete(m.route);ownership.delete(m.route);void saveOwnership().catch(console.error)}
     },
     close:()=>{try{socket.releaseLocal()}finally{notify({kind:'closed',route:m.route})}}
    };
    channels.set(m.route,socket);
    const relay=new RelayConnection(socket,async tabId=>{
     const old=ownership.get(m.route)??{group:-1,tabs:[]};
     ownership.set(m.route,{group:old.group,tabs:[...new Set([...old.tabs,tabId])]});
     await saveOwnership();
     if(socket.readyState!==1)throw Error('BROWSER_ROUTE_CLOSED');
    });
    const group=new ConnectedTabGroup(relay,tab,String(m.title||'Untitled'),
     uniqueGroupStyle(String(m.title||'Untitled'),[...groups.values()].map(g=>g.groupStyle)),
     tabId=>[...groups].some(([route,g])=>route!==m.route&&g.connectedTabIds().includes(tabId)));
    group.ongroup=(id,tabs)=>{ownership.set(m.route,{group:id,tabs});void saveOwnership().catch(console.error)};
    group.onclose=()=>{channels.delete(m.route);groups.delete(m.route);ownership.delete(m.route);void saveOwnership().catch(console.error)};
    groups.set(m.route,group);
   } else if(m.kind==='wire') {
    const socket=channels.get(m.route);if(!socket)throw Error('BROWSER_ROUTE_CLOSED');
    socket.onmessage?.({data:JSON.stringify(m.payload)});
   } else {
    openingTokens.delete(m.route);channels.get(m.route)?.close();
   }
  } catch(e:any){try{channels.get(m.route)?.releaseLocal()}finally{notify({kind:'route-error',route:m.route,error:e.message})}}
  finally{if(m.kind==='open'){openings.delete(m.route);openingTokens.delete(m.route)}}
  return true;
 }
 const openingTokens=new Map<string,object>();
 return {receive,disconnect};
}
