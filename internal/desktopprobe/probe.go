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
		if err = os.WriteFile(path, []byte(probePlugin), 0600); err != nil {
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
import {dirname,join} from 'node:path';
import {fileURLToPath} from 'node:url';
const probeCwd=join(dirname(fileURLToPath(import.meta.url)),'..','probe-workspace');
export const inject=['webServer','sessions','sessionController','sessionQuery'];
export function apply(ctx){
let cancelled=0,backgroundTicks=0,backgroundTimer,backgroundChild;
ctx.effect(()=>ctx.webServer.register({kind:'exact',path:'/__work/probe',handler:async(req,res)=>{
const action=new URL(req.url,'http://local').searchParams.get('action');
if(action==='background'){if(!backgroundTimer){backgroundTimer=setInterval(()=>backgroundTicks++,100);backgroundChild=spawn(process.execPath,['-e','setInterval(()=>{},1000)'],{windowsHide:true,stdio:'ignore'});}res.end('started');return;}
if(action==='background-status'){res.setHeader('content-type','application/json');res.end(JSON.stringify({ticks:backgroundTicks,pid:process.pid,child:backgroundChild?.pid}));return;}
if(action==='echo'){res.writeHead(200,{'content-type':'application/octet-stream'});for await(const chunk of req){if(!res.write(chunk))await once(res,'drain');}res.end();return;}
if(action==='stream'){res.writeHead(200,{'content-type':'application/octet-stream'});res.write('first');const requestedDelay=Number(new URL(req.url,'http://local').searchParams.get('delayMs'));const delay=Number.isFinite(requestedDelay)&&requestedDelay>=0&&requestedDelay<=30000?requestedDelay:30000;const timer=setTimeout(()=>res.end('last'),delay);res.once('close',()=>{clearTimeout(timer);cancelled++;});return;}
if(action==='session'){let existed=false;try{await ctx.sessionQuery.readSession('pc-ipc-probe');existed=true;}catch(error){if(error?.code!=='SESSION_QUERY_SESSION_NOT_FOUND')throw error;}await ctx.sessionController.create({sessionId:'pc-ipc-probe',cwd:probeCwd});const s=ctx.sessions.get('pc-ipc-probe');if(!s)throw new Error('probe Session was not created');if(!existed){s.append('turn/start',{turn:1});s.append('turn/end',{turn:1,reason:{kind:'completed'}});}res.end(existed?'reused':'created');return;}
res.setHeader('content-type','application/json');res.end(JSON.stringify({cancelled}));
}}));
}`

func Assets(bridge *desktopbridge.Bridge) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			bridge.Assets(w, r)
			return
		}
		directOrigin := "null"
		if value := strings.TrimRight(strings.TrimSpace(os.Getenv("DSH_WORK_DIRECT_TCP_ORIGIN")), "/"); value != "" {
			if parsed, err := url.Parse(value); err == nil && parsed.Scheme == "http" && parsed.Host != "" && parsed.Path == "" && parsed.RawQuery == "" && parsed.Fragment == "" {
				if encoded, err := json.Marshal(value); err == nil {
					directOrigin = string(encoded)
				}
			}
		}
		recorder := httptest.NewRecorder()
		bridge.Assets(recorder, r)
		for k, v := range recorder.Header() {
			w.Header()[k] = v
		}
		w.WriteHeader(recorder.Code)
		script := strings.Replace(probeScript, "__DSH_WORK_DIRECT_TCP_ORIGIN__", directOrigin, 1)
		body := strings.Replace(recorder.Body.String(), "</body>", "<script type=\"module\">"+script+"</script></body>", 1)
		w.Write([]byte(body))
	}
}

const probeScript = `
const results={ok:false,checks:[],timings:{},errors:[],fetchName:globalThis.fetch.name};
const directTcpOrigin=__DSH_WORK_DIRECT_TCP_ORIGIN__;
const standardHTTP=true;
results.transportMode='wails-http-proxy+route-streams';
window.addEventListener('error',e=>results.errors.push(String(e.message).slice(0,200)));
window.addEventListener('unhandledrejection',e=>results.errors.push(String(e.reason).slice(0,200)));
const report=async()=>{const {Stream}=await import('/wails/runtime.js');const c=Stream('probe-report');c.onopen=()=>c.send(new TextEncoder().encode(JSON.stringify(results)));};
try{
 const input=new Uint8Array(2*1024*1024+13);for(let i=0;i<input.length;i++)input[i]=i%251;
 const binarySamplesMs=[];
 for(let sample=0;sample<5;sample++){
  const started=performance.now();
  const response=await WorkerBridge.workerFetch('/__work/probe?action=echo',{method:'POST',body:input});
  const output=new Uint8Array(await response.arrayBuffer());
  if(output.length!==input.length||!output.every((v,i)=>v===input[i]))throw new Error('binary mismatch');
  binarySamplesMs.push(performance.now()-started);
 }
 const sortedBinarySamples=[...binarySamplesMs].sort((a,b)=>a-b);
 results.timings.binarySamplesMs=binarySamplesMs;
 results.timings.binaryRoundtripMs=sortedBinarySamples[Math.floor(sortedBinarySamples.length/2)];
 results.checks.push('2MiB binary upload/download (5 samples)');
 if(directTcpOrigin){
  const directTcpPingSamplesMs=[];
  for(let sample=0;sample<5;sample++){
   const started=performance.now();
   const response=await fetch(directTcpOrigin+'/__direct/ping');
   const output=await response.text();
   if(!response.ok||output!=='ok')throw new Error('direct TCP ping mismatch');
   directTcpPingSamplesMs.push(performance.now()-started);
  }
  const sortedDirectTcpPingSamples=[...directTcpPingSamplesMs].sort((a,b)=>a-b);
  results.timings.directTcpPingSamplesMs=directTcpPingSamplesMs;
  results.timings.directTcpPingMs=sortedDirectTcpPingSamples[Math.floor(sortedDirectTcpPingSamples.length/2)];
  results.checks.push('native WebView direct TCP loopback ping (5 samples)');
 }
 if(globalThis.fetch.name!=='fetch')throw new Error('Fetch was replaced');results.checks.push('native Fetch unchanged; Gateway WebSocket check follows');
 const forbidden=await fetch('/wails/runtime?object=0&method=0',{headers:{'x-wails-window-name':'settings','x-wails-window-id':'0'}});if(forbidden.status!==403)throw new Error('Worker reached privileged runtime');results.checks.push('privileged runtime denied with spoofed headers');
 if(window.parent!==window){results.framed={origin:location.origin,embedder:location.ancestorOrigins?.[0]??'',previousGeneration:localStorage.getItem('dsh-work.probe.generation')??''};localStorage.setItem('dsh-work.probe.generation',globalThis.__WORK_GENERATION__??'');let reached=false;try{void window.parent.document.body;reached=true;}catch{}if(reached)throw new Error('framed Worker reached the shell document');results.checks.push('framed Worker isolated from shell document');}
 const stale=await fetch('/?generation=stale-generation');if(stale.status!==410)throw new Error('stale generation reached Worker');results.checks.push('stale generation denied');
 if(standardHTTP){
  const streamStart=performance.now();
  const streaming=await fetch('/__work/probe?action=stream&delayMs=1000');
  const reader=streaming.body.getReader(),first=await reader.read();
  const firstText=new TextDecoder().decode(first.value);
  results.timings.firstChunkMs=performance.now()-streamStart;
  results.timings.standardHTTPStreamBuffered=firstText!=='first';
  if(firstText!=='firstlast')throw new Error('standard HTTP stream body mismatch: '+firstText);
  await reader.cancel().catch(()=>{});
  results.checks.push('standard Wails HTTP response buffered until EOF');
  results.checks.push('stream cancellation deferred with standard Wails HTTP');
 }else{
  const abort=new AbortController(),streamStart=performance.now();
  const streaming=await fetch('/__work/probe?action=stream',{signal:abort.signal});
  const reader=streaming.body.getReader(),first=await reader.read();
  if(new TextDecoder().decode(first.value)!=='first')throw new Error('first chunk missing');
  results.timings.firstChunkMs=performance.now()-streamStart;results.checks.push('response before EOF');
  await fetch('/__work/probe');results.checks.push('independent request during slow stream');
  abort.abort();await reader.cancel().catch(()=>{});
  let stats;for(let i=0;i<50;i++){stats=await(await fetch('/__work/probe')).json();if(stats.cancelled)break;await new Promise(r=>setTimeout(r,50));}
  if(!stats.cancelled)throw new Error('cancellation did not reach Worker');results.checks.push('cancel propagated');
 }
 const session=await fetch('/__work/probe?action=session');if(!session.ok)throw new Error('session fixture failed ('+session.status+'): '+await session.text());const sessionResult=await session.text();if(sessionResult==='reused')results.checks.push('persisted Session reused across Worker generation');else results.checks.push('real Session events');
 const bootDeadline=performance.now()+30000;
 while(document.querySelector('[data-dsh-boot]')){
  const boot=document.querySelector('[data-dsh-boot]');
  if(boot.textContent.includes('Failed to load plugins'))throw new Error(boot.textContent);
  if(performance.now()>bootDeadline)throw new Error('DSH plugin boot did not complete');
  await new Promise(r=>setTimeout(r,50));
 }
 if(!globalThis.__DSH_BOOT__)throw new Error('DSH boot graph missing');
 if(!document.getElementById('root')?.innerText.trim())throw new Error('DSH application not mounted'); const uploadWorkerUrl=URL.createObjectURL(new Blob([''],{type:'text/javascript'}));
 const uploadWorker=new Worker(uploadWorkerUrl,{name:'dsh-file-upload'});
 URL.revokeObjectURL(uploadWorkerUrl);
 let uploadProgress;
 const uploaded=await new Promise((resolve,reject)=>{
  const timer=setTimeout(()=>{uploadWorker.terminate();reject(new Error('DSH upload adapter timed out'));},30000);
  uploadWorker.onmessage=event=>{
   const frame=event.data;
   if(frame.kind==='progress'){uploadProgress=frame;return;}
   clearTimeout(timer);uploadWorker.terminate();
   if(frame.kind==='complete')resolve(frame);else reject(new Error(frame.message||'DSH upload adapter failed'));
  };
  uploadWorker.postMessage({url:new URL('/api/session/uploadFileBinary?sessionId=pc-ipc-probe&name=E-32-probe.bin',location.href).href,headers:{'content-type':'application/octet-stream'},body:new Blob([input])});
 });
 const uploadedBody=JSON.parse(uploaded.body);
 if(uploaded.status!==200||uploadedBody.ok!==true||uploadProgress?.loaded!==input.length||uploadProgress?.total!==input.length)throw new Error('2MiB DSH upload or progress mismatch');
 results.checks.push('2MiB session upload through DSH XHR Worker adapter');
 {
  await new Promise((resolve,reject)=>{
   const ws=new WebSocket(new URL('/api/remote.mux',location.href).href.replace(/^http/,'ws'));
   const timer=setTimeout(()=>{ws.close();reject(new Error('DSH Remote event readiness timed out'));},10000);
   ws.onopen=()=>ws.send(JSON.stringify({type:'open',streamId:'desktop-readiness',endpoint:'$events',payload:{args:{}}}));
   ws.onerror=()=>{clearTimeout(timer);reject(new Error('DSH Remote event connection failed'));};
   ws.onmessage=e=>{try{const frame=JSON.parse(e.data);if(frame.type!=='item'||frame.streamId!=='desktop-readiness'||frame.value?.type!=='ready'||!frame.value.clientId)throw new Error('DSH Remote stream did not acknowledge readiness');clearTimeout(timer);ws.close();resolve();}catch(error){clearTimeout(timer);ws.close();reject(error);}};
  });
  results.checks.push('upstream Remote event ready handshake');
 }
 if(/reconnecting/i.test(document.getElementById('root')?.innerText??''))throw new Error('DSH application is reconnecting');
 if(results.errors.length)throw new Error('DSH WebView errors: '+results.errors.join('; '));
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
	if selected := os.Getenv("DSH_WORK_DESKTOP_RUNTIME"); selected != "" {
		hint.Path, hint.Version = selected, os.Getenv("DSH_WORK_DESKTOP_VERSION")
	}
	c.StatePath = filepath.Join(root, "manager.json")
	c.Runtimes = []dshmanager.RuntimeInfo{{ID: "dsh-" + hint.Version, Version: hint.Version, Path: hint.Path, Source: dshmanager.RuntimeSourceDevelopmentFixture, Installed: true}}
	c.DefaultRunContext = dshmanager.RunContext{RuntimeID: "dsh-" + hint.Version, Node: dshmanager.NodeSelection{Kind: dshmanager.NodeSelectionSystem}, Profile: dshmanager.ProfileRef{DataDirectoryID: "dsh-work", Name: "web"}}
}
