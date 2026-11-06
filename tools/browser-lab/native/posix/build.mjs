import {execFileSync} from 'node:child_process';
import {mkdirSync,existsSync,readFileSync,writeFileSync,appendFileSync,cpSync,copyFileSync,readdirSync} from 'node:fs';
import {join,resolve} from 'node:path';
import {fileURLToPath} from 'node:url';
const root=fileURLToPath(new URL('../../',import.meta.url)),work=join(root,'.work/posix');mkdirSync(work,{recursive:true});
const version='156.0.15+gcf671c0+chromium-156.0.8078.80';
const platform=process.platform==='darwin'?(process.arch==='arm64'?'macosarm64':'macosx64'):process.platform==='linux'&&process.arch==='x64'?'linux64':null;
if(!platform)throw Error('Unsupported native SDK platform');
const name=`cef_binary_${version}_${platform}`,archive=join(work,name+'.tar.bz2'),sdk=join(work,name),build=join(work,'build');
const run=(exe,args,options={})=>execFileSync(exe,args,{stdio:'inherit',...options});
if(!existsSync(sdk)){
 run('curl',['--fail','--location','--retry','3','--output',archive,'https://cef-builds.spotifycdn.com/'+encodeURIComponent(name)+'.tar.bz2']);
 run('tar',['-xjf',archive,'-C',work]);
}
const cmake=join(sdk,'CMakeLists.txt');const marker='# browser-lab bridge target';
if(!readFileSync(cmake,'utf8').includes(marker))appendFileSync(cmake,`\n${marker}\nadd_subdirectory("${join(root,'native/posix/bridge')}" "browser-lab-bridge")\n`);
const args=['-S',sdk,'-B',build,'-DCMAKE_BUILD_TYPE=Release','-DUSE_SANDBOX=ON'];
if(process.platform==='darwin')args.push('-DCMAKE_OSX_ARCHITECTURES='+process.arch.replace('x64','x86_64'));
run('cmake',args);
run('cmake',['--build',build,'--config','Release','--target','cefsimple','browser_lab_bridge','--parallel','3']);
const cefOut=join(build,'tests/cefsimple/Release'),bundle=join(work,'bundle');mkdirSync(bundle,{recursive:true});
let executable,helper,bridge;
if(process.platform==='darwin'){
 // Preserve relative framework symlinks; fs.cp's default rewrites their targets.
 const app=join(bundle,'cefsimple.app');run('ditto',[join(cefOut,'cefsimple.app'),app]);
 executable=join(app,'Contents/MacOS/cefsimple');
 helper=join(app,'Contents/Frameworks/cefsimple Helper.app/Contents/MacOS/cefsimple Helper');
 bridge=join(app,'Contents/Frameworks/libbrowser_lab_bridge.dylib');copyFileSync(join(build,'lab/libbrowser_lab_bridge.dylib'),bridge);
}else{
 cpSync(cefOut,bundle,{recursive:true});executable=join(bundle,'host');helper=join(bundle,'cefsimple');bridge=join(bundle,'libbrowser_lab_bridge.so');copyFileSync(join(build,'lab/libbrowser_lab_bridge.so'),bridge);
 // The SDK ships its supported setuid sandbox helper. Do not disable sandbox.
 const sandbox=join(bundle,'chrome-sandbox');if(!existsSync(sandbox))throw Error('SDK sandbox helper missing');
 run('sudo',['chown','root:root',sandbox]);run('sudo',['chmod','4755',sandbox]);
}
run('go',['build','-o',executable,'.'],{cwd:join(root,'native/posix/host')});
if(process.platform==='darwin')run('codesign',['--force','--deep','--sign','-',join(bundle,'cefsimple.app')]);
writeFileSync(join(bundle,'build.json'),JSON.stringify({schemaVersion:1,kind:'browser-lab-posix-smoke',platform:process.platform,arch:process.arch,version,wails:'v3.0.0-beta.16',created:new Date().toISOString(),sourceCommit:execFileSync('git',['rev-parse','HEAD'],{encoding:'utf8'}).trim(),executable,helper,bridge},null,2));
console.log('Native POSIX bundle: '+bundle);
