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
run('cmake',['--build',build,'--config','Release','--target','cefsimple','browser_lab_bridge','browser_lab_broker','--parallel','3']);
const cefOut=join(build,'tests/cefsimple/Release'),bundle=join(work,'bundle');mkdirSync(bundle,{recursive:true});
let executable,helper,bridge;
if(process.platform==='darwin'){
 // Preserve relative framework symlinks; fs.cp's default rewrites their targets.
 const broker=join(bundle,'broker.app');run('ditto',[join(cefOut,'cefsimple.app'),broker]);
 copyFileSync(join(build,'lab/browser_lab_broker'),join(broker,'Contents/MacOS/cefsimple'));
 const app=join(bundle,'host.app');mkdirSync(join(app,'Contents/MacOS'),{recursive:true});mkdirSync(join(app,'Contents/Frameworks'),{recursive:true});
 writeFileSync(join(app,'Contents/Info.plist'),`<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd"><plist version="1.0"><dict><key>CFBundleExecutable</key><string>host</string><key>CFBundleIdentifier</key><string>dev.dsh.browserlab.host</string><key>CFBundleName</key><string>Browser Lab Host</string><key>CFBundlePackageType</key><string>APPL</string><key>LSMinimumSystemVersion</key><string>13.0</string><key>NSHighResolutionCapable</key><true/></dict></plist>`);
 executable=join(app,'Contents/MacOS/host');
 helper=join(broker,'Contents/Frameworks/cefsimple Helper.app/Contents/MacOS/cefsimple Helper');
 bridge=join(app,'Contents/Frameworks/libbrowser_lab_bridge.dylib');copyFileSync(join(build,'lab/libbrowser_lab_bridge.dylib'),bridge);
}else{
 cpSync(cefOut,bundle,{recursive:true});executable=join(bundle,'host');helper=join(bundle,'browser_lab_broker');bridge=join(bundle,'libbrowser_lab_bridge.so');copyFileSync(join(build,'lab/libbrowser_lab_bridge.so'),bridge);copyFileSync(join(build,'lab/browser_lab_broker'),helper);
 // The SDK ships its supported setuid sandbox helper. Do not disable sandbox.
 const sandbox=join(bundle,'chrome-sandbox');if(!existsSync(sandbox))throw Error('SDK sandbox helper missing');
 run('sudo',['chown','root:root',sandbox]);run('sudo',['chmod','4755',sandbox]);
}
const hostDirectory=join(root,'native/posix/host');
const env={...process.env};
run('go',['build','-o',executable,'.'],{cwd:hostDirectory,env});
run('go',['build','-o',join(bundle,'gateway'),'.'],{cwd:join(root,'native/gateway')});
if(process.platform==='darwin')run('clang',['-o',join(bundle,'sandbox-probe'),join(root,'native/posix/sandbox-probe.c')]);
if(process.platform==='darwin')for(const app of ['broker.app','host.app'])run('codesign',['--force','--deep','--sign','-',join(bundle,app)]);
writeFileSync(join(bundle,'build.json'),JSON.stringify({schemaVersion:1,kind:'browser-lab-posix-smoke',platform:process.platform,arch:process.arch,version,wails:'v3.0.0-beta.16',created:new Date().toISOString(),sourceCommit:execFileSync('git',['rev-parse','HEAD'],{encoding:'utf8'}).trim(),executable,helper,bridge},null,2));
console.log('Native POSIX bundle: '+bundle);
