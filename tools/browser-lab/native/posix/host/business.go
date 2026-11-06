package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// A real bounded filesystem root, not an arbitrary-file URL proxy.
func startArtifactServer(data string) (*http.Server, error) {
	root, err := os.OpenRoot(filepath.Join(data, "artifact"))
	if err != nil {
		return nil, err
	}
	files := http.FileServerFS(root.FS())
	server := &http.Server{Addr: "127.0.0.1:19484", ReadHeaderTimeout: 3 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		files.ServeHTTP(w, r)
	})}
	go func() {
		defer root.Close()
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Fprintln(os.Stderr, err)
		}
	}()
	return server, nil
}

// Wails-only resource delivery and gateway consent presentation. External CEF
// pages have neither the virtual Wails origin nor this token/asset transport.
func businessProxy(w http.ResponseWriter, r *http.Request, token string) bool {
	target := ""
	if strings.HasPrefix(r.URL.Path, "/gate/") {
		target = "http://127.0.0.1:19482/" + strings.TrimPrefix(r.URL.Path, "/gate/")
	}
	if strings.HasPrefix(r.URL.Path, "/recovery/") {
		target = "http://127.0.0.1:19483/" + strings.TrimPrefix(r.URL.Path, "/recovery/")
	}
	if r.URL.Path == "/business/display" {
		target = "http://127.0.0.1:19483/display"
	}
	if target == "" {
		return false
	}
	if r.Header.Get("X-Browser-Lab-Token") != token {
		http.Error(w, "unauthorized", 403)
		return true
	}
	request, err := http.NewRequestWithContext(r.Context(), r.Method, target, r.Body)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return true
	}
	request.Header.Set("X-Browser-Lab-Token", token)
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: 2 * time.Minute}).Do(request)
	if err != nil {
		http.Error(w, err.Error(), 502)
		return true
	}
	defer response.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(response.StatusCode)
	io.Copy(w, io.LimitReader(response.Body, 4<<20))
	return true
}

const businessControls = `<section id="grant-panel"><b>浏览器控制授权</b><output id="status"></output><output id="grants">等待授权服务</output><div id="requests"></div><button onclick="stopControl()">撤销控制</button></section>`
const businessJS = `
const panel=document.querySelector('aside');panel.innerHTML=document.querySelector('#grant-template').innerHTML;
async function gate(path,body){const r=await fetch('/gate/'+path,{method:body?'POST':'GET',headers:{'X-Browser-Lab-Token':token,'Content-Type':'application/json'},body:body?JSON.stringify(body):undefined});if(!r.ok)throw Error(await r.text());return r.json()}
async function stopControl(){try{await Promise.all(((await gate('state')).browserAccess?.sessions||[]).map(s=>gate('revoke',{session:s.session,lifetime:s.lifetime})))}catch(e){document.querySelector('#grants').textContent=e.message}}
window.stopControl=stopControl;
async function updateGrants(){try{const state=await gate('state');const access=state.browserAccess;document.querySelector('#grants').textContent=access?.sessions?.length?'控制会话已授权':'页面可见；控制需授权';const box=document.querySelector('#requests');box.replaceChildren();for(const request of access?.requests||[]){const row=document.createElement('div');row.dataset.consentId=request.id;const label=document.createElement('p');label.textContent=(request.kind==='browser'?'允许本次测试会话控制？':request.kind==='site'?'允许访问此站点？':'确认本次操作？')+' '+(request.site||request.title||'');row.append(label);for(const [text,choice]of [['允许','allow'],['拒绝','deny']]){const b=document.createElement('button');b.textContent=text;b.dataset.choice=choice;b.onclick=()=>gate('answer',{id:request.id,choice}).then(updateGrants).catch(e=>label.textContent=e.message);row.append(b)}box.append(row)}}catch(e){document.querySelector('#grants').textContent='等待授权服务'}}
setInterval(updateGrants,700);updateGrants();
`

const recoveryJS = `
async function recover(kind){const box=document.querySelector('#recovery-message');try{box.textContent='正在恢复';const s=await(await fetch('/lab/state',{headers:{'X-Browser-Lab-Token':token}})).json();const r=await fetch('/recovery/'+kind,{method:'POST',headers:{'X-Browser-Lab-Token':token,'Content-Type':'application/json'},body:JSON.stringify({tab:s.selected})});if(!r.ok)throw Error(await r.text());box.textContent='已恢复'}catch(e){box.textContent=e.message}}window.recover=recover;
let recoveryStateKey='';
function renderRecovery(s){
 const dead=s.ready&&s.tabs.find(t=>t.name===s.selected)?.fault;
 const key=JSON.stringify([s.viewGeneration,s.selected,s.guestRunning,s.starting,s.ready,!!dead]);
 if(key!==recoveryStateKey){document.querySelector('#recovery-message').textContent='';recoveryStateKey=key}
 document.querySelector('#fault-panel').hidden=s.ready&&!dead;
 document.querySelector('#fault-title').textContent=s.starting?'浏览器正在启动':!s.guestRunning?'浏览器已退出':'页面发生异常';
 document.querySelector('#recover-renderer').hidden=!dead;
 document.querySelector('#recover-broker').hidden=s.guestRunning;
 document.querySelector('#recover-broker').disabled=s.starting;
}
setInterval(async()=>{try{renderRecovery(await(await fetch('/lab/state',{headers:{'X-Browser-Lab-Token':token}})).json())}catch{}},150);
`

// Visual presentation follows native state. Native cover hides the child before
// any DOM modal can become visible; the screenshot is never a live target.
const overlayJS = `
let overlayEpoch=-1,placeholderURL='';
setInterval(async()=>{try{
 const s=await(await fetch('/lab/state',{headers:{'X-Browser-Lab-Token':token}})).json();
 const host=location.pathname==='/float'?s.float:s.main;
 const owner=s.tabs.find(t=>t.name===s.selected)?.parentHWND===host.hwnd;
 const cover=document.querySelector('#cover');if(location.pathname==='/float')cover.style.left='20px';
 cover.hidden=!(s.covered&&owner);
 if(s.covered&&owner&&overlayEpoch!==s.presentationEpoch){
  const r=await fetch('/overlay-placeholder',{headers:{'X-Browser-Lab-Token':token}});if(!r.ok)throw Error('snapshot unavailable');
  if(placeholderURL)URL.revokeObjectURL(placeholderURL);placeholderURL=URL.createObjectURL(await r.blob());const image=document.querySelector('#placeholder');const epoch=s.presentationEpoch;image.onload=()=>{image.dataset.epoch=String(epoch)};image.src=placeholderURL;overlayEpoch=epoch;
 }
}catch(e){console.error(e.message)}},150);
`
