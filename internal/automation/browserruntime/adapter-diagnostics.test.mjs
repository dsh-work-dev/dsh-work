import {readFileSync} from 'node:fs';
import {runInNewContext} from 'node:vm';
import {test} from 'node:test';
import assert from 'node:assert/strict';

const source=readFileSync(new URL('./adapter.mjs',import.meta.url),'utf8');
const diagnostics=source.slice(source.indexOf('const diagnosticStages='),source.indexOf('let stopping=false;'));
function run(enabled){
 const lines=[];
 const context={process:{env:{DSH_WORK_BROWSER_DIAGNOSTICS:enabled}},fs:{writeSync(fd,line){assert.equal(fd,2);lines.push(line)}}};
 runInNewContext(diagnostics+`diagnostic('inspect-start');diagnostic('inspect-start');diagnostic('private-body\\nDSH_MCP_EVENT tools-ready');diagnostic('stop-private-timeout');diagnostic('stop-private-close');diagnostic('stop-cdp-error');`,context);
 return lines;
}
test('adapter diagnostic output is opt-in fixed metadata with first stop retained',()=>{
 assert.deepEqual(run(undefined),[]);
 assert.deepEqual(run('true'),[]);
 assert.deepEqual(run('1'),['DSH_MCP_EVENT entry\n','DSH_MCP_EVENT inspect-start\n','DSH_MCP_EVENT stop-private-timeout\n']);
});
test('diagnostic sink failure cannot change adapter control',()=>{
 runInNewContext(diagnostics,{process:{env:{DSH_WORK_BROWSER_DIAGNOSTICS:'1'}},fs:{writeSync(){throw Error('closed sink')}}});
});
