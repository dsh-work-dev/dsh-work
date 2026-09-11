package dshactivity

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/workeripc"
)

// Opt-in integration against the installed, pinned DSH web Worker. No model/API
// is contacted: a fixture appends real Session domain events inside that Worker.
func TestLiveDSHWorkerActivity(t *testing.T) {
	if os.Getenv("DSH_WORK_LIVE_TEST") != "1" {
		t.Skip("set DSH_WORK_LIVE_TEST=1 for installed DSH integration")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	cli := filepath.Join(root, "tools/dsh/node_modules/@deepseek-ai/dsh/lib/bin.js")
	home := t.TempDir()
	b := New(filepath.Join(home, "bridge"))
	patch, err := b.Prepare("live-test")
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	fixture := filepath.Join(home, "fixture.mjs")
	source := `export const inject=['sessions','webServer'];
export function apply(ctx,config){
let pending, resolve, answered;
ctx.effect(()=>ctx.webServer.register({kind:'exact',path:'/__dshwork/test',handler:(req,res)=>{
if(req.headers['x-dsh-work-token']!==config.token){res.writeHead(403);res.end();return;}
let session=ctx.sessions.get('pet-live-fixture');if(!session)session=ctx.sessions.create('pet-live-fixture',{meta:{cwd:config.cwd}});
const action=new URL(req.url,'http://localhost').searchParams.get('action');
try {if(action==='phase-thinking') {session.append('turn/start',{turn:2});session.append('step/start',{turn:2,step:1});}
else if(action==='phase-tools') {session.append('tool/call',{turn:2,step:1,callId:'a',name:'read_file',arguments:'{}'});session.append('tool/call',{turn:2,step:1,callId:'b',name:'apply_patch',arguments:'{}'});}
else if(action==='phase-one-result') {session.append('tool/result',{turn:2,step:1,message:{id:'result-a',role:'user',source:{kind:'tool',callId:'a'},content:[{type:'tool-result',toolCallId:'a',content:[],isError:true}]},error:{code:'RECOVERABLE',message:'retry'}},{surfaceOp:'append'});}
else if(action==='phase-last-result') {session.append('tool/result',{turn:2,step:1,message:{id:'result-b',role:'user',source:{kind:'tool',callId:'b'},content:[{type:'tool-result',toolCallId:'b',content:[]}]}},{surfaceOp:'append'});}
else if(action==='answer') {resolve?.({answers:[]});pending?.abort();}
else if(action==='question'||action==='approval'||action==='plan-review') {
pending=new AbortController();const request={agent:{id:session.id},signal:pending.signal,toolName:'fixture',reason:'Allow fixture?',questions:[{id:'q',question:'Choose a format',...(action==='plan-review'?{intent:{kind:'plan-review'}}:{})}]};
void ctx.waterfall(action==='approval'?'approval/request':'user-questions/request',request,()=>new Promise(r=>{resolve=r;})).then(value=>{answered=value;});
} else {session.append('turn/start',{turn:1});session.append('turn/end',{turn:1,reason:{kind:action||'completed'}});}
res.writeHead(200);res.end(JSON.stringify({answered}));}catch(error){res.writeHead(500);res.end(String(error));}}}));}`
	if err = os.WriteFile(fixture, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(patch)
	var patches []map[string]any
	_ = json.Unmarshal(data, &patches)
	patches = append(patches, map[string]any{"insert": []any{map[string]any{"id": "pet-live-fixture", "name": moduleURL(fixture), "config": map[string]string{"token": b.token, "cwd": home}}}})
	data, _ = json.Marshal(patches)
	if err = os.WriteFile(patch, data, 0600); err != nil {
		t.Fatal(err)
	}
	port := 1
	carrier, err := workeripc.New()
	if err != nil {
		t.Fatal(err)
	}
	defer carrier.Close()
	patch, err = carrier.Prepare(filepath.Join(home, "carrier"), filepath.Join(home, "dsh-home", "profiles", "web"), patch)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", cli, "--profile", "web", "--patch", patch, "--host", "127.0.0.1", "--port", strconv.Itoa(port), "--no-open")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "DSH_HOME="+filepath.Join(home, "dsh-home"))
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = cmd.Stdout
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	defer func() { cancel(); <-done }()
	urls := make(chan string, 1)
	logs := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(output)
		scanner.Buffer(make([]byte, 4096), 1<<20)
		var lines []string
		pattern := regexp.MustCompile(`http://127\.0\.0\.1:[0-9]+/\?token=[A-Za-z0-9_-]+`)
		for scanner.Scan() {
			line := scanner.Text()
			if u := pattern.FindString(line); u != "" {
				select {
				case urls <- u:
				default:
				}
			}
			if len(lines) < 40 {
				lines = append(lines, pattern.ReplaceAllString(line, "[Worker URL]"))
			}
		}
		logs <- strings.Join(lines, "\n")
	}()
	var authURL string
	select {
	case authURL = <-urls:
	case <-done:
		t.Fatalf("DSH failed: %s", <-logs)
	case <-ctx.Done():
		cancel()
		<-done
		t.Fatalf("DSH startup timed out: %s", <-logs)
	}
	b.Start(ctx, authURL, carrier.HTTP)
	deadline := time.Now().Add(12 * time.Second)
	for !b.Snapshot().Connected && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if !b.Snapshot().Connected {
		t.Fatal("DSH plugin did not serve authenticated activity")
	}
	// The dual-face module must be in the real browser boot graph, not only loaded on Host.
	index, err := b.client.Get(b.origin + "/")
	if err != nil {
		t.Fatal(err)
	}
	indexBody, _ := io.ReadAll(index.Body)
	index.Body.Close()
	if !strings.Contains(string(indexBody), "@dsh-work/pet-activity") {
		t.Fatal("pet client missing from DSH boot graph")
	}
	b.mu.Lock()
	client, origin, token := b.client, b.origin, b.token
	b.mu.Unlock()
	req, _ := http.NewRequestWithContext(ctx, "GET", origin+"/__dshwork/test", nil)
	req.Header.Set("X-DSH-Work-Token", token)
	req.Header.Set("Origin", origin)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("fixture status %d", resp.StatusCode)
	}
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s := b.Snapshot()
		if len(s.Sessions) > 0 && s.Sessions[0].State == "ready" {
			t.Log("Actual DSH Session turn/start → turn/end → authenticated Host activity: ready/unread")
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if len(b.Snapshot().Sessions) == 0 || b.Snapshot().Sessions[0].State != "ready" {
		t.Fatal("completion did not reach pet")
	}
	request := func(action string) {
		req, _ := http.NewRequestWithContext(ctx, "GET", origin+"/__dshwork/test?action="+action, nil)
		req.Header.Set("X-DSH-Work-Token", token)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		responseBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("fixture %s: %d %s", action, resp.StatusCode, responseBody)
		}
	}
	waitFor := func(check func(Activity) bool) {
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			s := b.Snapshot()
			if len(s.Sessions) > 0 && check(s.Sessions[0]) {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("expected activity not received: %+v", b.Snapshot())
	}
	request("phase-thinking")
	waitFor(func(a Activity) bool { return a.WorkPhase == "thinking" && a.Running })
	request("phase-tools")
	waitFor(func(a Activity) bool { return a.WorkPhase == "working" && a.ToolActivity == "editing" })
	request("phase-one-result")
	waitFor(func(a Activity) bool {
		return a.WorkPhase == "working" && a.ToolActivity == "editing" && a.Outcome == ""
	})
	request("phase-last-result")
	waitFor(func(a Activity) bool { return a.WorkPhase == "result" && a.Running })
	for _, kind := range []string{"question", "approval", "plan-review"} {
		request(kind)
		waitFor(func(a Activity) bool {
			return a.Interaction != nil && a.Interaction.Kind == kind && a.State == "needs-input"
		})
		request("answer")
		waitFor(func(a Activity) bool { return a.Interaction == nil })
	}
	for _, outcome := range []string{"aborted", "blocked", "error", "max-tokens", "interrupted"} {
		request(outcome)
		waitFor(func(a Activity) bool { return a.Outcome == outcome && a.State != "ready" })
	}
	request("completed")
	waitFor(func(a Activity) bool { return a.State == "ready" })
	if err := b.Open(ctx, "pet-live-fixture"); err != nil {
		t.Fatal(err)
	}
	clientSync := func(input any) map[string]any {
		body, _ := json.Marshal(input)
		req, _ := http.NewRequestWithContext(ctx, "POST", origin+"/__dshwork/activity-client", bytes.NewReader(body))
		req.Header.Set("Origin", origin)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("browser sync: %d", resp.StatusCode)
		}
		var data map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
			t.Fatal(err)
		}
		return data
	}
	navigation, ok := clientSync(map[string]any{})["navigation"].(map[string]any)
	if !ok || navigation["sessionId"] != "pet-live-fixture" {
		t.Fatal("navigation not delivered to DSH client")
	}
	clientSync(map[string]any{"ack": navigation["id"], "current": "pet-live-fixture", "visible": true})
	waitFor(func(a Activity) bool { return !a.Unread && a.State == "idle" })
	// Raw webServer routes must explicitly retain DSH's cookie trust fence.
	anonymous, _ := http.NewRequestWithContext(ctx, "POST", origin+"/__dshwork/activity-client", strings.NewReader(`{}`))
	anonymous.Header.Set("Origin", origin)
	anonymousClient := &http.Client{Transport: carrier.HTTP}
	unauthorized, err := anonymousClient.Do(anonymous)
	if err != nil {
		t.Fatal(err)
	}
	unauthorized.Body.Close()
	if unauthorized.StatusCode != 401 {
		t.Fatalf("anonymous client route: %d", unauthorized.StatusCode)
	}
}
