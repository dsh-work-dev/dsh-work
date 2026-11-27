import https from 'node:https';
import {readFileSync} from 'node:fs';
import {X509Certificate} from 'node:crypto';

// Public test-only key/certificate: never installed into any trust store. The
// browser must use its normal TLS policy and reject this self-signed identity.
export async function createInvalidTLSFixture() {
 const cert=readFileSync(new URL('./tls-untrusted-cert.pem',import.meta.url));
 const key=readFileSync(new URL('./tls-untrusted-key.pem',import.meta.url));
 const identity=new X509Certificate(cert);
 const audit={connections:0,requests:0,tlsErrors:[]},sockets=new Set();
 const server=https.createServer({key,cert},(_req,res)=>{
  audit.requests++;res.writeHead(200,{'Content-Type':'text/plain'});res.end('Browser Lab untrusted TLS fixture');
 });
 server.on('connection',socket=>{audit.connections++;sockets.add(socket);socket.once('close',()=>sockets.delete(socket))});
 server.on('tlsClientError',error=>{if(audit.tlsErrors.length<16)audit.tlsErrors.push(error.code||'TLS_ERROR')});
 await new Promise((resolve,reject)=>{server.once('error',reject);server.listen(0,'127.0.0.1',resolve)});
 return {
  url:`https://127.0.0.1:${server.address().port}/`,audit,
  certificate:{subject:identity.subject,issuer:identity.issuer,validFrom:identity.validFrom,validTo:identity.validTo,trusted:false},
  async close(){await new Promise((resolve,reject)=>{server.close(error=>error?reject(error):resolve());for(const socket of sockets)socket.destroy()})},
 };
}
