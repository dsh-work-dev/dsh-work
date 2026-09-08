package workergateway

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestGatewayBootstrapsDSHSessionAndProxiesTrustedOrigin(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("token") == "dsh-secret" {
			http.SetCookie(writer, &http.Cookie{Name: "dsh_session", Value: "session-value", Path: "/"})
			writer.Header().Set("Location", "/")
			writer.WriteHeader(http.StatusSeeOther)
			return
		}
		if _, err := request.Cookie("dsh_session"); err != nil {
			http.Error(writer, "missing DSH session", http.StatusUnauthorized)
			return
		}
		if request.Header.Get("Origin") != "http://"+request.Host {
			http.Error(writer, "unexpected upstream origin", http.StatusForbidden)
			return
		}
		writer.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(writer, "<html><head></head><body><a href=\"https://example.com\">outside</a></body></html>")
	}))
	defer upstream.Close()

	adapter := New()
	external := make(chan string, 1)
	adapter.SetOpenExternal(func(value string) error {
		external <- value
		return nil
	})
	session, err := adapter.Start(context.Background(), upstream.URL+"/?token=dsh-secret")
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar, Timeout: time.Second}
	response, err := client.Get(session.URL())
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), externalPath) {
		t.Fatalf("unexpected bootstrapped response: status=%s body=%q", response.Status, body)
	}
	if policy := response.Header.Get("Content-Security-Policy"); policy != "navigate-to 'self'" {
		t.Fatalf("unexpected navigation policy %q", policy)
	}

	origin := strings.TrimSuffix(session.Origin(), "/")
	untrusted, err := http.NewRequest(http.MethodGet, origin+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	untrusted.Header.Set("Origin", "http://untrusted.example")
	untrustedResponse, err := client.Do(untrusted)
	if err != nil {
		t.Fatal(err)
	}
	_ = untrustedResponse.Body.Close()
	if untrustedResponse.StatusCode != http.StatusForbidden {
		t.Fatalf("untrusted origin status = %s, want 403", untrustedResponse.Status)
	}

	missingOriginProxy, err := http.NewRequest(http.MethodPost, origin+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	missingOriginProxyResponse, err := client.Do(missingOriginProxy)
	if err != nil {
		t.Fatal(err)
	}
	_ = missingOriginProxyResponse.Body.Close()
	if missingOriginProxyResponse.StatusCode != http.StatusForbidden {
		t.Fatalf("missing-origin proxy status = %s, want 403", missingOriginProxyResponse.Status)
	}

	missingOriginExternal, err := http.NewRequest(http.MethodPost, origin+externalPath+"?url="+url.QueryEscape("https://example.com"), nil)
	if err != nil {
		t.Fatal(err)
	}
	missingOriginExternalResponse, err := client.Do(missingOriginExternal)
	if err != nil {
		t.Fatal(err)
	}
	_ = missingOriginExternalResponse.Body.Close()
	if missingOriginExternalResponse.StatusCode != http.StatusForbidden {
		t.Fatalf("missing-origin external handoff status = %s, want 403", missingOriginExternalResponse.Status)
	}
	select {
	case value := <-external:
		t.Fatalf("missing-origin request reached external callback with %q", value)
	default:
	}

	externalRequest, err := http.NewRequest(http.MethodPost, origin+externalPath+"?url="+url.QueryEscape("https://example.com"), nil)
	if err != nil {
		t.Fatal(err)
	}
	externalRequest.Header.Set("Origin", origin)
	externalResponse, err := client.Do(externalRequest)
	if err != nil {
		t.Fatal(err)
	}
	_ = externalResponse.Body.Close()
	if externalResponse.StatusCode != http.StatusNoContent {
		t.Fatalf("external handoff status = %s, want 204", externalResponse.Status)
	}
	select {
	case value := <-external:
		if value != "https://example.com" {
			t.Fatalf("external URL = %q", value)
		}
	case <-time.After(time.Second):
		t.Fatal("external handoff callback was not called")
	}
}

func TestGatewayWaitsForDirectoryPicker(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/directoryPicker/pick" {
			select {
			case <-time.After(2500 * time.Millisecond):
			case <-r.Context().Done():
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"path":"C:/workspace"}`)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	session, err := New().Start(context.Background(), upstream.URL+"/?token=picker-test")
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar, Timeout: 5 * time.Second}
	response, err := client.Get(session.URL())
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	request, err := http.NewRequest(http.MethodPost, session.Origin()+"api/directoryPicker/pick", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Origin", strings.TrimSuffix(session.Origin(), "/"))
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || string(body) != `{"path":"C:/workspace"}` {
		t.Fatalf("directory picker: status=%d body=%q", response.StatusCode, body)
	}
}

func TestGatewayRejectsInvalidUpstreamLaunchURL(t *testing.T) {
	for _, value := range []string{
		"http://localhost:4321/?token=value",
		"http://127.0.0.1:4321/",
		"http://127.0.0.1:4321/?token=one&other=two",
		"https://127.0.0.1:4321/?token=value",
	} {
		if _, err := New().Start(context.Background(), value); err == nil {
			t.Fatalf("Start(%q) unexpectedly succeeded", value)
		}
	}
}

func TestGatewayReportsUnavailableExternalHandoff(t *testing.T) {
	if err := New().externalHandler("https://example.com"); err == nil {
		t.Fatal("externalHandler() error = nil, want unavailable error")
	}
}

func TestGatewayCloseConnectionsClosesHijackedTransport(t *testing.T) {
	session := &gatewaySession{connections: make(map[net.Conn]struct{})}
	local, peer := net.Pipe()
	defer peer.Close()
	session.trackConnection(local, http.StateHijacked)
	session.closeConnections()

	buffer := make([]byte, 1)
	if _, err := peer.Read(buffer); err == nil {
		t.Fatal("hijacked connection remained open after gateway cleanup")
	}
}
