package desktopbridge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"sync"
	"testing"
)

type externalOpenConn struct {
	ctx            context.Context
	metadata       []byte
	headerSent     chan struct{}
	mu             sync.Mutex
	metadataRead   bool
	creditRead     bool
	responseStatus int
}

func (c *externalOpenConn) Context() context.Context { return c.ctx }

func (c *externalOpenConn) Receive() ([]byte, error) {
	c.mu.Lock()
	if !c.metadataRead {
		c.metadataRead = true
		metadata := c.metadata
		c.mu.Unlock()
		return metadata, nil
	}
	if !c.creditRead {
		c.creditRead = true
		headerSent := c.headerSent
		c.mu.Unlock()
		select {
		case <-headerSent:
			return []byte{4}, nil
		case <-c.ctx.Done():
			return nil, c.ctx.Err()
		}
	}
	c.mu.Unlock()
	<-c.ctx.Done()
	return nil, c.ctx.Err()
}

func (c *externalOpenConn) Send(frame []byte) error {
	if len(frame) == 0 || frame[0] != 0 {
		return nil
	}
	var response struct {
		Status int `json:"status"`
	}
	if err := json.Unmarshal(frame[1:], &response); err != nil {
		return err
	}
	c.mu.Lock()
	c.responseStatus = response.Status
	c.mu.Unlock()
	select {
	case <-c.headerSent:
	default:
		close(c.headerSent)
	}
	return nil
}

func TestServeOpensSignInURLThroughNativeBrowserBridge(t *testing.T) {
	const target = "https://chat.deepseek.com/signin?state=one&client=desktop"
	metadata, err := json.Marshal(request{
		URL:        "/__work/external?url=" + url.QueryEscape(target),
		Generation: "generation-1",
		Method:     http.MethodPost,
	})
	if err != nil {
		t.Fatal(err)
	}
	conn := &externalOpenConn{ctx: context.Background(), metadata: metadata, headerSent: make(chan struct{})}
	opened := ""
	bridge := &Bridge{
		Generation: "generation-1",
		OpenExternal: func(value string) error {
			opened = value
			return nil
		},
	}
	if err := bridge.Serve(conn); err != nil {
		t.Fatalf("Serve failed: %v", err)
	}
	conn.mu.Lock()
	status := conn.responseStatus
	conn.mu.Unlock()
	if opened != target || status != http.StatusNoContent {
		t.Fatalf("native browser bridge opened %q with response status %d", opened, status)
	}
}
