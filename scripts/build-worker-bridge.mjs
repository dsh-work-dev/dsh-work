import {build} from '../frontend/node_modules/vite/dist/node/index.js';
import {fileURLToPath} from 'node:url';
import {readFileSync,writeFileSync,mkdirSync} from 'node:fs';
import {join,dirname} from 'node:path';
const root=fileURLToPath(new URL('../',import.meta.url));
const outDir=join(root,'internal/desktopbridge/assets');
const result=await build({
  configFile:false,root:root+'frontend',publicDir:false,
  build:{outDir,write:false,emptyOutDir:false,
    rolldownOptions:{external:['@wailsio/runtime'],output:{paths:{'@wailsio/runtime':'/wails/runtime.js'}}},
    lib:{entry:root+'internal/desktopbridge/client.ts',formats:['es'],fileName:()=> 'client.js'},
    minify:true},
});
// The running dev watcher observes embedded JS. Rewriting identical generated
// bytes can schedule another full build forever, so only publish changed bytes.
for(const bundle of Array.isArray(result)?result:[result])for(const output of bundle.output){
  const destination=join(outDir,output.fileName);
  const bytes=Buffer.from(output.type==='chunk'?output.code:output.source);
  let previous;try{previous=readFileSync(destination)}catch(error){if(error.code!=='ENOENT')throw error}
  if(previous?.equals(bytes))continue;
  mkdirSync(dirname(destination),{recursive:true});writeFileSync(destination,bytes);
}
