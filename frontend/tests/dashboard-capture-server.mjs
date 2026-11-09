// Loopback-only screenshot sink for the isolated E-68 fixture browser. Not imported by the product.
import http from 'node:http';
import {mkdir,writeFile} from 'node:fs/promises';
import {resolve} from 'node:path';
const root=resolve('output/playwright/e68');await mkdir(root,{recursive:true});
http.createServer(async(req,res)=>{res.setHeader('Access-Control-Allow-Origin','http://127.0.0.1:18876');res.setHeader('Access-Control-Allow-Methods','POST,OPTIONS');if(req.method==='OPTIONS'){res.writeHead(204);res.end();return;}const name=new URL(req.url,'http://127.0.0.1').searchParams.get('name');if(req.method!=='POST'||!name||!/^[-a-zA-Z0-9_.]+\.(png|json|html)$/.test(name)){res.writeHead(400);res.end();return;}const chunks=[];let length=0;for await(const c of req){length+=c.length;if(length>20e6){res.writeHead(413);res.end();return;}chunks.push(c);}const body=Buffer.concat(chunks).toString();await writeFile(resolve(root,name),name.endsWith('.png')?Buffer.from(body,'base64'):body);res.writeHead(200);res.end('saved');}).listen(18877,'127.0.0.1',()=>console.log('E68 screenshot sink 18877'));
