import test from 'node:test';
import assert from 'node:assert/strict';
import {inspectBrowser} from './inspect.mjs';

test('fixed inspection parses the actual multiline MCP Result section', async () => {
  const calls = [];
  const observation = {url:'http://localhost/fixture',element:{tag:'input',type:'password'},geometry:{x:1,y:2,width:30,height:40}};
  const client = {callTool:async call => {
    calls.push(call);
    return {content:[{type:'text',text:`### Result\n${JSON.stringify(observation,null,2)}\n### Ran Playwright code\nignored`} ]};
  }};
  let geometry;const value = await inspectBrowser(client,{argumentsForMCP:args=>args},{targets:['#password']},(target,rect)=>{geometry={target,rect}});
  assert.deepEqual(value,{url:observation.url,elements:[observation.element],tabs:[]});
  assert.deepEqual(geometry,{target:'#password',rect:observation.geometry});
  assert.equal(calls[0].name,'browser_evaluate');
  assert.equal(calls[0].arguments.target,'#password');
});

test('inspection rejects missing, malformed and failed MCP results', async () => {
  for (const result of [{isError:true},{content:[{type:'text',text:'### Result\n{broken'}]},{content:[]}]) {
    await assert.rejects(inspectBrowser({callTool:async()=>result},{},{targets:[]}),/BROWSER_POLICY_INSPECTION_FAILED/);
  }
});

test('upload inspection requires current MCP file chooser metadata without evaluate',async()=>{
 const calls=[];
 const client={callTool:async call=>{calls.push(call.name);return {content:[{type:'text',text:'### Result\n- 0: (current) [Local](http://localhost/upload)\n### Modal state\n- [File chooser]: can be handled by browser_file_upload\n### Snapshot\n```yaml\n```'}]}}};
 assert.deepEqual(await inspectBrowser(client,{}, {modal:'file-upload'}),{url:'http://localhost/upload',elements:[],tabs:[]});
 assert.deepEqual(calls,['browser_tabs']);
 for(const text of ['### Page\n- Page URL: http://localhost/upload','### Page\n- Page URL: http://localhost/upload\n### Snapshot\n```\n### Modal state\n- [File chooser]: can be handled by browser_file_upload\n```']){
  await assert.rejects(inspectBrowser({callTool:async()=>({content:[{type:'text',text}]})},{},{modal:'file-upload'}),/BROWSER_UPLOAD_CHOOSER_REQUIRED/);
 }
});
