package desktopbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"

	"testing"
	"time"
)

type sessionExportWriter struct {
	buffer bytes.Buffer
	writes int
}

func (w *sessionExportWriter) Write(p []byte) (int, error) {
	w.writes++
	return w.buffer.Write(p)
}

func TestSessionExportStreamsToHostSaveWriter(t *testing.T) {
	payload := bytes.Repeat([]byte("session-log-entry\n"), 18000)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/session.export" || r.URL.Query().Get("sessionId") != "session/one" || r.URL.Query().Get("includeDescendants") != "true" {
			t.Errorf("export request = %s %s", r.Method, r.URL.String())
		}
		w.Header().Set("Content-Type", "application/zip")
		for offset := 0; offset < len(payload); offset += 8192 {
			end := min(offset+8192, len(payload))
			if _, err := w.Write(payload[offset:end]); err != nil {
				return
			}
			w.(http.Flusher).Flush()
		}
	}))
	defer upstream.Close()

	writer := &sessionExportWriter{}
	bridge := &Bridge{
		Client:     upstream.Client(),
		Origin:     upstream.URL,
		Generation: "g1",
		SaveSessionExport: func(_ context.Context, filename string, write func(io.Writer) error) (bool, error) {
			if filename != "dsh-session-session_one.zip" {
				t.Fatalf("suggested filename = %q", filename)
			}
			return true, write(writer)
		},
	}
	requestURL := &url.URL{Path: "/__work/session-export", RawQuery: "sessionId=session%2Fone&includeDescendants=true"}
	response := bridge.sessionExport(context.Background(), requestURL, request{Method: http.MethodPost})
	defer response.Body.Close()
	var result sessionExportResult
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || result.Result != "saved" {
		t.Fatalf("result = %#v, status = %d", result, response.StatusCode)
	}
	if !bytes.Equal(writer.buffer.Bytes(), payload) || writer.writes < 2 {
		t.Fatalf("saved bytes = %d in %d writes, want streamed %d-byte archive", writer.buffer.Len(), writer.writes, len(payload))
	}
}

func TestSessionExportCancellationDoesNotFetchArchive(t *testing.T) {
	var fetched bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetched = true
		w.Header().Set("Content-Type", "application/zip")
		_, _ = io.WriteString(w, "archive")
	}))
	defer upstream.Close()
	bridge := &Bridge{
		Client: upstream.Client(), Origin: upstream.URL, Generation: "g1",
		SaveSessionExport: func(_ context.Context, _ string, _ func(io.Writer) error) (bool, error) { return false, nil },
	}
	response := bridge.sessionExport(context.Background(), &url.URL{Path: "/__work/session-export", RawQuery: "sessionId=s1"}, request{Method: http.MethodPost})
	defer response.Body.Close()
	var result sessionExportResult
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || result.Result != "cancelled" || fetched {
		t.Fatalf("cancel result = %#v, status=%d, fetched=%v", result, response.StatusCode, fetched)
	}
}

func TestSessionExportRejectsInvalidRequests(t *testing.T) {
	bridge := &Bridge{Generation: "g1", SaveSessionExport: func(context.Context, string, func(io.Writer) error) (bool, error) {
		t.Fatal("invalid request reached save dialog")
		return false, nil
	}}
	for _, testCase := range []struct {
		name string
		url  *url.URL
		meta request
	}{
		{name: "wrong method", url: &url.URL{Path: "/__work/session-export", RawQuery: "sessionId=s1"}, meta: request{Method: http.MethodGet}},
		{name: "missing session", url: &url.URL{Path: "/__work/session-export"}, meta: request{Method: http.MethodPost}},
		{name: "invalid descendants", url: &url.URL{Path: "/__work/session-export", RawQuery: "sessionId=s1&includeDescendants=yes"}, meta: request{Method: http.MethodPost}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			response := bridge.sessionExport(context.Background(), testCase.url, testCase.meta)
			defer response.Body.Close()
			if response.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusBadRequest)
			}
		})
	}

}

func TestServeSessionExportReturnsOnlyTheHostSaveResult(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conn := &testConn{ctx: ctx, in: make(chan []byte, 1), out: make(chan []byte, 1)}
	metadata, err := json.Marshal(request{URL: "/__work/session-export?sessionId=s1", Generation: "g1", Method: http.MethodPost})
	if err != nil {
		t.Fatal(err)
	}
	conn.in <- metadata
	bridge := &Bridge{
		Client: http.DefaultClient, Origin: "http://127.0.0.1", Generation: "g1",
		SaveSessionExport: func(_ context.Context, filename string, _ func(io.Writer) error) (bool, error) {
			if filename != "dsh-session-s1.zip" {
				t.Fatalf("suggested filename = %q", filename)
			}
			return true, nil
		},
	}
	done := make(chan error, 1)
	go func() { done <- bridge.Serve(conn) }()
	next := func() []byte {
		t.Helper()
		select {
		case frame := <-conn.out:
			return frame
		case <-time.After(2 * time.Second):
			t.Fatal("Worker export response stalled")
			return nil
		}
	}
	header := next()
	if len(header) == 0 || header[0] != 0 {
		t.Fatalf("response header frame = %v", header)
	}
	var responseHeader struct {
		Status int ` + 'json:"status"' + @`
	}
	if err := json.Unmarshal(header[1:], &responseHeader); err != nil || responseHeader.Status != http.StatusOK {
		t.Fatalf("response header = %#v, err=%v", responseHeader, err)
	}
	var body []byte
	for {
		conn.in <- []byte{4}
		frame := next()
		if len(frame) == 0 {
			t.Fatal("empty response frame")
		}
		switch frame[0] {
		case 1:
			body = append(body, frame[1:]...)
		case 2:
			var result sessionExportResult
			if err := json.Unmarshal(body, &result); err != nil || result.Result != "saved" {
				t.Fatalf("export result = %#v, err=%v", result, err)
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("Worker export handler did not finish")
			}
			return
		default:
			t.Fatalf("unexpected response frame %d", frame[0])
		}
	}
}
