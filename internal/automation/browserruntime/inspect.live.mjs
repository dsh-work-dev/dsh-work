// Explicit local-only MCP contract check. No model, account, extension registration
// or existing browser profile. The separate formal-chain evidence covers routing.
import {createRequire} from 'node:module';
import {pathToFileURL} from 'node:url';
import path from 'node:path';
import fs from 'node:fs';
import os from 'node:os';
import http from 'node:http';
import assert from 'node:assert/strict';
import {inspectBrowser} from './inspect.mjs';
const dependencies=process.argv[2];
if(!dependencies)throw Error('Pass the existing locked MCP dependency directory.');
const require=createRequire(path.join(dependencies,'package.json'));
assert.equal(require('@playwright/mcp/package.json').version,'0.0.83');
const {Client}=await import(pathToFileURL(require.resolve('@modelcontextprotocol/sdk/client/index.js')));
const {StdioClientTransport}=await import(pathToFileURL(require.resolve('@modelcontextprotocol/sdk/client/stdio.js')));
const directory=fs.mkdtempSync(path.join(os.tmpdir(),'dsh-policy-inspect-'));
const fixture=fs.readFileSync(new URL('../../../.research/assets/E-53/gateway/fixture.html',import.meta.url));
const server=http.createServer((_req,res)=>res.writeHead(200,{'content-type':'text/html;charset=utf-8'}).end(fixture));
await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
const client=new Client({name:'DSH-Work-policy-contract',version:'0.1.0'});
const transport=new StdioClientTransport({command:process.execPath,args:[path.join(dependencies,'node_modules/@playwright/mcp/cli.js'),'--browser','msedge','--headless','--isolated','--output-dir',directory],stderr:'pipe'});
try{
 const connected=client.connect(transport);transport.stderr?.resume();await connected;
 const url=`http://127.0.0.1:${server.address().port}/fixture.html`;
 const nav=await client.callTool({name:'browser_navigate',arguments:{url}});assert.ok(!nav.isError);
 const refs={argumentsForMCP:args=>args};
 let last;const observationClient={callTool:async(...args)=>{last=await client.callTool(...args);return last}};
 let observed;try{observed=await inspectBrowser(observationClient,refs,{targets:['#search','#password','#otp','#message','#payment'],tabs:true})}catch(error){console.error(JSON.stringify(last));throw error}
 assert.equal(observed.url,url);assert.equal(observed.elements[0].type,'search');assert.equal(observed.elements[0].name,'Search');
 assert.equal(observed.elements[1].type,'password');assert.equal(observed.elements[2].autocomplete,'one-time-code');
 assert.equal(observed.elements[3].multiline,true);assert.equal(observed.elements[3].inForm,true);assert.equal(observed.elements[3].submitName,'Send message');
 assert.equal(observed.elements[4].name,'Buy now');assert.equal(observed.tabs[0].url,url);
 const chooser=await client.callTool({name:'browser_click',arguments:{target:'#upload'}});assert.ok(!chooser.isError);
 let modalResult;const modalClient={callTool:async(...args)=>{modalResult=await client.callTool(...args);return modalResult}};
 let uploadEvidence;try{uploadEvidence=await inspectBrowser(modalClient,refs,{modal:'file-upload'})}catch(error){console.error(JSON.stringify(modalResult));throw error}
 assert.equal(uploadEvidence.url,url);assert.deepEqual(uploadEvidence.elements,[]);
 const upload=path.join(directory,'dummy-upload.txt');fs.writeFileSync(upload,'local dummy upload');
 const uploaded=await client.callTool({name:'browser_file_upload',arguments:{paths:[upload]}});assert.ok(!uploaded.isError);
 const snapshot=await client.callTool({name:'browser_snapshot',arguments:{}});assert.ok(!snapshot.isError);
 assert.match(snapshot.content.filter(v=>v.type==='text').map(v=>v.text).join('\n'),/dummy-upload\.txt/);
 console.log(JSON.stringify({passed:true,localOnly:true,isolatedProfile:true,modelCalls:0,metadataFields:observed.elements.length,tabs:observed.tabs.length,modalUpload:true}));
}finally{await client.close().catch(()=>{});await transport.close().catch(()=>{});await new Promise(resolve=>server.close(resolve));fs.rmSync(directory,{recursive:true,force:true});}
