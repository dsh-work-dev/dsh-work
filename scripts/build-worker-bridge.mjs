import {build} from '../frontend/node_modules/vite/dist/node/index.js';
import {fileURLToPath} from 'node:url';
const root=fileURLToPath(new URL('../',import.meta.url));
await build({
  configFile:false,root:root+'frontend',publicDir:false,
  build:{outDir:root+'internal/desktopbridge/assets',emptyOutDir:true,
    rolldownOptions:{external:['@wailsio/runtime'],output:{paths:{'@wailsio/runtime':'/wails/runtime.js'}}},
    lib:{entry:root+'internal/desktopbridge/client.ts',formats:['es'],fileName:()=> 'client.js'},
    minify:true},
});
