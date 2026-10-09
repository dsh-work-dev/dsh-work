package desktopbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
)

type sessionExportResult struct {
	Result  string `json:"result"`
	Message string `json:"message,omitempty"`
}

func (b *Bridge) sessionExport(ctx context.Context, route *url.URL, meta request) *http.Response {
	if route == nil || route.Path != "/__work/session-export" || meta.Method != http.MethodPost || meta.HasBody {
		return sessionExportResponse(http.StatusBadRequest, sessionExportResult{Result: "error", Message: "invalid session export request"})
	}
	query := route.Query()
	sessionIDs := query["sessionId"]
	if len(sessionIDs) != 1 || sessionIDs[0] == "" || len(sessionIDs[0]) > 4096 {
		return sessionExportResponse(http.StatusBadRequest, sessionExportResult{Result: "error", Message: "missing or invalid sessionId"})
	}
	descendants := query.Get("includeDescendants")
	if descendants == "" {
		descendants = "false"
	}
	if len(query["includeDescendants"]) > 1 || descendants != "true" && descendants != "false" {
		return sessionExportResponse(http.StatusBadRequest, sessionExportResult{Result: "error", Message: "invalid includeDescendants"})
	}
	if b.SaveSessionExport == nil || b.Client == nil || b.Origin == "" {
		return sessionExportResponse(http.StatusServiceUnavailable, sessionExportResult{Result: "error", Message: "session export is unavailable"})
	}
	sessionID := sessionIDs[0]
	filename := sessionExportFilename(sessionID)
	upstreamQuery := url.Values{"sessionId": {sessionID}, "includeDescendants": {descendants}}
	writeArchive := func(dst io.Writer) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.Origin+"/api/session.export?"+upstreamQuery.Encode(), nil)
		if err != nil {
			return err
		}
		req.Header.Set("Origin", b.Origin)
		response, err := b.Client.Do(req)
		if err != nil {
			return err
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 8<<10))
			return fmt.Errorf("session export returned HTTP %d", response.StatusCode)
		}
		mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/zip" {
			return fmt.Errorf("session export returned an invalid content type")
		}
		_, err = io.Copy(dst, response.Body)
		return err
	}
	saved, err := b.SaveSessionExport(ctx, filename, writeArchive)
	if err != nil {
		return sessionExportResponse(http.StatusBadGateway, sessionExportResult{Result: "error", Message: "session export failed"})
	}
	if !saved {
		return sessionExportResponse(http.StatusOK, sessionExportResult{Result: "cancelled"})
	}
	return sessionExportResponse(http.StatusOK, sessionExportResult{Result: "saved"})
}

func sessionExportFilename(sessionID string) string {
	var safe strings.Builder
	for _, value := range sessionID {
		if value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z' || value >= '0' && value <= '9' || value == '_' || value == '-' {
			safe.WriteRune(value)
		} else {
			safe.WriteByte('_')
		}
		if safe.Len() == 80 {
			break
		}
	}
	if safe.Len() == 0 {
		safe.WriteString("session")
	}
	return "dsh-session-" + safe.String() + ".zip"
}

func sessionExportResponse(status int, result sessionExportResult) *http.Response {
	body, _ := json.Marshal(result)
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": {"application/json; charset=utf-8"}},
		Body:       io.NopCloser(strings.NewReader(string(body))),
	}
}
