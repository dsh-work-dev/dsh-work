package desktopbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type testConn struct {
	ctx     context.Context
	in, out chan []byte
}

func (c *testConn) Context() context.Context { return c.ctx }
func (c *testConn) Receive() ([]byte, error) {
	select {
	case v := <-c.in:
		return v, nil
	case <-c.ctx.Done():
		return nil, c.ctx.Err()
	}
}
func (c *testConn) Send(v []byte) error {
	select {
	case c.out <- v:
		return nil
	case <-c.ctx.Done():
		return c.ctx.Err()
	}
}

// Exercises streaming before EOF, upload integrity, credit-based flow control,
// and cancellation against a real HTTP peer through the production bridge.
func TestFetchFlowControlAndCancel(t *testing.T) {
	closed := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/wait" {
			w.Write([]byte("first"))
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			closed <- struct{}{}
			return
		}
		_ = http.NewResponseController(w).EnableFullDuplex()
		w.Header().Set("Content-Type", "application/octet-stream")
		io.Copy(w, r.Body)
	}))
	defer server.Close()
	bridge := &Bridge{Client: server.Client(), Origin: server.URL}
	for _, path := range []string{"/echo", "/wait"} {
		t.Run(path, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			c := &testConn{ctx: ctx, in: make(chan []byte, 1), out: make(chan []byte, 1)}
			meta, _ := json.Marshal(request{URL: path, Method: "POST", HasBody: path == "/echo"})
			c.in <- meta
			done := make(chan error, 1)
			go func() { done <- bridge.Serve(c) }()
			payload := bytes.Repeat([]byte{0, 255, 97, 128}, ChunkSize/4)
			if path == "/echo" {
				c.in <- append([]byte{1}, payload...)
			}
			// Full-duplex HTTP permits response headers before upload ack.
			headerSeen, uploadAcknowledged := false, path != "/echo"
			for !headerSeen || !uploadAcknowledged {
				select {
				case frame := <-c.out:
					switch frame[0] {
					case 0:
						if headerSeen {
							t.Fatal("duplicate headers")
						}
						headerSeen = true
					case 3:
						if uploadAcknowledged {
							t.Fatal("duplicate upload acknowledgement")
						}
						uploadAcknowledged = true
						c.in <- []byte{2}
					default:
						t.Fatal("unexpected frame before download credit")
					}
				case <-ctx.Done():
					t.Fatal("headers or upload stalled")
				}
			}
			select {
			case <-c.out:
				t.Fatal("sent data without reader credit")
			case <-time.After(30 * time.Millisecond):
			}
			var result []byte
			for {
				c.in <- []byte{4}
				select {
				case frame := <-c.out:
					if frame[0] == 2 {
						if !bytes.Equal(result, payload) {
							t.Fatalf("binary payload changed: got %d expected %d prefix=%v", len(result), len(payload), result[:min(len(result), 16)])
						}
						cancel()
						<-done
						return
					}
					if frame[0] != 1 {
						t.Fatal("invalid body frame")
					}
					result = append(result, frame[1:]...)
					if path == "/wait" {
						if string(result) != "first" {
							t.Fatal("first chunk missing")
						}
						cancel()
						<-done
						select {
						case <-closed:
							return
						case <-time.After(time.Second):
							t.Fatal("upstream not cancelled")
						}
					}
				case <-ctx.Done():
					t.Fatal("body stalled")
				}
			}
		})
	}
}
