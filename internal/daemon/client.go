// Package daemon connects local clients to the per-user background owner.
package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

const Origin = "http://daemon.local"
const Protocol = 1

type Client struct{ HTTP *http.Client }

func NewClient(identity string) *Client {
	return &Client{HTTP: &http.Client{Transport: &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return dial(ctx, identity) }, MaxIdleConnsPerHost: 16, IdleConnTimeout: 30 * time.Second}}}
}
func (c *Client) Close() { c.HTTP.CloseIdleConnections() }

func (c *Client) JSON(ctx context.Context, path string, input, output any) error {
	data, err := json.Marshal(input)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, Origin+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("connect background: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("background: %s", body)
	}
	if output == nil {
		_, err = io.Copy(io.Discard, resp.Body)
		return err
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(output)
}

type Call struct {
	Service, Method, Surface string
	Args                     []json.RawMessage
}
type Result struct {
	Value json.RawMessage
	Error string
}

func (c *Client) Call(ctx context.Context, service, method, surface string, args []any, output any) error {
	call := Call{Service: service, Method: method, Surface: surface}
	for _, arg := range args {
		data, err := json.Marshal(arg)
		if err != nil {
			return err
		}
		call.Args = append(call.Args, data)
	}
	var result Result
	if err := c.JSON(ctx, "/control", call, &result); err != nil {
		return err
	}
	if result.Error != "" {
		return fmt.Errorf("%s", result.Error)
	}
	if output != nil && len(result.Value) > 0 {
		return json.Unmarshal(result.Value, output)
	}
	return nil
}
