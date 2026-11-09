package devrunner

import (
	"context"
	"encoding/json"
	"github.com/coder/websocket"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// webviewReady only reads the fixed shell/DSH readiness facts used by the
// existing CDP inventory. It accepts neither a URL nor an expression from IPC.
func webviewReady(ctx context.Context, port string) bool {
	request, err := http.NewRequestWithContext(ctx, "GET", "http://127.0.0.1:"+port+"/json/list", nil)
	if err != nil {
		return false
	}
	client := &http.Client{Transport: &http.Transport{Proxy: nil}}
	defer client.CloseIdleConnections()
	response, err := client.Do(request)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	var pages []struct{ Type, URL, WebSocketDebuggerURL string }
	if json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&pages) != nil {
		return false
	}
	endpoint := ""
	for _, p := range pages {
		if p.Type == "page" && !strings.Contains(strings.ToLower(p.URL), "pet") {
			endpoint = p.WebSocketDebuggerURL
			break
		}
	}
	address, err := url.Parse(endpoint)
	if err != nil || address.Scheme != "ws" || address.Hostname() != "127.0.0.1" && address.Hostname() != "localhost" || address.Port() != port || address.User != nil {
		return false
	}
	conn, _, err := websocket.Dial(ctx, endpoint, nil)
	if err != nil {
		return false
	}
	defer conn.CloseNow()
	conn.SetReadLimit(1 << 20)
	send := func(id int, method string, params any) bool {
		data, _ := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
		return conn.Write(ctx, websocket.MessageText, data) == nil
	}
	if !send(1, "Runtime.enable", map[string]any{}) || !send(2, "Page.getFrameTree", map[string]any{}) {
		return false
	}
	type contextInfo struct {
		ID      int
		Origin  string
		AuxData struct {
			FrameID   string
			IsDefault bool
		}
	}
	contexts := map[int]contextInfo{}
	mainFrame := ""
	shellID, dshID := 0, 0
	shellSent, dshSent := false, false
	shellOK, dshOK := false, false
	for {
		_, raw, err := conn.Read(ctx)
		if err != nil {
			return false
		}
		var message struct {
			ID     int
			Method string
			Params struct{ Context contextInfo }
			Result json.RawMessage
			Error  json.RawMessage
		}
		if json.Unmarshal(raw, &message) != nil {
			return false
		}
		if len(message.Error) > 0 {
			return false
		}
		if message.Method == "Runtime.executionContextCreated" {
			c := message.Params.Context
			contexts[c.ID] = c
			if len(contexts) > 128 {
				return false
			}
		}
		if message.ID == 2 {
			var result struct {
				FrameTree struct{ Frame struct{ ID string } }
			}
			if json.Unmarshal(message.Result, &result) != nil {
				return false
			}
			mainFrame = result.FrameTree.Frame.ID
		}
		for id, c := range contexts {
			if c.AuxData.IsDefault && c.AuxData.FrameID == mainFrame && mainFrame != "" {
				shellID = id
			}
			if c.AuxData.IsDefault && strings.HasPrefix(c.Origin, "http://wails.localhost:48217") {
				dshID = id
			}
		}
		if shellID != 0 && !shellSent {
			shellSent = true
			if !send(3, "Runtime.evaluate", map[string]any{"contextId": shellID, "returnByValue": true, "expression": "Boolean(document.querySelector('iframe')?.src && document.readyState === 'complete')"}) {
				return false
			}
		}
		if dshID != 0 && !dshSent {
			dshSent = true
			if !send(4, "Runtime.evaluate", map[string]any{"contextId": dshID, "returnByValue": true, "expression": "Boolean(globalThis.__DSH_WORK_BROWSER_PROVIDER__?.protocolVersion === 1 && globalThis.__DSH_WORK_EMBEDDED_VERSION__ === '0.2.2-rc.1' && document.readyState === 'complete')"}) {
				return false
			}
		}
		if message.ID == 3 || message.ID == 4 {
			var result struct {
				Result           struct{ Value bool }
				ExceptionDetails json.RawMessage
			}
			if json.Unmarshal(message.Result, &result) != nil || len(result.ExceptionDetails) > 0 || !result.Result.Value {
				return false
			}
			if message.ID == 3 {
				shellOK = true
			} else {
				dshOK = true
			}
		}
		if shellOK && dshOK {
			return true
		}
	}
}
