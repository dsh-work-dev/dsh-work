// Package desktopprobe supplies opt-in native desktop integration checks.
package desktopprobe

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/local/dsh-work/internal/desktopbridge"
	"github.com/local/dsh-work/internal/dshadapter"
	"github.com/local/dsh-work/internal/dshmanager"
)

func Patch(root, discoveryRoot string) func(string) (string, error) {
	return func(patch string) (string, error) {
		if err := os.MkdirAll(root, 0700); err != nil {
			return "", err
		}
		path, err := filepath.Abs(filepath.Join(root, "probe.mjs"))
		if err != nil {
			return "", err
		}
		wsURL := (&url.URL{Scheme: "file", Path: "/" + filepath.ToSlash(filepath.Join(discoveryRoot, "tools", "dsh", "node_modules", "ws", "wrapper.mjs"))}).String()
		encodedWS, _ := json.Marshal(wsURL)
		pluginSource := "import {WebSocketServer} from " + string(encodedWS) + ";\n" + probePlugin
		if err = os.WriteFile(path, []byte(pluginSource), 0600); err != nil {
			return "", err
		}
		b, err := os.ReadFile(patch)
		if err != nil {
			return "", err
		}
		var rows []any
		if err = json.Unmarshal(b, &rows); err != nil {
			return "", err
		}
		u := (&url.URL{Scheme: "file", Path: "/" + filepath.ToSlash(path)}).String()
		rows = append(rows, map[string]any{"insert": []any{map[string]any{"id": "pc-ipc-probe", "name": u}}})
		b, err = json.Marshal(rows)
		if err != nil {
			return "", err
		}
		return patch, os.WriteFile(patch, b, 0600)
	}
}

const probePlugin = `import {once} from 'node:events';
import {spawn} from 'node:child_process';
export const inject=['webServer','sessions','connection'];
export function apply(ctx){
let cancelled=0,backgroundTicks=0,backgroundTimer,backgroundChild;
const ws=new WebSocketServer({noServer:true});
ws.on('connection',socket=>{socket.on('message',(data,binary)=>socket.send(data,{binary}));});
ctx.effect(()=>ctx.webServer.registerUpgrade({path:'/__work/probe-ws',handler:(req,socket,head)=>{if(ctx.connection.requestRejection(req)){socket.destroy();return;}ws.handleUpgrade(req,socket,head,client=>ws.emit('connection',client));}}));
ctx.effect(()=>()=>{for(const socket of ws.clients)socket.terminate();ws.close();});
ctx.effect(()=>ctx.webServer.register({kind:'exact',path:'/__work/probe',handler:async(req,res)=>{
const action=new URL(req.url,'http://local').searchParams.get('action');
if(action==='background'){if(!backgroundTimer){backgroundTimer=setInterval(()=>backgroundTicks++,100);backgroundChild=spawn(process.execPath,['-e','setInterval(()=>{},1000)'],{windowsHide:true,stdio:'ignore'});}res.end('started');return;}
if(action==='background-status'){res.setHeader('content-type','application/json');res.end(JSON.stringify({ticks:backgroundTicks,pid:process.pid,child:backgroundChild?.pid}));return;}
if(action==='echo'){res.writeHead(200,{'content-type':'application/octet-stream'});for await(const chunk of req){if(!res.write(chunk))await once(res,'drain');}res.end();return;}
if(action==='stream'){res.writeHead(200,{'content-type':'application/octet-stream'});res.write('first');const timer=setTimeout(()=>res.end('last'),30000);res.once('close',()=>{clearTimeout(timer);cancelled++;});return;}
if(action==='session'){let s=ctx.sessions.get('pc-ipc-probe');if(!s)s=ctx.sessions.create('pc-ipc-probe');s.append('turn/start',{turn:1});s.append('turn/end',{turn:1,reason:{kind:'completed'}});res.end('ok');return;}
res.setHeader('content-type','application/json');res.end(JSON.stringify({cancelled}));
}}));
}`

func Assets(bridge *desktopbridge.Bridge) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			bridge.Assets(w, r)
			return
		}
		recorder := httptest.NewRecorder()
		bridge.Assets(recorder, r)
		for k, v := range recorder.Header() {
			w.Header()[k] = v
		}
		w.WriteHeader(recorder.Code)
		body := strings.Replace(recorder.Body.String(), "</body>", "<script type=\"module\">"+probeScript+"</script></body>", 1)
		w.Write([]byte(body))
	}
}

const probeScript = `
const results={ok:false,checks:[],timings:{},errors:[],fetchName:globalThis.fetch.name};
window.addEventListener('error',e=>results.errors.push(String(e.message).slice(0,200)));
window.addEventListener('unhandledrejection',e=>results.errors.push(String(e.reason).slice(0,200)));
const report=()=>{const c=WorkerBridge.Stream('probe-report');c.onopen=()=>c.send(new TextEncoder().encode(JSON.stringify(results)));};
try{
 const input=new Uint8Array(2*1024*1024+13);for(let i=0;i<input.length;i++)input[i]=i%251;
 const binarySamplesMs=[];
 for(let sample=0;sample<5;sample++){
  const started=performance.now();
  const response=await fetch('/__work/probe?action=echo',{method:'POST',body:input});
  const output=new Uint8Array(await response.arrayBuffer());
  if(output.length!==input.length||!output.every((v,i)=>v===input[i]))throw new Error('binary mismatch');
  binarySamplesMs.push(performance.now()-started);
 }
 const sortedBinarySamples=[...binarySamplesMs].sort((a,b)=>a-b);
 results.timings.binarySamplesMs=binarySamplesMs;
 results.timings.binaryRoundtripMs=sortedBinarySamples[Math.floor(sortedBinarySamples.length/2)];
 results.checks.push('2MiB binary upload/download (5 samples)');
 await new Promise((resolve,reject)=>{const ws=new WebSocket(location.href.replace(/^http/,'ws').replace(/[?].*$/,'')+'__work/probe-ws');ws.binaryType='arraybuffer';const timer=setTimeout(()=>{ws.close();reject(new Error('WebSocket timed out'));},10000);let binary=false;ws.onerror=()=>{clearTimeout(timer);reject(new Error('WebSocket failed'));};ws.onopen=()=>ws.send(input);ws.onmessage=e=>{if(!binary){const bytes=new Uint8Array(e.data);if(bytes.length!==input.length||!bytes.every((v,i)=>v===input[i])){clearTimeout(timer);reject(new Error('WebSocket binary mismatch'));return;}binary=true;ws.send('text ✓');}else{clearTimeout(timer);ws.close();if(e.data!=='text ✓')reject(new Error('WebSocket text mismatch'));else resolve();}};});results.checks.push('WebSocket binary/text');
 const forbidden=await fetch('/wails/runtime?object=0&method=0',{headers:{'x-wails-window-name':'settings','x-wails-window-id':'0'}});if(forbidden.status!==403)throw new Error('Worker reached privileged runtime');results.checks.push('privileged runtime denied with spoofed headers');
 await new Promise((resolve,reject)=>{const stream=WorkerBridge.Stream('worker-fetch');const timer=setTimeout(()=>reject(new Error('stale generation accepted')),2000);stream.onopen=()=>stream.send(new TextEncoder().encode(JSON.stringify({generation:'stale-generation',url:'/',method:'GET',headers:{},hasBody:false})));stream.onclose=()=>{clearTimeout(timer);resolve();};stream.onmessage=()=>{clearTimeout(timer);reject(new Error('stale document reached Worker'));};});results.checks.push('stale generation denied');
 const abort=new AbortController(),streamStart=performance.now();
 const streaming=await fetch('/__work/probe?action=stream',{signal:abort.signal});
 const reader=streaming.body.getReader(),first=await reader.read();
 if(new TextDecoder().decode(first.value)!=='first')throw new Error('first chunk missing');
 results.timings.firstChunkMs=performance.now()-streamStart;results.checks.push('response before EOF');
 await fetch('/__work/probe');results.checks.push('independent request during slow stream');
 abort.abort();await reader.cancel().catch(()=>{});
 let stats;for(let i=0;i<50;i++){stats=await(await fetch('/__work/probe')).json();if(stats.cancelled)break;await new Promise(r=>setTimeout(r,50));}
 if(!stats.cancelled)throw new Error('cancellation did not reach Worker');results.checks.push('cancel propagated');
 const session=await fetch('/__work/probe?action=session');if(!session.ok)throw new Error('session fixture failed');results.checks.push('real Session events');
 await new Promise(r=>setTimeout(r,1500));
 if(!globalThis.__DSH_BOOT__)throw new Error('DSH boot graph missing');
 if(!document.body.innerText.trim())throw new Error('DSH page empty');
 results.pageText=document.body.innerText.slice(0,300);results.checks.push('real DSH WebView');
 results.ok=true;
}catch(error){results.failure=String(error);results.stack=error?.stack;}
report();
`

func Configure(c *dshmanager.Config, dsh *dshadapter.Adapter, root string) {
	if os.Getenv("DSH_WORK_DESKTOP_REPORT") == "" {
		return
	}
	hint := dsh.RuntimeHint()
	c.StatePath = filepath.Join(root, "manager.json")
	c.Runtimes = []dshmanager.RuntimeInfo{{ID: "dsh-" + hint.Version, Version: hint.Version, Path: hint.Path, Source: dshmanager.RuntimeSourceDevelopmentFixture, Installed: true}}
	c.DefaultRunContext = dshmanager.RunContext{RuntimeID: "dsh-" + hint.Version, Node: dshmanager.NodeSelection{Kind: dshmanager.NodeSelectionSystem}, Profile: dshmanager.ProfileRef{DataDirectoryID: "dsh-work", Name: "web"}}
}
