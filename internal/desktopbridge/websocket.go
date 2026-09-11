package desktopbridge

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/coder/websocket"
)

// WebSocket retains plugin upgrade routes over the same IPC transport. Message
// chunks have individual acknowledgements in both directions.
func (b *Bridge) WebSocket(c ByteConn) error {
	first, err := c.Receive()
	if err != nil {
		return err
	}
	if len(first) > 32<<10 {
		return errors.New("metadata too large")
	}
	var meta struct {
		URL, Generation string
		Protocols       []string
	}
	if json.Unmarshal(first, &meta) != nil || meta.Generation != b.Generation {
		return errors.New("invalid generation")
	}
	u, err := url.Parse(meta.URL)
	if err != nil || u.IsAbs() || u.Host != "" || !strings.HasPrefix(meta.URL, "/") || strings.HasPrefix(meta.URL, "//") {
		return errors.New("invalid WebSocket path")
	}
	ctx, cancel := context.WithCancel(c.Context())
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(b.Origin, "http")+meta.URL, &websocket.DialOptions{HTTPClient: b.Client, HTTPHeader: http.Header{"Origin": {b.Origin}}, Subprotocols: meta.Protocols})
	if err != nil {
		return err
	}
	defer conn.CloseNow()
	conn.SetReadLimit(16 << 20)
	if err = c.Send(append([]byte{0}, []byte(conn.Subprotocol())...)); err != nil {
		return err
	}
	credit := make(chan struct{}, 1)
	go func() {
		defer cancel()
		var writer io.WriteCloser
		var size int
		var messageKind byte
		defer func() {
			if writer != nil {
				writer.Close()
			}
		}()
		for {
			frame, e := c.Receive()
			if e != nil || len(frame) == 0 || len(frame) > ChunkSize+1 {
				return
			}
			switch frame[0] {
			case 1, 2:
				if writer == nil {
					size = 0
					messageKind = frame[0]
					writer, e = conn.Writer(ctx, websocket.MessageType(frame[0]))
					if e != nil {
						return
					}
				}
				size += len(frame) - 1
				if size > 16<<20 || messageKind != frame[0] {
					return
				}
				if _, e = writer.Write(frame[1:]); e != nil {
					return
				}
				if c.Send([]byte{3}) != nil {
					return
				}
			case 5:
				if writer == nil {
					return
				}
				if writer.Close() != nil {
					return
				}
				writer = nil
				if c.Send([]byte{3}) != nil {
					return
				}
			case 6:
				var closing struct {
					Code   int
					Reason string
				}
				if json.Unmarshal(frame[1:], &closing) != nil || len(closing.Reason) > 123 || (closing.Code != 1000 && (closing.Code < 3000 || closing.Code > 4999)) {
					return
				}
				_ = conn.Close(websocket.StatusCode(closing.Code), closing.Reason)
				return
			case 4:
				select {
				case credit <- struct{}{}:
				default:
					return
				}
			default:
				return
			}
		}
	}()
	for {
		kind, reader, e := conn.Reader(ctx)
		if e != nil {
			var closing websocket.CloseError
			if errors.As(e, &closing) {
				data, _ := json.Marshal(map[string]any{"code": closing.Code, "reason": closing.Reason})
				_ = c.Send(append([]byte{6}, data...))
			}
			return e
		}
		for {
			select {
			case <-credit:
			case <-ctx.Done():
				return ctx.Err()
			}
			buf := make([]byte, ChunkSize+1)
			buf[0] = byte(kind)
			n, e := reader.Read(buf[1:])
			if n > 0 {
				if c.Send(buf[:n+1]) != nil {
					return io.ErrClosedPipe
				}
			}
			if e == io.EOF {
				if n > 0 {
					select {
					case <-credit:
					case <-ctx.Done():
						return ctx.Err()
					}
				}
				if c.Send([]byte{5}) != nil {
					return io.ErrClosedPipe
				}
				break
			}
			if e != nil {
				return e
			}
			if n == 0 {
				credit <- struct{}{}
			}
		}
	}
}
