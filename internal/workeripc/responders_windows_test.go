//go:build windows

package workeripc

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCarrierResponders(t *testing.T) {
	runtime, err := filepath.Abs("../../tools/dsh/node_modules/@deepseek-ai/dsh/lib/bin.js")
	if err != nil {
		t.Fatal(err)
	}
	// Match the published runtime entry selected by the other Worker tests.
	if _, err := os.Stat(runtime); err != nil {
		t.Skip("install tools/dsh dependencies to exercise upstream responders")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	transport, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer transport.Close()
	dir := t.TempDir()
	if _, err := transport.Prepare(dir, runtime, ""); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, "node", "testdata/responders.mjs", filepath.Join(dir, "host.mjs"), runtime)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); _ = cmd.Wait() }()
	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() || scanner.Text() != "carrier-ready" {
		t.Fatal("carrier fixture did not start")
	}
	client := &http.Client{Transport: transport.HTTP, Timeout: 3 * time.Second}
	request := func(method, path string, headers http.Header) (int, http.Header, string) {
		t.Helper()
		req, _ := http.NewRequestWithContext(ctx, method, Origin+path, nil)
		req.Header = headers
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		body, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		return res.StatusCode, res.Header, string(body)
	}
	for _, path := range []string{"/resource?q=opaque", "/prefix/child", "/fallback"} {
		status, headers, body := request("GET", path, nil)
		if status != 202 || headers.Get("X-Resource") != "yes" || body != "GET "+path {
			t.Fatalf("resource %s: %d %v %q", path, status, headers, body)
		}
		status, headers, body = request("HEAD", path, nil)
		if status != 202 || headers.Get("Etag") != `"fixture"` || body != "" {
			t.Fatalf("HEAD %s: %d %v %q", path, status, headers, body)
		}
		status, _, body = request("GET", path, http.Header{"If-None-Match": {`"fixture"`}})
		if status != 304 || body != "" {
			t.Fatalf("conditional %s: %d %q", path, status, body)
		}
		status, headers, _ = request("POST", path, nil)
		if status != 405 || headers.Get("Allow") != "GET, HEAD" {
			t.Fatalf("resource methods %s: %d %v", path, status, headers)
		}
	}
	if status, _, body := request("GET", "/handler", nil); status != 200 || body != "handler" {
		t.Fatalf("named handler: %d %q", status, body)
	}
	if status, _, _ := request("GET", "/failure", nil); status != 500 {
		t.Fatalf("resource failure: %d", status)
	}
	partial, err := client.Get(Origin + "/partial-failure")
	if err == nil {
		_, err = io.ReadAll(partial.Body)
		partial.Body.Close()
	}
	if err == nil {
		t.Fatal("partially written failure did not close connection")
	}
	if status, _, body := request("HEAD", "/stream", nil); status != 200 || body != "" {
		t.Fatalf("stream HEAD: %d %q", status, body)
	}
	stream, err := client.Get(Origin + "/stream")
	if err != nil {
		t.Fatal(err)
	}
	chunk := make([]byte, len("stream"))
	if _, err := io.ReadFull(stream.Body, chunk); err != nil {
		t.Fatal(err)
	}
	stream.Body.Close()
	var state struct {
		Cancellations, Aborts int
		Warnings              []string
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		_, _, body := request("GET", "/state", nil)
		if err := json.Unmarshal([]byte(body), &state); err != nil {
			t.Fatal(err)
		}
		if state.Cancellations == 2 && state.Aborts == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("stream not canceled: %+v", state)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(state.Warnings) != 2 || !strings.Contains(state.Warnings[0], "TypeError") || strings.Contains(strings.Join(state.Warnings, " "), "secret") {
		t.Fatalf("unsafe or missing failure diagnostics: %+v", state.Warnings)
	}
	request("GET", "/fallback-function", nil)
	if status, _, body := request("POST", "/fallback", nil); status != 200 || body != "function fallback" {
		t.Fatalf("function fallback: %d %q", status, body)
	}
	request("GET", "/fallback-clear", nil)
	if status, _, _ := request("GET", "/missing", nil); status != 404 {
		t.Fatalf("unclaimed fallback: %d", status)
	}
}
