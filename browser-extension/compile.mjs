// Uses the repository's locked frontend build toolchain, never a global download.
import fs from 'node:fs';
import {fileURLToPath} from 'node:url';
import {build} from '../frontend/node_modules/esbuild/lib/main.js';
export async function compileRoutes() {
 const result=await build({entryPoints:[fileURLToPath(new URL('src/routes.ts',import.meta.url))],bundle:true,format:'esm',platform:'browser',write:false,loader:{'.png':'dataurl'},
 banner:{js:'/*! Derived from Microsoft Playwright. Copyright (c) Microsoft Corporation. Apache-2.0; see LICENSE, NOTICE and PROVENANCE.txt. */'}});
 return result.outputFiles[0].contents;
}
if(process.argv[1]===fileURLToPath(import.meta.url)){
 fs.writeFileSync(new URL('routes.js',import.meta.url),await compileRoutes());
 await build({entryPoints:[fileURLToPath(new URL('playwright/cdpRelayV2.ts',import.meta.url))],bundle:true,format:'esm',platform:'node',outfile:fileURLToPath(new URL('../internal/automation/browserruntime/cdp-adapter.mjs',import.meta.url)),banner:{js:'/*! Microsoft Playwright, Apache-2.0. See browser-extension/LICENSE, NOTICE and PROVENANCE.txt. */'}});
}
