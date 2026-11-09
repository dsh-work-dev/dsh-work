// This trusted import runs before the fixed CLI, profile, or plugin modules.
import fs from 'node:fs';
import cp from 'node:child_process';
import {createRequire,syncBuiltinESMExports} from 'node:module';
import {Socket} from 'node:net';
const config=JSON.parse(fs.readFileSync(process.env.DSH_WORK_RESTRICTED_BOOTSTRAP,'utf8'));
if(fs.realpathSync(process.argv[1])!==config.entry)throw Error('MAC_PREVIEW_ENTRY_REJECTED');
const require=createRequire(import.meta.url);
if(require(config.sandbox).apply()!==true)throw Error('MAC_PREVIEW_SANDBOX_REQUIRED');
const unavailable=()=>{const e=Error('Local commands, terminals and custom stdio MCP are unavailable in this Mac preview.');e.code='MAC_PREVIEW_PROCESS_UNAVAILABLE';throw e};
for(const key of ['spawn','spawnSync','exec','execSync','execFile','execFileSync','fork'])cp[key]=unavailable;
if(typeof process.execve==='function')process.execve=unavailable;
syncBuiltinESMExports();
const bridge=require(config.bridge);
const owner=await bridge.connect(config.socket);
let socket;
try{
 const peer=bridge.peer(owner);
 if(peer.pid!==config.ownerPID||String(peer.started)!==config.ownerStarted)throw Error('MAC_PREVIEW_OWNER_REJECTED');
 socket=new Socket({fd:bridge.fd(owner),readable:true,writable:true});bridge.release(owner);
}catch(error){bridge.close(owner);throw error}
await new Promise((resolve,reject)=>{
 let bytes=Buffer.alloc(0),done=false;
 const finish=error=>{if(done)return;done=true;clearTimeout(timer);socket.destroy();error?reject(error):resolve()};
 const timer=setTimeout(()=>finish(Error('MAC_PREVIEW_REGISTRATION_TIMEOUT')),3000);
 socket.on('error',()=>finish(Error('MAC_PREVIEW_REGISTRATION_FAILED')));
 socket.on('close',()=>{if(!done)finish(Error('MAC_PREVIEW_REGISTRATION_CLOSED'))});
 socket.on('data',chunk=>{bytes=Buffer.concat([bytes,chunk]);if(bytes.length>1048580)return finish(Error('MAC_PREVIEW_REGISTRATION_INVALID'));if(bytes.length<4)return;const n=bytes.readUInt32LE();if(!n||n>1048576)return finish(Error('MAC_PREVIEW_REGISTRATION_INVALID'));if(bytes.length<n+4)return;try{const reply=JSON.parse(bytes.subarray(4,n+4));if(reply.kind!=='registered')throw Error();finish()}catch{finish(Error('MAC_PREVIEW_REGISTRATION_INVALID'))}});
 const body=Buffer.from(JSON.stringify({kind:'restricted-dsh',generation:config.generation,version:'restricted-dsh/node-only-v1'}));const header=Buffer.alloc(4);header.writeUInt32LE(body.length);socket.write(Buffer.concat([header,body]));
});
