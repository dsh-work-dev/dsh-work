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
 const context=vm.createContext({HostService:{GetLocale:async()=> 'en',GetStatus:()=>status.promise},applyLocale(){},status:{state:'Starting'},lifecycleRevision:0,render(){rendered.push(context.status.state)},refreshEnvironment:()=>inventory.promise,connectionFailed:false,lastError:'',prepareInitialEnvironment(){},poll:async()=>{},showError:e=>{throw e},t:x=>x});
 vm.runInContext(ts.transpileModule(body+'\nglobalThis.begin=initialize',{compilerOptions:{target:ts.ScriptTarget.ES2022}}).outputText,context);
 const running=context.begin();await tick();context.lifecycleRevision++;context.status={state:'Failed'};status.resolve({state:'Starting'});await tick();
 assert.deepEqual(rendered,['Failed']);inventory.resolve();await running;assert.equal(context.status.state,'Failed');
});

test('fresh Mac startup prepares the pinned runtime once and preserves existing contexts', async () => {
 const source = readFileSync(new URL('../src/startup.ts', import.meta.url), 'utf8');
 const body = source.slice(source.indexOf('  function prepareInitialEnvironment()'), source.indexOf('  releases.addEventListener('));
 const calls = [];
 const context = vm.createContext({
   busy: false, disposed: false, bootstrapAttempted: false,
   snapshot: {bootstrapDshVersion: '0.2.2-rc.1', configured: {runtimeId: 'existing'}},
   status: {error: {code: 'PROFILE_REQUIRED'}},
   active: () => false,
   installedNode: () => false,
   refreshStartupReleases: () => calls.push('refresh'),
   prepareAndStart: version => calls.push(version),
   action: (work, kind) => { calls.push(kind); work(); },
 });
 vm.runInContext(ts.transpileModule(body, {compilerOptions: {target: ts.ScriptTarget.ES2022}}).outputText, context);
 context.prepareInitialEnvironment();
 context.snapshot.configured = undefined;
 context.prepareInitialEnvironment();
 context.prepareInitialEnvironment();
 assert.deepEqual(calls, ['refresh', 'node', '0.2.2-rc.1']);
});

test('startup release discovery waits for Node installation and skips an available DSH selection', () => {
 const source = readFileSync(new URL('../src/startup.ts', import.meta.url), 'utf8');
 const body = source.slice(source.indexOf('  function refreshStartupReleases()'), source.indexOf('  releases.addEventListener('));
 const refreshReleases = () => {};
 const actions = [];
 const context = vm.createContext({
   busy: false, hostActive: false, nodeInstalled: false, runtimeInstalled: false, versionAvailable: false,
   active: () => context.hostActive,
   installedNode: () => context.nodeInstalled,
   installedRuntime: () => context.runtimeInstalled,
   downloadableVersion: () => context.versionAvailable,
   action: work => { actions.push(work); },
   refreshReleases,
 });
 vm.runInContext(ts.transpileModule(body, {compilerOptions: {target: ts.ScriptTarget.ES2022}}).outputText, context);
 context.refreshStartupReleases();
 context.nodeInstalled = true;
 context.busy = true;
 context.refreshStartupReleases();
 context.busy = false;
 context.hostActive = true;
 context.refreshStartupReleases();
 context.hostActive = false;
 context.versionAvailable = true;
 context.refreshStartupReleases();
 context.versionAvailable = false;
 context.runtimeInstalled = true;
 context.refreshStartupReleases();
 assert.deepEqual(actions, []);
 context.runtimeInstalled = false;
 context.refreshStartupReleases();
 assert.deepEqual(actions, [refreshReleases]);
});
