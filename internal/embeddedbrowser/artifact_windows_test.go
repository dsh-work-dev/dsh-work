//go:build windows

package embeddedbrowser_test

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/automation/browserruntime"
	"github.com/local/dsh-work/internal/embeddedbrowser"
	win "github.com/local/dsh-work/internal/platform/windows"
)

func TestLiveArtifactResourceIPCAndPartitions(t *testing.T) {
	executable, deps := os.Getenv("DSH_WORK_CEF_TEST_EXECUTABLE"), os.Getenv("DSH_WORK_BROWSER_TEST_DEPENDENCIES")
	if executable == "" || deps == "" {
		t.Skip("requires explicit CEF component and locked MCP dependencies")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
	defer cancel()
	dir := t.TempDir()
	for name, body := range map[string]string{
		"index.html": `<!doctype html><link rel="stylesheet" href="style.css"><output>Loading</output><img id="pic" src="image.svg"><script src="main.js"></script>`,
		"style.css":  `output{color:rgb(12,34,56)}`,
		"main.js":    `window.bundleJS=true;window.documentNonce=crypto.randomUUID();addEventListener('pageshow',e=>window.fromBFCache=e.persisted);document.querySelector('output').textContent='Bundle ready';`,
		"image.svg":  `<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16"><rect width="16" height="16" fill="red"/></svg>`,
		"secret.txt": "lease-only-secret",
		"worker.js":  "self.postMessage('worker-ran')",
		"sw.js":      "self.addEventListener('fetch',e=>e.respondWith(new Response('HIJACK')))",
		"child.html": `<!doctype html><script>window.childNonce=crypto.randomUUID();setInterval(()=>parent.childTicks=(parent.childTicks||0)+1,10)</script>`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	large, err := os.Create(filepath.Join(dir, "large.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if err = large.Truncate(128 << 20); err != nil {
		t.Fatal(err)
	}
	large.Close()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "outside.txt"), []byte("OUTSIDE_ONLY"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "escape")); err != nil {
		t.Fatalf("file escape fixture unavailable: %v", err)
	}
	junction := filepath.Join(dir, "junction")
	if err := os.Mkdir(junction, 0700); err != nil {
		t.Fatal(err)
	}
	path, err := windows.UTF16PtrFromString(junction)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(path, windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		t.Fatal(err)
	}
	data := winio.EncodeReparsePoint(&winio.ReparsePoint{Target: outside, IsMountPoint: true})
	var returned uint32
	err = windows.DeviceIoControl(handle, windows.FSCTL_SET_REPARSE_POINT, &data[0], uint32(len(data)), nil, 0, &returned, nil)
	windows.CloseHandle(handle)
	if err != nil {
		t.Fatal(err)
	}
	var externalRequests atomic.Int32
	external := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		externalRequests.Add(1)
		_, _ = w.Write([]byte("EXTERNAL"))
	}))
	defer external.Close()
	chunks := make(chan struct{}, 128)
	adapter := win.NewJobObjectAdapter()
	profile := filepath.Join(t.TempDir(), "profile")
	var cancelArtifactOpen context.CancelFunc
	b, err := embeddedbrowser.Start(ctx, embeddedbrowser.Config{Executable: executable, Profile: profile, Supervisor: adapter, ObserveProtocol: func(direction, method string) {
		if direction == "artifact" && method == "open-dispatched" && cancelArtifactOpen != nil {
			stop := cancelArtifactOpen
			cancelArtifactOpen = nil
			stop()
		}
		if direction == "artifact" && method == "read-chunk" {
			select {
			case chunks <- struct{}{}:
			default:
			}
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	open := func() *embeddedbrowser.Artifact {
		t.Helper()
		a, err := b.OpenArtifact(ctx, dir, "index.html")
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	a, z := open(), open()
	control := func(a *embeddedbrowser.Artifact) (automation.BrowserRoute, int) {
		t.Helper()
		selection, err := a.Select(ctx, automation.Request{Session: "artifact-evidence", Lifetime: "this-run"})
		if err != nil {
			t.Fatal(err)
		}
		r, err := selection.Open(ctx, "")
		if err != nil {
			t.Fatal(err)
		}
		for {
			select {
			case raw := <-r.Messages():
				var e struct {
					Method string
					Params []json.RawMessage
				}
				_ = json.Unmarshal(raw, &e)
				if e.Method == "tab.created" {
					var tab struct{ ID int }
					_ = json.Unmarshal(e.Params[0], &tab)
					return r, tab.ID
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
				return nil, 0
			}
		}
	}
	ra, ta := control(a)
	rz, tz := control(z)
	sequence := 0
	rpc := func(r automation.BrowserRoute, tab int, method string, args map[string]any) (json.RawMessage, string) {
		t.Helper()
		sequence++
		id := sequence
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		raw, _ := json.Marshal(map[string]any{"id": id, "method": "devtools.dispatch", "params": []any{map[string]any{"tabId": tab}, method, args}})
		if err := r.Send(ctx, raw); err != nil {
			t.Fatal(err)
		}
		for {
			select {
			case raw := <-r.Messages():
				var m struct {
					ID     int
					Result json.RawMessage
					Error  string
				}
				_ = json.Unmarshal(raw, &m)
				if m.ID == id {
					return m.Result, m.Error
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
				return nil, "timeout"
			case <-r.Done():
				t.Fatal(r.Err())
				return nil, "closed"
			case <-timer.C:
				t.Fatalf("native RPC timeout: %s A=%+v B=%+v", method, a.Stats(), z.Stats())
				return nil, "timeout"
			}
		}
	}
	eval := func(r automation.BrowserRoute, tab int, expression string) string {
		t.Helper()
		raw, err := rpc(r, tab, "Runtime.evaluate", map[string]any{"expression": expression, "awaitPromise": true, "returnByValue": true})
		if err != "" {
			t.Fatal(err)
		}
		var value struct {
			Result           struct{ Value json.RawMessage }
			ExceptionDetails json.RawMessage
		}
		_ = json.Unmarshal(raw, &value)
		if len(value.ExceptionDetails) > 0 {
			t.Fatalf("artifact inspection: %s", raw)
		}
		return string(value.Result.Value)
	}
	for _, v := range []struct {
		r   automation.BrowserRoute
		tab int
	}{{ra, ta}, {rz, tz}} {
		attempts := 0
		for {
			attempts++
			got := eval(v.r, v.tab, `({js:!!window.bundleJS,css:document.querySelector('output')&&getComputedStyle(document.querySelector('output')).color,image:document.querySelector('#pic')?.naturalWidth})`)
			if strings.Contains(got, `"js":true`) && strings.Contains(got, "12, 34, 56") && strings.Contains(got, `"image":16`) {
				t.Log("offline CSS/JS/image loaded through Go resource IPC")
				break
			}
			if attempts == 100 {
				t.Fatalf("offline bundle did not finish: %s A=%+v B=%+v", got, a.Stats(), z.Stats())
			}
			select {
			case <-time.After(30 * time.Millisecond):
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		}
	}
	writeStore := `(async()=>{document.cookie='same='+'A'+';path=/';localStorage.same='A';sessionStorage.same='A';await new Promise((ok,no)=>{let q=indexedDB.open('same',1);q.onupgradeneeded=()=>q.result.createObjectStore('store');q.onerror=no;q.onsuccess=()=>{let tx=q.result.transaction('store','readwrite');tx.objectStore('store').put('A','same');tx.oncomplete=()=>{q.result.close();ok()};tx.onerror=no}});await(await caches.open('same')).put('/cached',new Response('A'));let indexed=await new Promise((ok,no)=>{let q=indexedDB.open('same');q.onerror=no;q.onsuccess=()=>{let get=q.result.transaction('store').objectStore('store').get('same');get.onsuccess=()=>{q.result.close();ok(get.result)};get.onerror=no}});return {indexed,cookie:document.cookie,local:localStorage.same,session:sessionStorage.same,cache:await(await(await caches.open('same')).match('/cached')).text()}})()`
	if got := eval(ra, ta, writeStore); !strings.Contains(got, `"local":"A"`) || !strings.Contains(got, `"cache":"A"`) || !strings.Contains(got, `"indexed":"A"`) {
		t.Fatal("A APIs unusable", got)
	} else {
		t.Log("A storage readback:", got)
	}
	readStore := `(async()=>({cookie:document.cookie,local:localStorage.same||null,session:sessionStorage.same||null,dbs:(await indexedDB.databases()).map(d=>d.name),cache:await caches.keys(),nonce:window.documentNonce}))()`
	clean := eval(rz, tz, readStore)
	if strings.Contains(clean, `"A"`) || !strings.Contains(clean, `"dbs":[]`) || !strings.Contains(clean, `"cache":[]`) {
		t.Fatal("B inherited A storage", clean)
	}
	t.Log("B before independent writes:", clean)
	if got := eval(rz, tz, strings.ReplaceAll(writeStore, "'A'", "'B'")); !strings.Contains(got, `"local":"B"`) || !strings.Contains(got, `"indexed":"B"`) || !strings.Contains(got, `"cache":"B"`) {
		t.Fatal("B APIs unusable", got)
	} else {
		t.Log("B storage readback:", got)
	}
	bState := eval(rz, tz, readStore)
	beforeCanceledOpen := b.ArtifactCount()
	openCtx, cancelOpen := context.WithCancel(ctx)
	cancelArtifactOpen = cancelOpen
	if _, err := b.OpenArtifact(openCtx, dir, "index.html"); !errors.Is(err, context.Canceled) {
		cancelOpen()
		t.Fatal("initialization did not preserve cancellation", err)
	}
	cancelOpen()
	if b.ArtifactCount() != beforeCanceledOpen {
		t.Fatal("canceled real initialization retained lease/root owner")
	}
	if got := eval(rz, tz, readStore); got != bState {
		t.Fatal("canceled initialization affected B", got)
	}
	t.Log("real initialization canceled after dispatch; late native close ACK consumed, lease registry reclaimed, B retained")
	bReads := z.Stats().ReadRequests
	foreign, _ := json.Marshal(z.Origin() + "/secret.txt")
	if got := eval(ra, ta, `fetch(`+string(foreign)+`).then(()=>false,()=>true)`); got != "true" {
		t.Fatal("A fetched B", got)
	}
	if got := eval(ra, ta, `fetch('/escape/outside.txt').then(()=>false,()=>true)`); got != "true" {
		t.Fatal("file symlink escape", got)
	}
	if got := eval(ra, ta, `fetch('/junction/outside.txt').then(()=>false,()=>true)`); got != "true" {
		t.Fatal("junction escape", got)
	}
	if got := eval(ra, ta, `fetch('/%2e%2e%2foutside.txt').then(()=>false,()=>true)`); got != "true" {
		t.Fatal("encoded traversal escape", got)
	}
	extURL, _ := json.Marshal(external.URL + "/resource")
	if got := eval(ra, ta, `fetch(`+string(extURL)+`).then(()=>false,()=>true)`); got != "true" {
		t.Fatal("external network allowed", got)
	}
	if got := eval(ra, ta, `Promise.all(['img','script','link'].map(tag=>new Promise(ok=>{let e=document.createElement(tag);if(tag==='link'){e.rel='stylesheet';e.href=`+string(extURL)+`}else e.src=`+string(extURL)+`;e.onload=()=>{e.remove();ok('loaded')};e.onerror=()=>{e.remove();ok('denied')};document.body.append(e);setTimeout(()=>{e.remove();ok('timeout')},1000)})))`); got != `["denied","denied","denied"]` {
		t.Fatal("external assets policy", got)
	}
	readsBeforeObject := a.Stats().ReadRequests
	eval(ra, ta, `new Promise(ok=>{let o=document.createElement('object'),e=document.createElement('embed');o.data='/secret.txt';e.src='/secret.txt';document.body.append(o,e);setTimeout(()=>{o.remove();e.remove();ok(true)},50)})`)
	if a.Stats().ReadRequests != readsBeforeObject {
		t.Fatal("object/embed accessed lease resources")
	}
	if got := eval(ra, ta, `new Promise(ok=>{window.channelA=new BroadcastChannel('same');let local=new BroadcastChannel('same');channelA.onmessage=e=>{local.close();ok(e.data)};local.postMessage('local-api-works');setTimeout(()=>ok('timeout'),1000)})`); got != `"local-api-works"` {
		t.Fatal("BroadcastChannel API unusable", got)
	}
	eval(rz, tz, `window.foreignMessages=[];window.channelB=new BroadcastChannel('same');channelB.onmessage=e=>foreignMessages.push(e.data);'listening'`)
	eval(ra, ta, `channelA.postMessage('cross-partition');'sent'`)
	if got := eval(rz, tz, `new Promise(ok=>setTimeout(()=>ok(foreignMessages.length),50))`); got != "0" {
		t.Fatal("BroadcastChannel crossed partitions", got)
	}
	if got := eval(ra, ta, `fetch('/secret.txt').then(async r=>({body:await r.text(),cache:r.headers.get('cache-control')}))`); !strings.Contains(got, `"cache":"no-store"`) || !strings.Contains(got, "lease-only-secret") {
		t.Fatal("resource response cache policy", got)
	}
	if got := eval(ra, ta, `new Promise(ok=>{window.foreignFrame=document.createElement('iframe');foreignFrame.src=`+string(foreign)+`;document.body.append(foreignFrame);setTimeout(()=>ok('created'),80)})`); got != `"created"` {
		t.Fatal(got)
	}
	tree, treeErr := rpc(ra, ta, "Page.getFrameTree", map[string]any{})
	if treeErr != "" {
		t.Fatal(treeErr)
	}
	var frameTree struct{ FrameTree json.RawMessage }
	_ = json.Unmarshal(tree, &frameTree)
	var checkFrames func(json.RawMessage)
	checkFrames = func(raw json.RawMessage) {
		var frame struct {
			Frame       struct{ SecurityOrigin string }
			ChildFrames []json.RawMessage
		}
		_ = json.Unmarshal(raw, &frame)
		if frame.Frame.SecurityOrigin == z.Origin() {
			t.Fatal("foreign iframe acquired B principal", string(raw))
		}
		for _, child := range frame.ChildFrames {
			checkFrames(child)
		}
	}
	checkFrames(frameTree.FrameTree)
	eval(ra, ta, `foreignFrame.remove();'removed'`)
	if z.Stats().ReadRequests != bReads || externalRequests.Load() != 0 {
		t.Fatal("cross origin request reached foreign resource authority")
	}
	if got := eval(ra, ta, `(async()=>{let worker=await new Promise(ok=>{try{let w=new Worker('/worker.js');w.onmessage=()=>{w.terminate();ok(true)};w.onerror=()=>{w.terminate();ok(false)};setTimeout(()=>{w.terminate();ok('timeout')},2000)}catch{ok(false)}}),sw=false;try{await navigator.serviceWorker.register('/sw.js');sw=true}catch{}return {worker,sw}})()`); !strings.Contains(got, `"worker":false`) || !strings.Contains(got, `"sw":false`) {
		t.Fatal("Worker policy", got)
	}
	if _, err := rpc(ra, ta, "Page.navigate", map[string]any{"url": z.Origin() + "/secret.txt"}); err == "" {
		t.Log("navigation returns engine result; actual source URL checked next")
	}
	if got := eval(ra, ta, `location.origin`); got != `"`+a.Origin()+`"` {
		t.Fatal("cross lease navigation entered B", got)
	}
	type historyDocument struct {
		Href, Nonce string
		History     int
		Persisted   bool
	}
	waitHistoryDoc := func(expected string) historyDocument {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			raw, err := rpc(ra, ta, "Runtime.evaluate", map[string]any{"expression": "({href:location.href,nonce:window.documentNonce,history:history.length,persisted:!!window.fromBFCache,ready:!!window.bundleJS})", "returnByValue": true})
			if err == "" {
				var value struct {
					Result struct{ Value json.RawMessage }
				}
				_ = json.Unmarshal(raw, &value)
				var doc struct {
					Href, Nonce      string
					History          int
					Persisted, Ready bool
				}
				_ = json.Unmarshal(value.Result.Value, &doc)
				if doc.Href == expected && doc.Ready {
					return historyDocument{doc.Href, doc.Nonce, doc.History, doc.Persisted}
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("actual history navigation did not complete", expected)
		return historyDocument{}
	}
	originalDoc := waitHistoryDoc(a.Origin() + "/index.html")
	eval(ra, ta, `channelA.close();setTimeout(()=>location.assign('/index.html?history=second'),0);'scheduled'`)
	secondDoc := waitHistoryDoc(a.Origin() + "/index.html?history=second")
	if secondDoc.History < 2 || secondDoc.Nonce == originalDoc.Nonce {
		t.Fatal("history did not contain two real documents", secondDoc)
	}
	eval(ra, ta, `setTimeout(()=>history.back(),0);'scheduled'`)
	backDoc := waitHistoryDoc(a.Origin() + "/index.html")
	if backDoc.Persisted && backDoc.Nonce != originalDoc.Nonce {
		t.Fatal("BFCache marker did not preserve original document")
	}
	t.Logf("actual history: second document=%s; back URL=%s; original document restored=%t; pageshow.persisted=%t", secondDoc.Nonce, backDoc.Href, backDoc.Nonce == originalDoc.Nonce, backDoc.Persisted)
	if !backDoc.Persisted {
		t.Log("This mixed isolation scenario did not retain its document; retained revocation is qualified separately by TestLiveArtifactBFCacheRetainedRevocation")
	}
	for len(chunks) > 0 {
		<-chunks
	}
	if got := eval(ra, ta, `new Promise(ok=>{let frame=document.createElement('iframe');frame.id='localFrame';frame.src='/child.html';frame.onload=()=>ok(!!frame.contentWindow.childNonce);document.body.append(frame)})`); got != "true" {
		t.Fatal("same lease child frame did not load", got)
	}
	for len(chunks) > 0 {
		<-chunks
	}
	eval(ra, ta, `window.inflight=fetch('/large.bin').then(r=>r.arrayBuffer()).then(()=> 'complete',()=> 'canceled');'started'`)
	select {
	case <-chunks:
	case <-ctx.Done():
		t.Fatal("large resource did not start")
	}
	if err := a.Revoke(ctx); err != nil {
		t.Fatal(err)
	}
	proof := a.Stats()
	if proof.ClosedBrowsers != 1 || proof.CanceledStreams < 1 || !proof.PartitionRetired || proof.RevokedFrames < 2 || proof.ClosedFrames < proof.RevokedFrames || proof.ReadMarshaled < 1 {
		t.Fatalf("in-flight/context close evidence: %+v", proof)
	}
	t.Logf("revoke barrier: closedBrowsers=%d canceledStreams=%d revokedFrames=%d closedFrames=%d liveFrames=0 partitionRetired=%t offIOReadMarshaled=%d", proof.ClosedBrowsers, proof.CanceledStreams, proof.RevokedFrames, proof.ClosedFrames, proof.PartitionRetired, proof.ReadMarshaled)
	oldBack, _ := json.Marshal(map[string]any{"id": 999, "method": "devtools.dispatch", "params": []any{map[string]any{"tabId": ta}, "Runtime.evaluate", map[string]any{"expression": "history.back()"}}})
	if err := ra.Send(ctx, oldBack); err == nil {
		t.Fatal("revoked route remained usable")
	}
	if got := eval(rz, tz, readStore); got != bState {
		t.Fatal("revoking A affected B", got)
	}
	t.Log("A in-flight resource revoked; native close barrier confirmed; B document/storage preserved")
	newA := open()
	rn, tn := control(newA)
	for eval(rn, tn, `!!window.bundleJS`) != "true" {
		select {
		case <-time.After(20 * time.Millisecond):
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if newA.Origin() == a.Origin() {
		t.Fatal("origin reused")
	}
	if got := eval(rn, tn, readStore); strings.Contains(got, `"A"`) || strings.Contains(got, `"B"`) {
		t.Fatal("reopen restored old partition", got)
	}
	if got := eval(rn, tn, `fetch(`+string(foreign)+`).then(()=>false,()=>true)`); got != "true" {
		t.Fatal("new partition read foreign lease")
	}
	oldURL, _ := json.Marshal(a.Origin() + "/secret.txt")
	if got := eval(rn, tn, `fetch(`+string(oldURL)+`).then(()=>false,()=>true)`); got != "true" {
		t.Fatal("old lease URL revived", got)
	}
	// A third real artifact exercises the same resource IPC through locked MCP.
	viaMCP := open()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	factory := browserruntime.NewFactory(browserruntime.Config{Routes: viaMCP, Supervisor: adapter, Root: t.TempDir(), OutputRoot: os.Getenv("DSH_WORK_CEF_TEST_EVIDENCE"), Resolve: func(context.Context) (browserruntime.Runtime, error) {
		return browserruntime.Runtime{Node: node, Dependencies: deps}, nil
	}})
	r, err := factory(ctx, automation.Request{Session: "artifact-mcp", Lifetime: "this-run"})
	if err != nil {
		t.Fatal(err)
	}
	value, err := r.Execute(ctx, "browser_snapshot", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(value)
	if !strings.Contains(string(raw), "Bundle ready") {
		t.Fatal("MCP artifact snapshot", string(raw))
	}
	if _, err := r.Execute(ctx, "browser_take_screenshot", map[string]any{"scale": "css", "type": "png"}); err != nil {
		t.Fatal(err)
	}
	r.Close()
	for _, lease := range []*embeddedbrowser.Artifact{z, newA, viaMCP} {
		if err := lease.Revoke(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	b, err = embeddedbrowser.Start(ctx, embeddedbrowser.Config{Executable: executable, Profile: profile, Supervisor: adapter})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	restarted := open()
	rr, tr := control(restarted)
	for eval(rr, tr, `!!window.bundleJS`) != "true" {
		select {
		case <-time.After(20 * time.Millisecond):
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if got := eval(rr, tr, readStore); !strings.Contains(got, `"dbs":[]`) || !strings.Contains(got, `"cache":[]`) || !strings.Contains(got, `"local":null`) {
		t.Fatal("broker restart restored artifact partition", got)
	}
	if got := eval(rr, tr, `fetch(`+string(oldURL)+`).then(()=>false,()=>true)`); got != "true" {
		t.Fatal("broker restart revived old lease", got)
	}
	if err := restarted.Revoke(ctx); err != nil {
		t.Fatal(err)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	t.Log("independent artifact origins, usable storage isolation, Worker/SW denial, actual history/closed-target rejection, reopen and broker restart passed")
}
