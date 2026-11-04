import test from 'node:test';
import assert from 'node:assert/strict';
import {createRefScope} from './refs.mjs';
test('identical raw MCP refs cannot cross resources or generations',()=>{
 const a=createRefScope('a'),b=createRefScope('b');
 const actual={content:[{type:'text',text:'### Snapshot\n```yaml\n- button "Apply [ref=e9]" [ref=e1]\n```'}]};
 assert.equal(a.resultForAgent(actual).content[0].text,'### Snapshot\n```yaml\n- button "Apply [ref=e9]" [ref=dsh-a:e1]\n```');
 assert.equal(actual.content[0].text,'### Snapshot\n```yaml\n- button "Apply [ref=e9]" [ref=e1]\n```');
 assert.equal(a.resultForAgent({content:[{type:'text',text:'### Page\n- Page Title: literal [ref=e1]'}]}).content[0].text,'### Page\n- Page Title: literal [ref=e1]');
 assert.equal(a.resultForAgent({content:[{type:'text',text:'### Find results\nFound 1 match:\n- button "Apply" [ref=e1]'}]}).content[0].text,'### Find results\nFound 1 match:\n- button "Apply" [ref=dsh-a:e1]');
 assert.deepEqual(a.argumentsForMCP({target:'dsh-a:e1',text:'dsh-b:e1'}),{target:'e1',text:'dsh-b:e1'});
 assert.throws(()=>a.argumentsForMCP({target:'dsh-b:e1'}),/FOREIGN_OR_STALE/);
 assert.throws(()=>b.argumentsForMCP({fields:[{target:'dsh-a:e1'}]}),/FOREIGN_OR_STALE/);
 assert.throws(()=>a.argumentsForMCP({target:'e1'}),/FOREIGN_OR_STALE/);
 assert.deepEqual(a.argumentsForMCP({target:"getByRole('button')"}),{target:"getByRole('button')"});
});

test('only locked target slots are decoded; custom selectors and MIME values stay literal',()=>{
 const a=createRefScope('a');
 const args={target:'dsh-panel:has(button)',data:{target:'e1',ref:'dsh-b:e2'}};
 assert.deepEqual(a.argumentsForMCP(args),args);
 assert.deepEqual(a.argumentsForMCP({fields:[{target:'dsh-a:e1',value:'dsh-b:e2'}]}),{fields:[{target:'e1',value:'dsh-b:e2'}]});
});
