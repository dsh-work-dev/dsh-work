import {pathToFileURL} from 'node:url';
import http from 'node:http';

const html=`<!doctype html><html lang="zh-CN"><meta charset="utf-8"><title>Browser Lab Tab</title><style>body{margin:0;padding:26px;background:#123e50;color:#e8fbff;font:18px system-ui}button,input,a{font:inherit;padding:9px;margin:5px;display:inline-block}a{color:#8ff3cf}output{display:block;padding:12px;background:#076e60;margin:12px 0}p{line-height:1.6}</style><div id="guest-marker" style="width:16px;height:16px;background:#ed3ca5"></div><h1 id="name"></h1><p id="identity"></p><label>页面输入区<input id="entry" aria-label="页面输入区"></label><input id="password" type="password" aria-label="测试密码"><button id="apply">应用</button><output id="result">等待输入</output><button id="open-window">打开测试窗口</button><a id="open-tab" href="/popup?kind=blank" target="_blank" rel="noopener">新标签链接</a><a id="download-link" href="/download?kind=normal">下载测试文件</a><a id="download-traversal" href="/download?kind=traversal">下载异常文件名</a><a id="download-cancel" href="/download?kind=slow">取消测试下载</a><p id="storage-state"></p><script src="/batch.js"></script></html>`;
const js=`window.lab={tab:new URL(location).searchParams.get('tab'),instance:crypto.randomUUID(),ticks:0,events:[]};document.title='Browser Lab Tab '+lab.tab;document.querySelector('#name').textContent='页面 '+lab.tab;document.querySelector('#identity').textContent=lab.instance;setInterval(()=>lab.ticks++,250);window.businessClickCount=0;document.querySelector('#apply').onclick=()=>{businessClickCount++;document.querySelector('#result').textContent=document.querySelector('#entry').value};document.querySelector('#open-window').onclick=()=>{window.open('/popup?kind=script','browser-lab-batch-popup','width=600,height=400')};document.querySelector('#storage-state').textContent='集中验证：新窗、下载、权限与独立存储';`;
export function createFixtureServer(){const audit={requests:[],downloads:[]};return http.createServer((req,res)=>{
 const u=new URL(req.url,'http://127.0.0.1:19478');audit.requests.push({time:Date.now(),path:u.pathname,kind:u.searchParams.get('kind')});res.setHeader('Cache-Control','no-store');
 if(u.pathname==='/audit'){res.setHeader('Content-Type','application/json');res.end(JSON.stringify(audit));return}
 if(u.pathname==='/batch.js'){res.setHeader('Content-Type','text/javascript; charset=utf-8');res.end(js);return}
 if(u.pathname==='/interactions'){res.setHeader('Content-Type','text/html; charset=utf-8');res.end(html);return}
 if(u.pathname==='/popup'){res.setHeader('Content-Type','text/html; charset=utf-8');res.end('<!doctype html><meta charset="utf-8"><title>BROWSER_LAB isolated popup</title><h1 id="popup-only">隔离测试弹窗</h1><p>仅含本地测试内容</p>');return}
 if(u.pathname==='/download'){
  const kind=u.searchParams.get('kind');const item={time:Date.now(),kind,bytes:0,closed:false};audit.downloads.push(item);
  res.setHeader('Content-Type','application/octet-stream');res.setHeader('Content-Disposition',kind==='traversal'?'attachment; filename="../../browser-lab-batch-outside.txt"':'attachment; filename="browser-lab-batch-'+kind+'.txt"');
  res.on('close',()=>{item.closed=true;item.closedAt=Date.now()});
  if(kind==='slow'){const timer=setInterval(()=>{if(res.destroyed){clearInterval(timer);return}const data='BROWSER_LAB slow download test\n';item.bytes+=Buffer.byteLength(data);res.write(data)},150);res.on('close',()=>clearInterval(timer));return}
  const data='BROWSER_LAB isolated download '+kind+'\n';item.bytes=Buffer.byteLength(data);res.end(data);return
 }
 res.writeHead(404);res.end('not found');
})}
if(process.argv[1]&&import.meta.url===pathToFileURL(process.argv[1]).href){const server=createFixtureServer();server.listen(19478,'127.0.0.1',()=>console.log('Browser Lab fixture ready'))}
