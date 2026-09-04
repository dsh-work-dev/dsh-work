package workergateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	sessionCookie    = "work_worker_session"
	bootstrapPath    = "/__work/bootstrap"
	externalPath     = "/__work/external"
	maxBootstrapBody = 4 * 1024 * 1024
	maxExternalURL   = 4096
)

// Session is one per-generation trusted access path to a validated DSH
// endpoint. URL is a per-generation bootstrap URL; Origin is the stable URL
// safe for lifecycle projection.
type Session interface {
	URL() string
	Origin() string
	Close() error
}

// Adapter owns the local gateway boundary. It has no operating-system or
// browser-automation knowledge and can be shared by all native supervisors.
type Adapter struct {
	mu           sync.RWMutex
	openExternal func(string) error
}

func New() *Adapter { return &Adapter{} }

// SetOpenExternal installs the composition-root callback used by the
// navigation guard to delegate a validated external URL to the system browser.
func (a *Adapter) SetOpenExternal(fn func(string) error) {
	a.mu.Lock()
	a.openExternal = fn
	a.mu.Unlock()
}

func (a *Adapter) Start(ctx context.Context, upstreamAuthURL string) (Session, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	upstream, err := parseUpstreamAuthURL(upstreamAuthURL)
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen for dsh-work Worker gateway: %w", err)
	}

	port := listener.Addr().(*net.TCPAddr).Port
	origin := fmt.Sprintf("http://127.0.0.1:%d", port)
	sessionID, err := randomToken(32)
	if err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("create dsh-work Worker gateway session: %w", err)
	}
	proxy := &httputil.ReverseProxy{}
	proxy.Transport = &http.Transport{
		Proxy:                 nil,
		DialContext:           (&net.Dialer{Timeout: 2 * time.Second}).DialContext,
		ForceAttemptHTTP2:     false,
		MaxIdleConns:          16,
		MaxIdleConnsPerHost:   8,
		IdleConnTimeout:       30 * time.Second,
		ResponseHeaderTimeout: 2 * time.Second,
	}
	session := &gatewaySession{
		origin:          origin,
		bootstrapURL:    origin + bootstrapPath + "?session=" + url.QueryEscape(sessionID),
		sessionID:       sessionID,
		upstreamAuthURL: upstream,
		upstreamOrigin:  originOf(upstream),
		server:          &http.Server{ReadHeaderTimeout: 2 * time.Second},
		listener:        listener,
		proxy:           proxy,
		openExternal:    a.externalHandler,
		connections:     make(map[net.Conn]struct{}),
	}
	session.server.ConnState = session.trackConnection
	proxy.Rewrite = session.rewriteProxyRequest
	proxy.ModifyResponse = session.modifyProxyResponse
	proxy.ErrorHandler = session.proxyError
	session.server.Handler = http.HandlerFunc(session.serveHTTP)
	go func() {
		if err := session.server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			// The serving goroutine has no safe UI publication path. The Host
			// observes cleanup errors through the Session contract.
		}
	}()
	return session, nil
}

func (a *Adapter) externalHandler(value string) error {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.openExternal == nil {
		return nil
	}
	return a.openExternal(value)
}

type gatewaySession struct {
	mu              sync.Mutex
	origin          string
	bootstrapURL    string
	sessionID       string
	upstreamAuthURL *url.URL
	upstreamOrigin  string
	server          *http.Server
	listener        net.Listener
	proxy           *httputil.ReverseProxy
	openExternal    func(string) error
	connectionsMu   sync.Mutex
	connections     map[net.Conn]struct{}
	closeMu         sync.Mutex
	closed          bool
	closeErr        error
	exchangeOnce    sync.Once
	exchange        bootstrapExchange
}

type bootstrapExchange struct {
	cookies     []*http.Cookie
	status      int
	location    string
	body        []byte
	contentType string
	err         error
}

func (s *gatewaySession) URL() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bootstrapURL
}

func (s *gatewaySession) Origin() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.origin + "/"
}

func (s *gatewaySession) Close() error {
	s.closeMu.Lock()
	defer s.closeMu.Unlock()
	s.mu.Lock()
	if s.closed {
		err := s.closeErr
		s.mu.Unlock()
		return err
	}
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	err := s.server.Shutdown(ctx)
	cancel()
	if closeErr := s.listener.Close(); err == nil && closeErr != nil && !errors.Is(closeErr, net.ErrClosed) {
		err = closeErr
	}
	s.closeConnections()
	s.mu.Lock()
	if err == nil {
		s.closed = true
	}
	s.closeErr = err
	s.mu.Unlock()
	return err
}

func (s *gatewaySession) trackConnection(connection net.Conn, state http.ConnState) {
	s.connectionsMu.Lock()
	defer s.connectionsMu.Unlock()
	switch state {
	case http.StateNew, http.StateActive, http.StateIdle, http.StateHijacked:
		s.connections[connection] = struct{}{}
	case http.StateClosed:
		delete(s.connections, connection)
	}
}

func (s *gatewaySession) closeConnections() {
	s.connectionsMu.Lock()
	connections := make([]net.Conn, 0, len(s.connections))
	for connection := range s.connections {
		connections = append(connections, connection)
	}
	s.connectionsMu.Unlock()
	for _, connection := range connections {
		_ = connection.Close()
	}
}

func (s *gatewaySession) serveHTTP(writer http.ResponseWriter, request *http.Request) {
	if !s.validHost(request.Host) {
		http.Error(writer, "invalid gateway host", http.StatusBadRequest)
		return
	}
	switch request.URL.Path {
	case bootstrapPath:
		s.serveBootstrap(writer, request)
		return
	case externalPath:
		s.serveExternal(writer, request)
		return
	}
	if !s.validSession(request) {
		writer.Header().Set("Cache-Control", "no-store")
		http.Error(writer, "worker session required", http.StatusUnauthorized)
		return
	}
	if !sameOrigin(s.origin, request.Header.Get("Origin")) {
		http.Error(writer, "untrusted origin", http.StatusForbidden)
		return
	}
	if request.Method == http.MethodConnect || request.Method == http.MethodTrace {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if _, present := request.URL.Query()["token"]; present {
		http.Error(writer, "launch credentials are bootstrap-only", http.StatusForbidden)
		return
	}
	s.proxy.ServeHTTP(writer, request)
}

func (s *gatewaySession) serveBootstrap(writer http.ResponseWriter, request *http.Request) {
	query := request.URL.Query()
	if request.Method != http.MethodGet || len(query) != 1 || len(query["session"]) != 1 || query.Get("session") != s.sessionID {
		http.Error(writer, "invalid worker bootstrap", http.StatusNotFound)
		return
	}
	if !sameOrigin(s.origin, request.Header.Get("Origin")) {
		http.Error(writer, "untrusted origin", http.StatusForbidden)
		return
	}
	s.exchangeOnce.Do(func() { s.exchange = exchangeUpstream(s.upstreamAuthURL) })
	result := s.exchange
	if result.err != nil {
		http.Error(writer, "worker bootstrap failed", http.StatusBadGateway)
		return
	}
	setSessionCookie(writer, s.sessionID)
	for _, cookie := range result.cookies {
		copy := *cookie
		copy.Domain = ""
		http.SetCookie(writer, &copy)
	}
	writer.Header().Set("Cache-Control", "no-store")
	if result.status >= 300 && result.status < 400 {
		writer.Header().Set("Location", "/")
		writer.WriteHeader(http.StatusSeeOther)
		return
	}
	if result.contentType != "" {
		writer.Header().Set("Content-Type", result.contentType)
	}
	if strings.HasPrefix(strings.ToLower(result.contentType), "text/html") && len(result.body) <= maxBootstrapBody {
		result.body = injectNavigationGuard(result.body)
		writer.Header().Add("Content-Security-Policy", "navigate-to 'self'")
	}
	writer.WriteHeader(result.status)
	_, _ = writer.Write(result.body)
}

func (s *gatewaySession) serveExternal(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost || !s.validSession(request) || !sameOrigin(s.origin, request.Header.Get("Origin")) {
		http.Error(writer, "external navigation is not available", http.StatusForbidden)
		return
	}
	value := request.URL.Query().Get("url")
	if len(value) == 0 || len(value) > maxExternalURL {
		http.Error(writer, "invalid external URL", http.StatusBadRequest)
		return
	}
	target, err := url.Parse(value)
	if err != nil || (target.Scheme != "http" && target.Scheme != "https") || target.Host == "" || target.User != nil {
		http.Error(writer, "invalid external URL", http.StatusBadRequest)
		return
	}
	if s.openExternal != nil {
		if err := s.openExternal(target.String()); err != nil {
			http.Error(writer, "external browser handoff failed", http.StatusBadGateway)
			return
		}
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (s *gatewaySession) validHost(value string) bool {
	return value == strings.TrimPrefix(s.origin, "http://")
}

func (s *gatewaySession) validSession(request *http.Request) bool {
	cookie, err := request.Cookie(sessionCookie)
	return err == nil && cookie.Value == s.sessionID
}

func (s *gatewaySession) rewriteProxyRequest(request *httputil.ProxyRequest) {
	in := request.In
	upstream := mustParseURL(s.upstreamOrigin)
	request.SetURL(upstream)
	request.Out.URL.Path = in.URL.Path
	request.Out.URL.RawPath = in.URL.RawPath
	request.Out.URL.RawQuery = in.URL.RawQuery
	request.Out.Host = upstream.Host
	request.Out.Header.Del("Cookie")
	var upstreamCookies []string
	for _, cookie := range in.Cookies() {
		if cookie.Name != sessionCookie {
			upstreamCookies = append(upstreamCookies, cookie.Name+"="+cookie.Value)
		}
	}
	if len(upstreamCookies) > 0 {
		request.Out.Header.Set("Cookie", strings.Join(upstreamCookies, "; "))
	}
	request.Out.Header.Set("Accept-Encoding", "identity")
	request.Out.Header.Set("Origin", s.upstreamOrigin)
}

func (s *gatewaySession) modifyProxyResponse(response *http.Response) error {
	if location := response.Header.Get("Location"); location != "" {
		target, err := response.Request.URL.Parse(location)
		if err != nil || target.Scheme != "http" || target.Host != mustParseURL(s.upstreamOrigin).Host {
			return errors.New("DSH returned an external redirect")
		}
		response.Header.Set("Location", s.origin+target.RequestURI())
	}
	if values := response.Header.Values("Set-Cookie"); len(values) > 0 {
		response.Header.Del("Set-Cookie")
		for _, raw := range values {
			cookie, err := http.ParseSetCookie(raw)
			if err != nil {
				continue
			}
			cookie.Domain = ""
			if cookie.Path == "" {
				cookie.Path = "/"
			}
			response.Header.Add("Set-Cookie", cookie.String())
		}
	}
	contentType := strings.ToLower(response.Header.Get("Content-Type"))
	contentEncoding := strings.ToLower(strings.TrimSpace(response.Header.Get("Content-Encoding")))
	if strings.HasPrefix(contentType, "text/html") && (contentEncoding == "" || contentEncoding == "identity") && response.Body != nil && (response.ContentLength <= maxBootstrapBody || response.ContentLength < 0) {
		body, err := io.ReadAll(io.LimitReader(response.Body, maxBootstrapBody+1))
		_ = response.Body.Close()
		if err != nil {
			return err
		}
		if len(body) <= maxBootstrapBody {
			body = injectNavigationGuard(body)
			response.Header.Add("Content-Security-Policy", "navigate-to 'self'")
		}
		response.Body = io.NopCloser(strings.NewReader(string(body)))
		response.ContentLength = int64(len(body))
		response.Header.Set("Content-Length", fmt.Sprint(len(body)))
	}
	return nil
}

func (s *gatewaySession) proxyError(writer http.ResponseWriter, _ *http.Request, _ error) {
	http.Error(writer, "worker gateway upstream unavailable", http.StatusBadGateway)
}

func exchangeUpstream(upstream *url.URL) bootstrapExchange {
	client := &http.Client{
		Timeout: 2 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	request, err := http.NewRequest(http.MethodGet, upstream.String(), nil)
	if err != nil {
		return bootstrapExchange{err: err}
	}
	response, err := client.Do(request)
	if err != nil {
		return bootstrapExchange{err: err}
	}
	defer response.Body.Close()
	result := bootstrapExchange{
		status:      response.StatusCode,
		location:    response.Header.Get("Location"),
		cookies:     response.Cookies(),
		contentType: response.Header.Get("Content-Type"),
	}
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		if response.StatusCode != http.StatusSeeOther || result.location != "/" || len(result.cookies) == 0 {
			result.err = errors.New("DSH bootstrap returned an unexpected redirect")
		}
		return result
	}
	if response.StatusCode != http.StatusOK {
		result.err = fmt.Errorf("DSH bootstrap returned %s", response.Status)
		return result
	}
	result.body, result.err = io.ReadAll(io.LimitReader(response.Body, maxBootstrapBody+1))
	if len(result.body) > maxBootstrapBody {
		result.err = errors.New("DSH bootstrap response is too large")
	}
	return result
}

func parseUpstreamAuthURL(value string) (*url.URL, error) {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "http" || parsed.Hostname() != "127.0.0.1" || parsed.Port() == "" || parsed.User != nil || parsed.Fragment != "" || parsed.Path != "" && parsed.Path != "/" {
		return nil, errors.New("DSH gateway requires an HTTP loopback launch URL")
	}
	query := parsed.Query()
	if len(query) != 1 || len(query["token"]) != 1 || len(query.Get("token")) == 0 || len(query.Get("token")) > 512 {
		return nil, errors.New("DSH gateway requires one bounded launch token")
	}
	return parsed, nil
}

func originOf(value *url.URL) string {
	return value.Scheme + "://" + value.Host
}

func mustParseURL(value string) *url.URL {
	parsed, _ := url.Parse(value)
	return parsed
}

func sameOrigin(expected, supplied string) bool {
	return supplied == "" || supplied == expected
}

func setSessionCookie(writer http.ResponseWriter, value string) {
	http.SetCookie(writer, &http.Cookie{
		Name:     sessionCookie,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   3600,
	})
}

func randomToken(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func contextError(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func injectNavigationGuard(body []byte) []byte {
	const script = `<script>(function(){const o=location.origin;function x(u){try{const v=new URL(u,location.href);return (v.protocol==="http:"||v.protocol==="https:")&&v.origin!==o?v.href:""}catch(_){return""}}function g(u){if(!u)return;fetch("/__work/external?url="+encodeURIComponent(u),{method:"POST",headers:{"Origin":o},credentials:"same-origin"}).catch(function(){})}document.addEventListener("click",function(e){const a=e.target&&e.target.closest?e.target.closest("a"):null;if(!a)return;const u=x(a.href);if(u){e.preventDefault();g(u)}},true);const w=window.open;window.open=function(u){const v=x(u);if(v){g(v);return null}return w.apply(this,arguments)}})();</script>`
	lower := strings.ToLower(string(body))
	index := strings.Index(lower, "</head>")
	if index < 0 {
		return append(body, []byte(script)...)
	}
	result := make([]byte, 0, len(body)+len(script))
	result = append(result, body[:index]...)
	result = append(result, script...)
	result = append(result, body[index:]...)
	return result
}
