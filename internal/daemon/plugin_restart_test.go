package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/local/dsh-work/internal/lifecycle"
	"github.com/local/dsh-work/internal/workeripc"
)

type pluginRestartSession struct {
	generation string
	client     *http.Client
}

type pluginRestartTestTransport struct {
	endpoint *url.URL
}

func (t pluginRestartTestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	forwarded := request.Clone(request.Context())
	endpoint := *request.URL
	endpoint.Scheme = t.endpoint.Scheme
	endpoint.Host = t.endpoint.Host
	forwarded.URL = &endpoint
	return http.DefaultTransport.RoundTrip(forwarded)
}

func pluginRestartTestClient(t *testing.T, worker *httptest.Server) *http.Client {
	t.Helper()
	endpoint, err := url.Parse(worker.URL)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Transport: pluginRestartTestTransport{endpoint: endpoint}}
}

func (s pluginRestartSession) URL() string          { return "" }
func (s pluginRestartSession) Generation() string   { return s.generation }
func (s pluginRestartSession) Patch() string        { return "" }
func (s pluginRestartSession) Client() *http.Client { return s.client }
func (s pluginRestartSession) Activate(string)      {}
func (s pluginRestartSession) Close() error         { return nil }

func TestPluginRestartUsesHostLifecycleForCurrentWorker(t *testing.T) {
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/dsh-market/status" || r.Method != http.MethodGet || r.Header.Get("Origin") != workeripc.Origin {
			t.Fatalf("status request = %s %s origin=%q", r.Method, r.URL.Path, r.Header.Get("Origin"))
		}
		_, _ = w.Write([]byte(`{"active":false,"busy":false,"boot":"boot-1","restart":true,"debugger":null}`))
	}))
	defer worker.Close()

	called := ""
	restart := func(_ context.Context, generation string) (lifecycle.Status, error) {
		called = generation
		return lifecycle.Status{State: lifecycle.StateStarting, GenerationID: "next-generation"}, nil
	}
	request := httptest.NewRequest(http.MethodPost, "/worker", strings.NewReader(`{}`))
	request.Header.Set("Origin", Origin)
	response := httptest.NewRecorder()
	servePluginRestart(response, request, pluginRestartSession{"current-generation", pluginRestartTestClient(t, worker)}, "/dsh-market/restart", restart)

	if response.Code != http.StatusAccepted || called != "current-generation" || !strings.Contains(response.Body.String(), `"boot":"boot-1"`) {
		t.Fatalf("restart response = %d %q, called with %q", response.Code, response.Body.String(), called)
	}
}

func TestPluginRestartV1PreservesTheMarketEnvelope(t *testing.T) {
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"active":false,"busy":false,"boot":"boot-2","restart":true,"debugger":null}`))
	}))
	defer worker.Close()
	request := httptest.NewRequest(http.MethodPost, "/worker", strings.NewReader(`{}`))
	request.Header.Set("Origin", Origin)
	response := httptest.NewRecorder()
	servePluginRestart(response, request, pluginRestartSession{"generation", pluginRestartTestClient(t, worker)}, "/dsh-market/api/v1/restart", func(context.Context, string) (lifecycle.Status, error) {
		return lifecycle.Status{State: lifecycle.StateStarting, GenerationID: "next"}, nil
	})
	var result struct {
		Schema string `json:"schema"`
		Result struct {
			OK   bool   `json:"ok"`
			Boot string `json:"boot"`
		} `json:"result"`
	}
	if response.Code != http.StatusAccepted || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.Schema != "dsh-market/update-api/v1" || !result.Result.OK || result.Result.Boot != "boot-2" {
		t.Fatalf("versioned restart response = %d %q", response.Code, response.Body.String())
	}
}

func TestPluginRestartRejectsForeignOriginAndBusyOperations(t *testing.T) {
	checks := []struct {
		name, origin, status string
		want                 int
	}{
		{name: "foreign origin", origin: "https://evil.example", status: `{"active":false,"busy":false,"boot":"boot","restart":true,"debugger":null}`, want: http.StatusForbidden},
		{name: "operation active", origin: Origin, status: `{"active":true,"busy":false,"boot":"boot","restart":true,"debugger":null}`, want: http.StatusConflict},
		{name: "install busy", origin: Origin, status: `{"active":false,"busy":true,"boot":"boot","restart":true,"debugger":null}`, want: http.StatusConflict},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(check.status)) }))
			defer worker.Close()
			called := false
			request := httptest.NewRequest(http.MethodPost, "/worker", strings.NewReader(`{}`))
			request.Header.Set("Origin", check.origin)
			response := httptest.NewRecorder()
			servePluginRestart(response, request, pluginRestartSession{"generation", pluginRestartTestClient(t, worker)}, "/dsh-market/restart", func(context.Context, string) (lifecycle.Status, error) {
				called = true
				return lifecycle.Status{}, nil
			})
			if response.Code != check.want || called {
				t.Fatalf("restart response = %d %q, restart called=%v", response.Code, response.Body.String(), called)
			}
		})
	}
}
