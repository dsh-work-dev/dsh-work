// Package desktopbridge adapts Fetch bodies to bounded Wails byte streams.
package desktopbridge

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const ChunkSize = 64 << 10

type Bridge struct {
	Client       *http.Client
	Origin       string
	Generation   string
	OpenExternal func(string) error
}

// ByteConn permits exercising the same Fetch implementation without a WebView.
type ByteConn interface {
	Context() context.Context
	Receive() ([]byte, error)
	Send([]byte) error
}

type request struct {
	URL        string      `json:"url"`
	Generation string      `json:"generation"`
	Method     string      `json:"method"`
	Headers    http.Header `json:"headers"`
	HasBody    bool        `json:"hasBody"`
}

func (b *Bridge) Serve(c ByteConn) error {
	first, err := c.Receive()
	if err != nil {
		return err
	}
	if len(first) > 32<<10 {
		return errors.New("request metadata too large")
	}
	var meta request
	if err := json.Unmarshal(first, &meta); err != nil {
		return err
	}
	if meta.Generation != b.Generation {
		return errors.New("Worker generation expired")
	}
	u, err := url.Parse(meta.URL)
	if err != nil || u.IsAbs() || u.Host != "" || !strings.HasPrefix(meta.URL, "/") || strings.HasPrefix(meta.URL, "//") {
		return errors.New("invalid Worker path")
	}
	if meta.Method == "CONNECT" || meta.Method == "TRACE" {
		return errors.New("unsupported method")
	}
	ctx, cancel := context.WithCancel(c.Context())
	defer cancel()
	r, w := io.Pipe()
	defer r.Close()
	defer w.Close()
	var body io.Reader
	if meta.HasBody {
		body = r
	}
	req, err := http.NewRequestWithContext(ctx, meta.Method, b.Origin+meta.URL, body)
	if err != nil {
		return err
	}
	req.Header = meta.Headers
	if req.Header == nil {
		req.Header = make(http.Header)
	}
	for _, name := range []string{"Cookie", "Host", "Connection", "Upgrade", "Transfer-Encoding", "Content-Length", "Proxy-Authorization"} {
		req.Header.Del(name)
	}
	req.Header.Set("Origin", b.Origin)
	credit := make(chan struct{}, 1)
	// Read control frames independently of a blocked upload. A duplex Worker
	// can fill its response pipe while waiting for our next download credit;
	// processing that credit must not wait for the upload pipe to drain.
	upload := make(chan []byte, 1)
	go func() {
		defer w.Close()
		for {
			select {
			case <-ctx.Done():
				return
			case frame := <-upload:
				if frame[0] == 2 {
					return
				}
				if _, err := w.Write(frame[1:]); err != nil {
					cancel()
					return
				}
				if err := c.Send([]byte{3}); err != nil {
					cancel()
					return
				}
			}
		}
	}()
	go func() {
		defer cancel()
		defer w.Close()
		ended := !meta.HasBody
		for {
			frame, err := c.Receive()
			if err != nil {
				w.CloseWithError(err)
				return
			}
			if len(frame) == 0 || len(frame) > ChunkSize+1 {
				w.CloseWithError(errors.New("invalid data frame"))
				return
			}
			switch frame[0] {
			case 1:
				if ended {
					return
				}
				select {
				case upload <- frame:
				default:
					return
				}
			case 2:
				if ended || len(frame) != 1 {
					return
				}
				ended = true
				select {
				case upload <- frame:
				default:
					return
				}
			case 4:
				if len(frame) != 1 {
					return
				}
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
	var response *http.Response
	if u.Path == "/__work/external" {
		target, e := url.Parse(u.Query().Get("url"))
		if e != nil || len(u.Query().Get("url")) > 4096 || target.User != nil || (target.Scheme != "https" && target.Scheme != "http") || target.Host == "" || b.OpenExternal == nil || meta.Method != "POST" {
			return errors.New("invalid external URL")
		}
		if err = b.OpenExternal(target.String()); err != nil {
			return err
		}
		response = &http.Response{StatusCode: 204, Header: make(http.Header), Body: http.NoBody}
	} else {
		response, err = b.Client.Do(req)
	}
	if err != nil {
		return err
	}
	defer response.Body.Close()
	response.Header.Del("Set-Cookie")
	header, _ := json.Marshal(map[string]any{"status": response.StatusCode, "headers": response.Header})
	if err = c.Send(append([]byte{0}, header...)); err != nil {
		return err
	}
	for {
		select {
		case <-credit:
		case <-ctx.Done():
			return ctx.Err()
		}
		buf := make([]byte, ChunkSize+1)
		buf[0] = 1
		n, readErr := response.Body.Read(buf[1:])
		if n > 0 {
			if err = c.Send(buf[:n+1]); err != nil {
				return err
			}
		}
		if readErr == io.EOF {
			return c.Send([]byte{2})
		}
		if readErr != nil {
			return readErr
		}
		if n == 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Millisecond):
			}
			select {
			case credit <- struct{}{}:
			default:
			}
		}
	}
}
