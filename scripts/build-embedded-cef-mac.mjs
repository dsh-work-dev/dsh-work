// Explicit development/release build, never called during app startup. Inputs
// are the approved official SDK and an owned build root, not Browser Lab output.
import {execFileSync} from 'node:child_process';
import {copyFileSync, existsSync, mkdirSync, readdirSync, readFileSync, writeFileSync} from 'node:fs';
import {join, resolve} from 'node:path';
import {fileURLToPath} from 'node:url';

if (process.platform !== 'darwin' || process.arch !== 'arm64') throw Error('Requires native macOS arm64');
const options = new Map();
for (let i=2;i<process.argv.length;i+=2) {
  if (!['--cef-root','--output-dir'].includes(process.argv[i]) || !process.argv[i+1]) throw Error('Expected --cef-root SDK --output-dir DIR');
  options.set(process.argv[i],resolve(process.argv[i+1]));
}
if (options.size !== 2) throw Error('Expected --cef-root SDK --output-dir DIR');
const sdk=options.get('--cef-root'),output=options.get('--output-dir');
const repo=fileURLToPath(new URL('../',import.meta.url)),version='156.0.15+gcf671c0+chromium-156.0.8078.80';
if (!readFileSync(join(sdk,'include/cef_version.h'),'utf8').includes(version)) throw Error('Approved CEF156 SDK required');
if (readFileSync(join(sdk,'CMakeLists.txt'),'utf8').includes('# browser-lab bridge target')) throw Error('Formal build requires an unmodified official SDK');
mkdirSync(output,{recursive:true});
const run=(exe,args,extra={})=>execFileSync(exe,args,{stdio:'inherit',...extra});
const official=join(output,'official'),build=join(output,'native'),app=join(output,'broker.app');
run('cmake',['-S',sdk,'-B',official,'-DCMAKE_BUILD_TYPE=Release','-DCMAKE_OSX_ARCHITECTURES=arm64','-DCMAKE_OSX_DEPLOYMENT_TARGET=15.0','-DUSE_SANDBOX=ON']);
run('cmake',['--build',official,'--config','Release','--target','cefsimple','--parallel','3']);
const wrapper=join(official,'libcef_dll_wrapper/libcef_dll_wrapper.a');
if (!existsSync(wrapper)) throw Error('Official SDK wrapper missing');
run('cmake',['-S',join(repo,'native/embeddedbrowser/mac'),'-B',build,`-DCEF_ROOT=${sdk}`,`-DCEF_WRAPPER_LIBRARY=${wrapper}`,'-DCMAKE_BUILD_TYPE=Release','-DCMAKE_OSX_ARCHITECTURES=arm64','-DCMAKE_OSX_DEPLOYMENT_TARGET=15.0','-DUSE_SANDBOX=ON']);
run('cmake',['--build',build,'--parallel','3']);
run('ditto',[join(official,'tests/cefsimple/Release/cefsimple.app'),app]);
const executable=join(app,'Contents/MacOS/cefsimple'),owner=join(app,'Contents/MacOS/dsh-cef-owner');
copyFileSync(join(build,'out/dsh_cef_broker'),executable);
run('go',['build','-o',owner,'./cmd/dsh-cef-owner-mac'],{cwd:repo});
const helpers=[];
for (const entry of readdirSync(join(app,'Contents/Frameworks'),{withFileTypes:true})) {
  if (!entry.isDirectory() || !/^cefsimple Helper.*\.app$/.test(entry.name)) continue;
  const helper=join(app,'Contents/Frameworks',entry.name);
  const name=execFileSync('plutil',['-extract','CFBundleExecutable','raw','-o','-',join(helper,'Contents/Info.plist')],{encoding:'utf8'}).trim();
  const target=join(helper,'Contents/MacOS',name);if(!existsSync(target))throw Error('Official helper layout missing');
  const identifier=execFileSync('plutil',['-extract','CFBundleIdentifier','raw','-o','-',join(helper,'Contents/Info.plist')],{encoding:'utf8'}).trim();
  if(!identifier.startsWith('org.cef.cefsimple'))throw Error('Unexpected official helper bundle identity');
  run('plutil',['-replace','CFBundleIdentifier','-string',identifier.replace('org.cef.cefsimple','dev.dsh.embeddedbrowser.broker'),join(helper,'Contents/Info.plist')]);
  copyFileSync(join(build,'out/dsh_cef_helper'),target);run('codesign',['--force','--sign','-',helper]);helpers.push(target);
}
if (!helpers.length) throw Error('Official helper bundles missing');
function noCrashConfig(dir) {
  for (const entry of readdirSync(dir,{withFileTypes:true})) {
    const path=join(dir,entry.name);
    if(entry.isDirectory())noCrashConfig(path);else if(entry.name==='crash_reporter.cfg')throw Error('Unqualified Crashpad configuration');
  }
}
noCrashConfig(app);
run('plutil',['-replace','CFBundleIdentifier','-string','dev.dsh.embeddedbrowser.broker',join(app,'Contents/Info.plist')]);
run('plutil',['-replace','LSMinimumSystemVersion','-string','15.0',join(app,'Contents/Info.plist')]);
run('codesign',['--force','--sign','-',owner]);
run('codesign',['--force','--deep','--sign','-',app]);
run('codesign',['--verify','--deep','--strict',app]);
const manifest={kind:'dsh-owned-cef-mac',version,app,executable,owner,helpers,sandbox:true,minimumOS:'15.0',architecture:'arm64'};
writeFileSync(join(output,'publication.json'),`${JSON.stringify(manifest,null,2)}\n`);
console.log(`Formal Mac CEF publication: ${join(output,'publication.json')}`);
