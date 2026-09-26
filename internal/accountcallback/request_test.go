package accountcallback

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRewriteSignInBodyUsesCallbackOriginAndPreservesArguments(t *testing.T) {
	input := `{"type":"client-request","rpcId":"r1","method":"account/startSignIn","payload":{"args":{"locale":"zh-CN","callbackOrigin":"http://wails.localhost","loginSource":"desktop"}}}`
	output, err := RewriteSignInBody(strings.NewReader(input), "http://127.0.0.1:45678")
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
}

func TestRewriteSignInBodyRejectsOtherOperationsAndCallbackOrigins(t *testing.T) {
	input := `{"type":"client-request","rpcId":"r1","method":"account/getState","payload":{"args":{}}}`
	if _, err := RewriteSignInBody(strings.NewReader(input), "http://127.0.0.1:45678"); err == nil {
		t.Fatal("rewrote an operation other than account/startSignIn")
	}
	input = `{"type":"client-request","rpcId":"r1","method":"account/startSignIn","payload":{"args":{}}}`
	if _, err := RewriteSignInBody(strings.NewReader(input), "http://wails.localhost"); err == nil {
		t.Fatal("accepted a non-loopback callback origin")
	}
}

func TestRewriteSignInBodyEnforcesRequestLimit(t *testing.T) {
	input := strings.Repeat("x", SignInRequestLimit+1)
	if _, err := RewriteSignInBody(strings.NewReader(input), "http://127.0.0.1:45678"); err == nil {
		t.Fatal("accepted an oversized account request")
	}
}
