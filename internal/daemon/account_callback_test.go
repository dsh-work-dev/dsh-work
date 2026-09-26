package daemon

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/local/dsh-work/internal/accountcallback"
	"github.com/local/dsh-work/internal/workerchannel"
)

func TestDaemonCallbackOriginSurvivesUIClientRestart(t *testing.T) {
	forwardedGeneration := make(chan string, 1)
	callback, err := accountcallback.NewAccountCallbackServer(func(w http.ResponseWriter, _ *http.Request, generation string) {
		forwardedGeneration <- generation
		w.WriteHeader(http.StatusNoContent)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer callback.Close()

	server := &Server{AccountCallback: callback}
	const input = `{"type":"client-request","rpcId":"r1","method":"account/startSignIn","payload":{"args":{"locale":"zh-CN","callbackOrigin":"http://wails.localhost","loginSource":"desktop"}}}`
	for _, generation := range []string{"worker-before-ui-restart", "worker-after-ui-restart"} {
		request := httptest.NewRequest(http.MethodPost, "/api/account/startSignIn", strings.NewReader(input))
		if err := server.prepareAccountSignInRequest(request, "/api/account/startSignIn", generation); err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		var envelope struct {
			Payload struct {
				Args struct {
					CallbackOrigin string `json:"callbackOrigin"`
				} `json:"args"`
			} `json:"payload"`
		}
		if err := json.Unmarshal(body, &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.Payload.Args.CallbackOrigin != callback.Origin() {
			t.Fatalf("request for %q used callback %q, want daemon callback %q", generation, envelope.Payload.Args.CallbackOrigin, callback.Origin())
		}
	}

	response, err := http.Get(callback.Origin() + "/oauth/callback?code=code&state=state")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("callback status = %d, want success page", response.StatusCode)
	}
	select {
	case generation := <-forwardedGeneration:
		if generation != "worker-after-ui-restart" {
			t.Fatalf("callback forwarded to %q, want current worker", generation)
		}
	default:
		t.Fatal("callback did not reach the current DSH worker")
	}
}

func TestDaemonRejectsSignInWhenCallbackListenerIsUnavailable(t *testing.T) {
	server := &Server{}
	request := httptest.NewRequest(http.MethodPost, "/api/account/startSignIn", strings.NewReader(`{}`))
	if err := server.prepareAccountSignInRequest(request, "/api/account/startSignIn", "generation"); err != errAccountCallbackUnavailable {
		t.Fatalf("prepareAccountSignInRequest error = %v, want callback unavailable", err)
	}
}

func TestAccountCallbackWorkerRequestPreservesOAuthQuery(t *testing.T) {
	original := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:45678/oauth/callback?code=code&state=state", nil)
	forwarded := accountCallbackWorkerRequest(original, "current-generation")
	if forwarded.URL.Path != "/worker" || forwarded.URL.RawQuery != original.URL.RawQuery {
		t.Fatalf("forwarded callback URL = %q, want daemon worker path with original query", forwarded.URL)
	}
	if forwarded.Header.Get("X-DSH-Path") != "/oauth/callback" || forwarded.Header.Get("X-DSH-Generation") != "current-generation" {
		t.Fatalf("forwarded callback headers = %v", forwarded.Header)
	}
	if forwarded.Header.Get("Origin") != "" {
		t.Fatalf("browser origin escaped the callback listener: %q", forwarded.Header.Get("Origin"))
	}
}

// statusOnlyResponse mirrors the callback listener's writer: it records the
// status and cannot be upgraded to a full-duplex HTTP/1 response.
type statusOnlyResponse struct {
	header http.Header
	status int
}

func (r *statusOnlyResponse) Header() http.Header         { return r.header }
func (r *statusOnlyResponse) WriteHeader(status int)      { r.status = status }
func (r *statusOnlyResponse) Write(b []byte) (int, error) { return len(b), nil }

func TestAccountCallbackReachesWorkerRoutingWithoutFullDuplexWriter(t *testing.T) {
	server := &Server{Channel: &workerchannel.Adapter{}}
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:45678/oauth/callback?code=code&state=state", nil)
	response := &statusOnlyResponse{header: make(http.Header)}
	server.ServeAccountCallback(response, request, "stale-generation")
	if response.status != http.StatusGone {
		t.Fatalf("callback status = %d, want Worker routing to report the stale generation (410)", response.status)
	}
}
