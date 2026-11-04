// Package browsernative adapts native messaging to the Go automation owner.
// Setup and per-session wire transports share one authenticated native port.
package browsernative

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/browserhost"
	"github.com/local/dsh-work/internal/daemon"
	"github.com/local/dsh-work/internal/workeripc"
)

var errNativeMessageTooLarge = errors.New("BROWSER_NATIVE_MESSAGE_TOO_LARGE")

const HostVersion = "0.2.0"
const MaxSetupBytes = 64 << 10
const Origin = "chrome-extension://" + automation.DevelopmentExtensionID + "/"
const BasePath = "/browser-native"

type admission struct {
	Token string `json:"token"`
}
type admissionTicket struct {
	expires    time.Time
	browser    string
	executable string
}
type Handler struct {
	Service *automation.Service
	Browser string
	Context context.Context
	Observe func(string, map[string]any)
	mu      sync.Mutex
	tickets map[string]admissionTicket
	active  int
}

// ServeHTTP must be mounted only on the existing current-user SID ACL daemon
// transport, not a TCP server. Admission secrets stay on that pipe and in memory.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Origin") != "" {
		http.Error(w, "forbidden", 403)
		return
	}
	if r.URL.Path == BasePath+"/admit" && r.Method == "POST" {
		var v struct {
			Origin     string `json:"origin"`
			Protocol   int    `json:"protocol"`
			Browser    string `json:"browser"`
			Executable string `json:"executable"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, MaxSetupBytes)).Decode(&v) != nil || v.Origin != Origin || v.Protocol != automation.BrowserProtocol {
			http.Error(w, "BROWSER_SOURCE_OR_PROTOCOL_REJECTED", 403)
			return
		}
		browser := h.Browser
		if browser == "" {
			browser = v.Browser
		}
		if browser != "chrome" && browser != "edge" {
			http.Error(w, "BROWSER_SOURCE_OR_PROTOCOL_REJECTED", 403)
			return
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		if h.tickets == nil {
			h.tickets = map[string]admissionTicket{}
		}
		for k, ticket := range h.tickets {
			if time.Now().After(ticket.expires) {
				delete(h.tickets, k)
			}
		}
		if len(h.tickets)+h.active >= 64 {
			http.Error(w, "BROWSER_CONNECTION_LIMIT", 429)
			return
		}
		token := rand.Text()
		h.tickets[token] = admissionTicket{expires: time.Now().Add(30 * time.Second), browser: browser, executable: v.Executable}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(admission{token})
		return
	}
	if r.URL.Path != BasePath+"/connect" || r.Method != "GET" {
		http.NotFound(w, r)
		return
	}
	token := r.Header.Get("X-DSH-Native-Admission")
	h.mu.Lock()
	ticket, ok := h.tickets[token]
	delete(h.tickets, token)
	if !ok || !time.Now().Before(ticket.expires) {
		h.mu.Unlock()
		http.Error(w, "BROWSER_ADMISSION_EXPIRED", 403)
		return
	}
	h.active++
	h.mu.Unlock()
	defer func() { h.mu.Lock(); h.active--; h.mu.Unlock() }()
	ws, e := websocket.Accept(w, r, nil)
	if e != nil {
		return
	}
	defer ws.CloseNow()
	ws.SetReadLimit(MaxFromExtensionBytes + 4)
	ctx := h.Context
	if ctx == nil {
		ctx = r.Context()
	}
	conn := websocket.NetConn(ctx, ws, websocket.MessageBinary)
	defer conn.Close()
	if workeripc.AuthenticateServer(conn, token) != nil {
		return
	}
	var hello struct {
		Kind string `json:"kind"`
		automation.BrowserHello
	}
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if ReadMessage(conn, &hello) != nil || hello.Kind != "hello" {
		return
	}
	conn.SetReadDeadline(time.Time{})
	if hello.ExtensionID != automation.DevelopmentExtensionID {
		WriteMessage(conn, map[string]any{"kind": "error", "error": "BROWSER_EXTENSION_SOURCE_REJECTED"})
		return
	}
	peer := &nativePeer{service: h.Service, conn: conn, routes: map[string]*nativeRoute{}}
	var id string
	if hello.WireProtocol == 2 {
		id, e = h.Service.ConnectBrowserHost(ticket.browser, ticket.executable, hello.BrowserHello, func(connection string) automation.BrowserPeer { peer.id = connection; return peer })
	} else {
		id, e = h.Service.BrowserConnected(ticket.browser, hello.BrowserHello)
		peer.id = id
	}
	if e != nil {
		WriteMessage(conn, map[string]any{"kind": "error", "error": e.Error()})
		return
	}
	defer h.Service.BrowserDisconnected(id)
	if h.Observe != nil {
		h.Observe("native-connected", map[string]any{"connection": id, "instance": hello.Instance, "extensionId": hello.ExtensionID, "browser": ticket.browser})
	}
	defer func() {
		if h.Observe != nil {
			h.Observe("native-disconnected", map[string]any{"connection": id})
		}
	}()
	terminal := errRouteClosed
	defer func() { peer.disconnect(terminal) }()
	if peer.write(map[string]any{"kind": "welcome", "protocol": automation.BrowserProtocol, "hostVersion": HostVersion, "wireProtocol": 2, "connection": id}) != nil {
		return
	}
	pending := ""
	for {
		var request struct {
			ID        string          `json:"id"`
			Kind      string          `json:"kind"`
			Name      string          `json:"name"`
			Candidate string          `json:"candidate"`
			Route     string          `json:"route"`
			Payload   json.RawMessage `json:"payload"`
			Error     string          `json:"error"`
		}
		var raw json.RawMessage
		if err := readMessageLimit(conn, &raw, MaxFromExtensionBytes); err != nil {
			terminal = err
			return
		}
		if json.Unmarshal(raw, &request) != nil {
			return
		}
		if request.Kind == "native-error" && request.Error == errNativeMessageTooLarge.Error() {
			terminal = errNativeMessageTooLarge
			if h.Observe != nil {
				h.Observe("native-error", map[string]any{"error": request.Error, "connection": id})
			}
			return
		}
		response := map[string]any{"id": request.ID, "kind": "result"}
		if request.Kind != "wire" && request.Kind != "closed" && request.Kind != "route-error" && len(raw) > MaxSetupBytes {
			response["error"] = errNativeMessageTooLarge.Error()
			if peer.write(response) != nil {
				return
			}
			continue
		}
		switch request.Kind {
		case "wire", "closed", "route-error":
			if hello.WireProtocol != 2 {
				return
			}
			peer.receive(request.Kind, request.Route, request.Payload, request.Error)
			continue
		case "bind":
			p, err := h.Service.RequestBrowserBinding(id, request.Name)
			if err != nil {
				response["error"] = err.Error()
			} else {
				pending = p.ID
				response["candidate"] = p.ID
				response["expires"] = p.Expires.UnixMilli()
			}
		case "cancel":
			if pending == "" || request.Candidate != pending {
				response["error"] = "BROWSER_BINDING_REQUEST_INVALID"
			} else {
				h.Service.CancelBrowserBinding(pending)
				pending = ""
			}
		case "status":
			response["status"] = h.Service.BrowserConnectionStatus(id)
		default:
			response["error"] = "BROWSER_SETUP_OPERATION_REJECTED"
		}
		if peer.write(response) != nil {
			return
		}
	}
}

func ReadMessage(r io.Reader, v any) error {
	return readMessageLimit(r, v, MaxSetupBytes)
}
func readMessageLimit(r io.Reader, v any, limit uint32) error {
	var n uint32
	if e := binary.Read(r, binary.LittleEndian, &n); e != nil {
		return e
	}
	if n == 0 || n > limit {
		return errNativeMessageTooLarge
	}
	b := make([]byte, n)
	if _, e := io.ReadFull(r, b); e != nil {
		return e
	}
	return json.Unmarshal(b, v)
}
func WriteMessage(w io.Writer, v any) error {
	return writeMessageLimit(w, v, MaxSetupBytes)
}

// ReadWireMessage/WriteWireMessage share bounded framing with the owned MCP
// adapter pipe. Browser-direction limits are still applied separately above.
func ReadWireMessage(r io.Reader, v any) error { return readMessageLimit(r, v, MaxFromExtensionBytes) }
func WriteWireMessage(w io.Writer, v any) error {
	return writeMessageLimit(w, v, MaxFromExtensionBytes)
}
func writeMessageLimit(w io.Writer, v any, limit int) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	return writeFrameLimit(w, b, limit)
}

// A relay forwards already-decoded JSON bytes without re-encoding. Re-marshaling
// RawMessage can expand HTML characters and spuriously exceed the native budget.
func writeFrameLimit(w io.Writer, b []byte, limit int) error {
	if len(b) > limit {
		return errNativeMessageTooLarge
	}
	frame := make([]byte, len(b)+4)
	binary.LittleEndian.PutUint32(frame, uint32(len(b)))
	copy(frame[4:], b)
	for len(frame) > 0 {
		n, e := w.Write(frame)
		if e != nil {
			return e
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		frame = frame[n:]
	}
	return nil
}

// Run is entered before logging/UI startup. stdin/stdout contain only native
// frames; caller errors go to stderr. The client uses the existing daemon pipe.
func Run(ctx context.Context, client *daemon.Client, origin string, in io.Reader, out io.Writer) error {
	return RunForBrowser(ctx, client, origin, "", in, out)
}

// RunForBrowser receives family from the native parent-process adapter. It is
// sealed into the admission ticket, never overridden by a later extension hello.
func RunForBrowser(ctx context.Context, client *daemon.Client, origin, browser string, in io.Reader, out io.Writer) error {
	return RunForParent(ctx, client, origin, browserhost.ParentInfo{Browser: browser}, in, out)
}
func RunForParent(ctx context.Context, client *daemon.Client, origin string, parent browserhost.ParentInfo, in io.Reader, out io.Writer) error {
	if origin != Origin {
		return errors.New("BROWSER_EXTENSION_SOURCE_REJECTED")
	}
	var a admission
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	e := client.JSON(bounded, BasePath+"/admit", map[string]any{"origin": origin, "protocol": automation.BrowserProtocol, "browser": parent.Browser, "executable": parent.Executable}, &a)
	cancel()
	if e != nil {
		return e
	}
	ws, _, e := websocket.Dial(ctx, "ws://daemon.local"+BasePath+"/connect", &websocket.DialOptions{HTTPClient: client.HTTP, HTTPHeader: http.Header{"X-DSH-Native-Admission": {a.Token}}})
	if e != nil {
		return e
	}
	defer ws.CloseNow()
	ws.SetReadLimit(MaxFromExtensionBytes + 4)
	conn := websocket.NetConn(ctx, ws, websocket.MessageBinary)
	defer conn.Close()
	if e := workeripc.AuthenticateClient(conn, a.Token); e != nil {
		return e
	}
	// Reads are size-guarded before allocation; no unbounded io.Copy of stdin.
	done := make(chan error, 2)
	pump := func(dst io.Writer, src io.Reader, limit uint32, report bool) {
		for {
			var v json.RawMessage
			if e := readMessageLimit(src, &v, limit); e != nil {
				if report && errors.Is(e, errNativeMessageTooLarge) {
					conn.SetWriteDeadline(time.Now().Add(time.Second))
					_ = WriteMessage(conn, map[string]any{"kind": "native-error", "error": errNativeMessageTooLarge.Error()})
				}
				done <- e
				return
			}
			if e := writeFrameLimit(dst, v, int(limit)); e != nil {
				done <- e
				return
			}
		}
	}
	go pump(conn, in, MaxFromExtensionBytes, true)
	go pump(out, conn, MaxToExtensionBytes, false)
	select {
	case e := <-done:
		return e
	case <-ctx.Done():
		return ctx.Err()
	}
}
