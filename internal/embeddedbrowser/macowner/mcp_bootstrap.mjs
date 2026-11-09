// Trusted --import asset for only the fixed owned-CEF MCP adapter and its
// locked SDK stdio child. Runs in each actual Node process before its entry.
import fs from 'node:fs';
import {createRequire} from 'node:module';
import {Socket} from 'node:net';
const config=JSON.parse(fs.readFileSync(process.env.DSH_WORK_MCP_OWNER_BOOTSTRAP,'utf8'));
const entry=fs.realpathSync(process.argv[1]);
if(entry!==config.adapter&&entry!==config.cli)throw Error('MAC_MCP_ENTRY_REJECTED');
if(entry===config.cli){
 const a=process.argv.slice(2);
 if(a.length!==10||a[0]!=='--cdp-endpoint'||!/^ws:\/\/127\.0\.0\.1:[0-9]+\/cdp\/[a-f0-9]{48}$/.test(a[1])||a[2]!=='--output-dir'||!a[3].startsWith('/')||a[4]!=='--timeout-action'||a[5]!=='10000'||a[6]!=='--timeout-navigation'||a[7]!=='15000'||a[8]!=='--snapshot-mode'||a[9]!=='none')throw Error('MAC_MCP_CONFIG_REJECTED');
}
for(const [name,version]of[['@playwright/mcp','0.0.83'],['@modelcontextprotocol/sdk','1.32.2'],['ws','8.22.1'],['playwright-core','1.64.0-alpha-1790635538000'],['playwright','1.64.0-alpha-1790635538000']]){
 if(JSON.parse(fs.readFileSync(`${config.dependencies}/node_modules/${name}/package.json`,'utf8')).version!==version)throw Error('MAC_MCP_PUBLICATION_REJECTED');
}
const bridge=createRequire(import.meta.url)(config.bridge);
const owner=await bridge.connect(config.socket);
let socket;
try{
 const peer=bridge.peer(owner);
 if(peer.pid!==config.ownerPID||String(peer.started)!==config.ownerStarted)throw Error('MAC_MCP_OWNER_REJECTED');
 socket=new Socket({fd:bridge.fd(owner),readable:true,writable:true});bridge.release(owner);
}catch(error){bridge.close(owner);throw error}
await new Promise((resolve,reject)=>{
 let bytes=Buffer.alloc(0),done=false;
 const finish=error=>{if(done)return;done=true;clearTimeout(timer);socket.destroy();error?reject(error):resolve()};
 const timer=setTimeout(()=>finish(Error('MAC_MCP_REGISTRATION_TIMEOUT')),3000);
 socket.on('error',()=>finish(Error('MAC_MCP_REGISTRATION_FAILED')));
 socket.on('close',()=>{if(!done)finish(Error('MAC_MCP_REGISTRATION_CLOSED'))});
 socket.on('data',chunk=>{
  bytes=Buffer.concat([bytes,chunk]);if(bytes.length>1048580)return finish(Error('MAC_MCP_REGISTRATION_INVALID'));
  if(bytes.length<4)return;const n=bytes.readUInt32LE();if(!n||n>1048576)return finish(Error('MAC_MCP_REGISTRATION_INVALID'));
  if(bytes.length<n+4)return;
  try{const reply=JSON.parse(bytes.subarray(4,n+4));if(reply.kind!=='registered')throw Error();finish()}catch{finish(Error('MAC_MCP_REGISTRATION_INVALID'))}
 });
 const body=Buffer.from(JSON.stringify({kind:'mcp',generation:config.generation}));const header=Buffer.alloc(4);header.writeUInt32LE(body.length);socket.write(Buffer.concat([header,body]));
});
