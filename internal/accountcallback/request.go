package accountcallback

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"strconv"
)

const SignInRequestLimit = 64 << 10

// RewriteSignInBody replaces the browser callback origin in DSH's official
// account/startSignIn request. It accepts only the expected account RPC shape.
func RewriteSignInBody(body io.Reader, callbackOrigin string) ([]byte, error) {
	if body == nil || !validCallbackOrigin(callbackOrigin) {
		return nil, errors.New("account sign-in callback unavailable")
	}
	data, err := io.ReadAll(io.LimitReader(body, SignInRequestLimit+1))
	if err != nil || len(data) > SignInRequestLimit {
		return nil, errors.New("invalid account sign-in request")
	}
	return WithCallbackOrigin(data, callbackOrigin)
}

func WithCallbackOrigin(data []byte, origin string) ([]byte, error) {
	if !validCallbackOrigin(origin) {
		return nil, errors.New("invalid account callback origin")
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, err
	}
	var kind, method, rpcID string
	if json.Unmarshal(envelope["type"], &kind) != nil || kind != "client-request" ||
		json.Unmarshal(envelope["method"], &method) != nil || method != "account/startSignIn" ||
		json.Unmarshal(envelope["rpcId"], &rpcID) != nil || rpcID == "" {
		return nil, errors.New("unexpected account Remote request")
	}
	var payload map[string]json.RawMessage
	if json.Unmarshal(envelope["payload"], &payload) != nil || payload == nil {
		return nil, errors.New("missing account Remote payload")
	}
	var args map[string]json.RawMessage
	if json.Unmarshal(payload["args"], &args) != nil || args == nil {
		return nil, errors.New("missing account Remote arguments")
	}
	callback, err := json.Marshal(origin)
	if err != nil {
		return nil, err
	}
	args["callbackOrigin"] = callback
	argsJSON, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	payload["args"] = argsJSON
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	envelope["payload"] = payloadJSON
	return json.Marshal(envelope)
}

func validCallbackOrigin(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	ip := net.ParseIP(u.Hostname())
	port, err := strconv.Atoi(u.Port())
	return err == nil && port > 0 && port <= 65535 && ip != nil && ip.IsLoopback()
}
