import test from 'node:test';
import assert from 'node:assert/strict';
import vm from 'node:vm';
import ts from 'typescript';
import {readFileSync} from 'node:fs';
const deferred=()=>{let resolve;const promise=new Promise(r=>resolve=r);return{promise,resolve}};
const tick=()=>new Promise(resolve=>setImmediate(resolve));
test('initial lifecycle renders before inventory and cannot overwrite a newer event',async()=>{
 const source=readFileSync(new URL('../src/startup.ts',import.meta.url),'utf8');
 const body=source.slice(source.indexOf('  async function initialize()'),source.indexOf('  void initialize();'));
 const status=deferred(),inventory=deferred(),rendered=[];
 const context=vm.createContext({HostService:{GetLocale:async()=> 'en',GetStatus:()=>status.promise},ManagerService:{RefreshDSHReleases:async()=>({})},applyLocale(){},status:{state:'Starting'},lifecycleRevision:0,render(){rendered.push(context.status.state)},refreshEnvironment:()=>inventory.promise,connectionFailed:false,lastError:'',renderEnvironment(){},poll:async()=>{},showError:e=>{throw e},t:x=>x});
 vm.runInContext(ts.transpileModule(body+'\nglobalThis.begin=initialize',{compilerOptions:{target:ts.ScriptTarget.ES2022}}).outputText,context);
 const running=context.begin();await tick();context.lifecycleRevision++;context.status={state:'Failed'};status.resolve({state:'Starting'});await tick();
 assert.deepEqual(rendered,['Failed']);inventory.resolve();await running;assert.equal(context.status.state,'Failed');
});
