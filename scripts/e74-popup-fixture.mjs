// Local-only real-app popup acceptance fixture. Start explicitly and keep it
// owned by the verifier; no app process or existing browser state is touched.
import http from 'node:http';
const server=http.createServer((req,res)=>{
 res.setHeader('Content-Type','text/html; charset=utf-8');res.setHeader('Cache-Control','no-store');
 if(req.url==='/target'){res.end('<!doctype html><title>Popup target</title><h1>New controlled tab</h1><p id="opener"></p><script>document.querySelector("#opener").textContent="opener: "+String(window.opener)</script>');return}
 res.end(`<!doctype html><meta charset="utf-8"><title>Popup source</title><style>body{font:18px system-ui;padding:28px}button,input{font:inherit;padding:10px;margin:8px}output{display:block;margin:20px}</style>
 <h1>Popup source</h1><label>Source marker <input id="marker" value="source unchanged"></label>
 <p><button id="open" onclick="document.querySelector('output').textContent='window.open returned '+String(window.open('/target','_blank'))">Open controlled tab</button>
 <button id="unknown" onclick="document.querySelector('output').textContent=String(window.open('javascript:void(0)','_blank'))">Unknown scheme</button>
 <a href="/next">Navigate source</a></p><output>ready</output>
 ${req.url==='/auto'?`<script>document.querySelector('output').textContent='no gesture returned '+String(window.open('/target','_blank'))</script>`:''}`);
});
server.listen(0,'127.0.0.1',()=>{const {port}=server.address();process.stdout.write(JSON.stringify({pid:process.pid,origin:'http://127.0.0.1:'+port,source:'http://127.0.0.1:'+port+'/source',automatic:'http://127.0.0.1:'+port+'/auto',target:'http://127.0.0.1:'+port+'/target'})+'\n')});
for(const event of['SIGINT','SIGTERM'])process.on(event,()=>server.close());
