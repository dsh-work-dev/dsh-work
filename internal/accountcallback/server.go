package accountcallback

import (
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const DesktopReturnScheme = "dsh-work"

// AccountCallbackServer exposes only the official DSH OAuth loopback callback.
// The page's HTTP and Remote transports remain on their existing Worker bridge.
type AccountCallbackServer struct {
	listener   net.Listener
	server     *http.Server
	origin     string
	forward    func(http.ResponseWriter, *http.Request, string)
	mu         sync.RWMutex
	generation string
}

func NewAccountCallbackServer(forward func(http.ResponseWriter, *http.Request, string)) (*AccountCallbackServer, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s := &AccountCallbackServer{listener: listener, origin: "http://" + listener.Addr().String(), forward: forward}
	s.server = &http.Server{
		Handler:           http.HandlerFunc(s.serveHTTP),
		ReadHeaderTimeout: 3 * time.Second,
		IdleTimeout:       5 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	go func() { _ = s.server.Serve(listener) }()
	return s, nil
}

func (s *AccountCallbackServer) Origin() string {
	if s == nil {
		return ""
	}
	return s.origin
}

func (s *AccountCallbackServer) SetGeneration(generation string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.generation = generation
	s.mu.Unlock()
}

func (s *AccountCallbackServer) Close() error {
	if s == nil || s.server == nil {
		return nil
	}
	return s.server.Close()
}

func (s *AccountCallbackServer) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || r.URL == nil || len(r.URL.RawQuery) > 8192 || r.ContentLength != 0 {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if r.Host != strings.TrimPrefix(s.origin, "http://") || !loopbackRemote(r.RemoteAddr) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if r.URL.Path == "/" && r.URL.RawQuery == "" {
		writeAccountCallbackPage(w, http.StatusOK, accountCallbackWaitingPage)
		return
	}
	if r.URL.Path != "/" && r.URL.Path != "/oauth/callback" {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || query.Get("state") == "" || (query.Get("code") == "" && query.Get("error") == "") {
		writeAccountCallbackPage(w, http.StatusBadRequest, accountCallbackErrorPage)
		return
	}
	s.mu.RLock()
	generation := s.generation
	s.mu.RUnlock()
	if generation == "" || s.forward == nil {
		writeAccountCallbackPage(w, http.StatusGone, accountCallbackExpiredPage)
		return
	}
	result := &accountCallbackResponse{header: make(http.Header)}
	callbackRequest := r
	if r.URL.Path == "/" {
		callbackRequest = r.Clone(r.Context())
		callbackRequest.URL.Path = "/oauth/callback"
	}
	s.forward(result, callbackRequest, generation)
	if result.status == 0 {
		result.status = http.StatusOK
	}
	// DSH completes a successful browser sign-in by redirecting to its platform
	// completion page. That redirect is an accepted terminal callback response.
	if query.Get("error") == "" && result.status >= http.StatusOK && result.status < 400 {
		writeAccountCallbackPage(w, http.StatusOK, accountCallbackSuccessPage)
		return
	}
	pageStatus := result.status
	if query.Get("error") != "" && pageStatus >= http.StatusOK && pageStatus < http.StatusMultipleChoices {
		pageStatus = http.StatusBadRequest
	} else if pageStatus < http.StatusBadRequest || pageStatus > 599 {
		pageStatus = http.StatusBadGateway
	}
	writeAccountCallbackPage(w, pageStatus, accountCallbackErrorPage)
}

// Account OAuth responses are short RPC acknowledgements. Capture only their
// status so the callback listener can replace an empty/raw backend response
// with a safe page; never buffer an upstream body that may contain secrets.
type accountCallbackResponse struct {
	header http.Header
	status int
}

func (r *accountCallbackResponse) Header() http.Header { return r.header }

func (r *accountCallbackResponse) WriteHeader(status int) { r.status = status }

func (r *accountCallbackResponse) Write(body []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return len(body), nil
}

func writeAccountCallbackPage(w http.ResponseWriter, status int, page string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; base-uri 'none'; form-action 'none'")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, accountCallbackPageStart+page+accountCallbackPageEnd)
}

const accountCallbackPageStart = `<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="referrer" content="no-referrer"><title>dsh-work 登录</title>
<style>
:root{color-scheme:light dark;font-family:system-ui,-apple-system,"Segoe UI",sans-serif;color:#17191f;background:#f4f5f7}
*{box-sizing:border-box}body{min-height:100vh;margin:0;display:grid;place-items:center;padding:24px}
main{width:min(100%,440px);padding:32px;border:1px solid #dfe1e6;border-radius:20px;background:#fff;box-shadow:0 12px 40px #18202b12}
h1{margin:0 0 12px;font-size:22px;line-height:1.4}p{margin:0 0 12px;color:#565b66;line-height:1.65}
.en{font-size:14px}.actions{margin-top:24px}a{display:inline-flex;align-items:center;justify-content:center;min-height:44px;padding:0 18px;border-radius:12px;background:#111318;color:#fff;text-decoration:none;font-weight:600}
a:focus-visible{outline:3px solid #6b8afd;outline-offset:3px}@media(prefers-color-scheme:dark){:root{color:#f1f2f5;background:#17191f}main{background:#22252d;border-color:#3b3f49}p{color:#b5bac5}a{background:#f1f2f5;color:#17191f}}
</style></head><body><main>`

const accountCallbackPageEnd = `</main><script>history.replaceState(null,"",location.pathname)</script></body></html>`

const accountCallbackReturnURL = DesktopReturnScheme + "://open"

const accountCallbackWaitingPage = `<h1>等待登录回调</h1><p>请从 dsh-work 发起登录。完成账号授权后，这个页面会提示你返回应用。</p><p class="en">Start sign-in in dsh-work, then complete authorization in your browser.</p><div class="actions"><a href="` + accountCallbackReturnURL + `" rel="noreferrer">打开 dsh-work</a></div>`

const accountCallbackSuccessPage = `<h1>登录流程已完成</h1><p>授权结果已返回 dsh-work。点击下方按钮回到应用查看账号状态。</p><p class="en">Authorization returned to dsh-work. Open the app to continue.</p><div class="actions"><a href="` + accountCallbackReturnURL + `" rel="noreferrer">返回 dsh-work</a></div>`

const accountCallbackExpiredPage = `<h1>登录会话已过期</h1><p>请回到 dsh-work 重新发起登录。</p><p class="en">This sign-in session has expired. Start a new sign-in in dsh-work.</p><div class="actions"><a href="` + accountCallbackReturnURL + `" rel="noreferrer">打开 dsh-work</a></div>`

const accountCallbackErrorPage = `<h1>登录尚未完成</h1><p>请回到 dsh-work 查看状态，或重新发起登录。</p><p class="en">Sign-in did not finish. Return to dsh-work to check the status or try again.</p><div class="actions"><a href="` + accountCallbackReturnURL + `" rel="noreferrer">返回 dsh-work</a></div>`

func loopbackRemote(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
