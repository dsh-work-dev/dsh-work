package accountcallback

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestWithAccountCallbackOriginRewritesOnlyOfficialStartRequest(t *testing.T) {
	input := `{"type":"client-request","rpcId":"r1","method":"account/startSignIn","payload":{"args":{"locale":"zh-CN","callbackOrigin":"http://wails.localhost","loginSource":"desktop"}}}`
	output, err := WithCallbackOrigin([]byte(input), "http://127.0.0.1:45678")
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Method  string `json:"method"`
		Payload struct {
			Args map[string]string `json:"args"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(output, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Method != "account/startSignIn" || decoded.Payload.Args["callbackOrigin"] != "http://127.0.0.1:45678" || decoded.Payload.Args["locale"] != "zh-CN" || decoded.Payload.Args["loginSource"] != "desktop" {
		t.Fatalf("unexpected forwarded request: %s", output)
	}
	if _, err := WithCallbackOrigin([]byte(strings.Replace(input, "account/startSignIn", "account/getState", 1)), "http://127.0.0.1:45678"); err == nil {
		t.Fatal("rewrote a different account operation")
	}
}

func TestAccountCallbackServerForwardsOnlyLoopbackOAuthCallback(t *testing.T) {
	forwarded := 0
	callback, err := NewAccountCallbackServer(func(w http.ResponseWriter, r *http.Request, generation string) {
		forwarded++
		if generation != "current-worker" || r.URL.Query().Get("code") != "code" || r.URL.Query().Get("state") != "state" {
			t.Errorf("unexpected forwarded callback: generation=%q url=%q", generation, r.URL)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer callback.Close()
	callback.SetGeneration("current-worker")

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("unexpected redirect") }}
	request, err := http.NewRequest(http.MethodGet, callback.Origin()+"/oauth/callback?code=code&state=state", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(response.Body)
	response.Body.Close()
	if readErr != nil || response.StatusCode != http.StatusOK || forwarded != 1 {
		t.Fatalf("callback returned %d after %d forwards", response.StatusCode, forwarded)
	}
	if !strings.Contains(string(body), "登录流程已完成") || !strings.Contains(string(body), `href="dsh-work://open"`) {
		t.Fatalf("callback page did not offer a return-to-app button: %s", body)
	}
	if response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("callback page is missing privacy headers: %v", response.Header)
	}

	for _, tc := range []struct {
		path string
		host string
		want int
	}{
		{path: "/other?code=code&state=state", want: http.StatusNotFound},
		{path: "/oauth/callback?code=code", want: http.StatusBadRequest},
		{path: "/oauth/callback?code=code&state=state", host: "attacker.invalid", want: http.StatusForbidden},
		{path: "/", want: http.StatusOK},
	} {
		request, err := http.NewRequest(http.MethodGet, callback.Origin()+tc.path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if tc.host != "" {
			request.Host = tc.host
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != tc.want {
			t.Fatalf("%s returned %d, want %d", tc.path, response.StatusCode, tc.want)
		}
		if tc.path == "/" {
			body, err := io.ReadAll(response.Body)
			if err != nil || !strings.Contains(string(body), `href="dsh-work://open"`) {
				t.Fatalf("loopback landing page did not offer to open dsh-work: %s (%v)", body, err)
			}
		}
		response.Body.Close()
	}
	if forwarded != 1 {
		t.Fatalf("rejected callback requests were forwarded %d times", forwarded)
	}
}

func TestAccountCallbackServerRequiresActiveGeneration(t *testing.T) {
	callback, err := NewAccountCallbackServer(func(http.ResponseWriter, *http.Request, string) {
		t.Fatal("callback forwarded without an active generation")
	})
	if err != nil {
		t.Fatal(err)
	}
	defer callback.Close()
	response, err := http.Get(callback.Origin() + "/oauth/callback?code=code&state=state")
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(response.Body)
	response.Body.Close()
	if readErr != nil || response.StatusCode != http.StatusGone || !strings.Contains(string(body), "登录会话已过期") || !strings.Contains(string(body), "dsh-work://open") {
		t.Fatalf("callback returned %d without a generation", response.StatusCode)
	}
}

func TestAccountCallbackServerForwardsRootCallbackQuery(t *testing.T) {
	forwarded := 0
	callback, err := NewAccountCallbackServer(func(w http.ResponseWriter, r *http.Request, generation string) {
		forwarded++
		if generation != "current-worker" || r.URL.Path != "/oauth/callback" || r.URL.Query().Get("code") != "code" || r.URL.Query().Get("state") != "state" {
			t.Errorf("unexpected root callback: generation=%q url=%q", generation, r.URL)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer callback.Close()
	callback.SetGeneration("current-worker")

	response, err := http.Get(callback.Origin() + "/?code=code&state=state")
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(response.Body)
	response.Body.Close()
	if readErr != nil || response.StatusCode != http.StatusOK || forwarded != 1 {
		t.Fatalf("root callback returned %d after %d forwards: %v", response.StatusCode, forwarded, readErr)
	}
	if !strings.Contains(string(body), "登录流程已完成") || !strings.Contains(string(body), `href="dsh-work://open"`) {
		t.Fatalf("root callback page did not offer a return-to-app button: %s", body)
	}
}

func TestAccountCallbackServerShowsFailurePageForOAuthError(t *testing.T) {
	callback, err := NewAccountCallbackServer(func(w http.ResponseWriter, _ *http.Request, _ string) {
		w.WriteHeader(http.StatusNoContent)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer callback.Close()
	callback.SetGeneration("current-worker")

	response, err := http.Get(callback.Origin() + "/oauth/callback?error=access_denied&state=state")
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(response.Body)
	response.Body.Close()
	if readErr != nil || response.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "登录尚未完成") || !strings.Contains(string(body), `href="dsh-work://open"`) {
		t.Fatalf("OAuth error callback returned %d without a usable failure page: %s (%v)", response.StatusCode, body, readErr)
	}
}

func TestAccountCallbackTreatsDSHCompletionRedirectAsSuccess(t *testing.T) {
	callback, err := NewAccountCallbackServer(func(w http.ResponseWriter, _ *http.Request, _ string) {
		w.Header().Set("Location", "https://chat.deepseek.com/desktop/complete")
		w.WriteHeader(http.StatusFound)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer callback.Close()
	callback.SetGeneration("current-worker")

	response, err := http.Get(callback.Origin() + "/oauth/callback?code=code&state=state")
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(response.Body)
	response.Body.Close()
	if readErr != nil || response.StatusCode != http.StatusOK || !strings.Contains(string(body), "登录流程已完成") || !strings.Contains(string(body), `href="dsh-work://open"`) {
		t.Fatalf("completion redirect produced status %d instead of the success page: %s (%v)", response.StatusCode, body, readErr)
	}
}
