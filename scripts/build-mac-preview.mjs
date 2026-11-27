// Build the actual Wails application, never a replacement UI/lab process.
// The host retains its existing macOS 12 minimum; the optional CEF component
// and private Node bridge have their separate qualified macOS 15 boundary.
import {execFileSync} from 'node:child_process';
import {copyFileSync,existsSync,mkdirSync,statSync,writeFileSync} from 'node:fs';
import {join,resolve} from 'node:path';
import {fileURLToPath} from 'node:url';
if(process.platform!=='darwin'||process.arch!=='arm64')throw Error('Requires native macOS arm64');
const options=new Map();
for(let i=2;i<process.argv.length;i+=2){const key=process.argv[i],value=process.argv[i+1];if(!['--output-dir','--bridge','--sandbox','--sign'].includes(key)||!value||options.has(key))throw Error('Expected --output-dir DIR --bridge BUILT_ADDON [--sandbox BUILT_ADDON] [--sign IDENTITY]');options.set(key,value)}
if(!options.has('--output-dir')||!options.has('--bridge'))throw Error('Output directory and built same-process bridge are required');
const repo=fileURLToPath(new URL('../',import.meta.url)),output=resolve(options.get('--output-dir'));
if(!existsSync(join(repo,'frontend/dist/index.html')))throw Error('Build the trusted frontend assets first (npm ci && npm run build:dev in frontend)');
const bridge=resolve(options.get('--bridge'));if(!statSync(bridge).isFile())throw Error('Built same-process bridge is required');
const app=join(output,'DSH Work Preview.app'),contents=join(app,'Contents'),bin=join(contents,'MacOS'),executable=join(bin,'dsh-work');mkdirSync(bin,{recursive:true});mkdirSync(join(contents,'Resources'),{recursive:true});
const run=(exe,args,extra={})=>execFileSync(exe,args,{stdio:'inherit',...extra});
const env={...process.env,CGO_ENABLED:'1',MACOSX_DEPLOYMENT_TARGET:'12.0',CGO_CFLAGS:`${process.env.CGO_CFLAGS||''} -mmacosx-version-min=12.0`,CGO_CXXFLAGS:`${process.env.CGO_CXXFLAGS||''} -mmacosx-version-min=12.0`,CGO_LDFLAGS:`${process.env.CGO_LDFLAGS||''} -mmacosx-version-min=12.0`};
run('go',['build','-trimpath','-buildvcs=false','-tags=e67presenter','-o',executable,'.'],{cwd:repo,env});
copyFileSync(bridge,join(bin,'workeripc-mac.node'));
if(options.has('--sandbox')){const sandbox=resolve(options.get('--sandbox'));if(!statSync(sandbox).isFile())throw Error('Built restricted runtime sandbox addon is required');copyFileSync(sandbox,join(bin,'dsh-preview-sandbox.node'))}
writeFileSync(join(contents,'Info.plist'),`<?xml version="1.0" encoding="UTF-8"?>\n<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">\n<plist version="1.0"><dict><key>CFBundleExecutable</key><string>dsh-work</string><key>CFBundleIdentifier</key><string>dev.dsh.work.preview</string><key>CFBundleName</key><string>DSH Work Preview</string><key>CFBundlePackageType</key><string>APPL</string><key>CFBundleVersion</key><string>1</string><key>LSMinimumSystemVersion</key><string>12.0</string><key>NSHighResolutionCapable</key><true/><key>NSAppTransportSecurity</key><dict><key>NSAllowsLocalNetworking</key><true/></dict></dict></plist>\n`);
run('codesign',['--force','--deep','--sign',options.get('--sign')??'-',app]);run('codesign',['--verify','--deep','--strict',app]);
const build={schemaVersion:1,kind:'dsh-work-mac-preview-build',app,executable,hostMinimumOS:'12.0',CEFQualificationOS:'15.0',sameProcessBridge:true,restrictedRuntimeAsset:options.has('--sandbox'),qualification:'build-only'};
writeFileSync(join(output,'mac-preview-build.json'),JSON.stringify(build,null,2)+'\n');console.log(JSON.stringify(build));
