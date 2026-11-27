// Build-time only; reuse installed Node headers, never compile at app startup.
import {execFileSync} from 'node:child_process';
import {mkdirSync, mkdtempSync, renameSync, rmSync, statSync} from 'node:fs';
import {dirname, join, resolve} from 'node:path';
import {fileURLToPath} from 'node:url';
const options=new Map();
for(let i=2;i<process.argv.length;i+=2){const k=process.argv[i],v=process.argv[i+1];if(!['--output-dir','--node-include','--sign'].includes(k)||!v||options.has(k))throw Error('Invalid build option');options.set(k,v)}
if(process.platform!=='darwin'||process.arch!=='arm64'||!options.has('--output-dir'))throw Error('Native macOS arm64 build and --output-dir required');
const output=resolve(options.get('--output-dir'));
const include=resolve(options.get('--node-include')??join(dirname(process.execPath),'../include/node'));
if(!statSync(join(include,'node_api.h')).isFile())throw Error('Installed Node headers required');
mkdirSync(output,{recursive:true});const stage=mkdtempSync(join(output,'.preview-build-'));
try{
 const asset=join(stage,'dsh-preview-sandbox.node');
 execFileSync('clang',['-std=c11','-Wall','-Wextra','-Werror','-arch','arm64','-mmacosx-version-min=15.0','-DNODE_GYP_MODULE_NAME=dsh_preview_sandbox',`-I${include}`,'-bundle','-undefined','dynamic_lookup',fileURLToPath(new URL('../internal/platform/mac/native/restrict.c',import.meta.url)),'-lsandbox','-o',asset],{stdio:'inherit'});
 execFileSync('codesign',['--force','--sign',options.get('--sign')??'-',asset],{stdio:'inherit'});
 execFileSync('codesign',['--verify','--strict',asset],{stdio:'inherit'});
 renameSync(asset,join(output,'dsh-preview-sandbox.node'));
}finally{rmSync(stage,{recursive:true,force:true})}
