// Development schema capture against the locked MCP. Does not execute tools.
import fs from 'node:fs';
import path from 'node:path';
import {createRequire} from 'node:module';
import {pathToFileURL} from 'node:url';
const root=path.resolve(process.argv[2]),require=createRequire(path.join(root,'package.json'));
for(const [name,version] of [['@playwright/mcp','0.0.83'],['@modelcontextprotocol/sdk','1.32.2']])if(JSON.parse(fs.readFileSync(path.join(root,'node_modules',name,'package.json'))).version!==version)throw Error('Pinned dependency mismatch');
const {Client}=await import(pathToFileURL(require.resolve('@modelcontextprotocol/sdk/client/index.js')));
const {StdioClientTransport}=await import(pathToFileURL(require.resolve('@modelcontextprotocol/sdk/client/stdio.js')));
const transport=new StdioClientTransport({command:process.execPath,args:[path.join(root,'node_modules/@playwright/mcp/cli.js'),'--cdp-endpoint','ws://127.0.0.1:1/not-connected'],env:{},stderr:'pipe'});
const client=new Client({name:'DSH-Work-schema-capture',version:'0.2.0'});
try{await client.connect(transport);const result=await client.listTools();fs.writeFileSync(new URL('./schemas.json',import.meta.url),JSON.stringify(result.tools,null,2)+'\n');console.log(`Captured ${result.tools.length} schemas; executed 0 tools`)}finally{await client.close();await transport.close()}
