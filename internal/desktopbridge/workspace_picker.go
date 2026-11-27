package desktopbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
)

func (b *Bridge) workspaceDirectory(ctx context.Context, route *url.URL, meta request) *http.Response {
	response := &http.Response{StatusCode: http.StatusBadRequest, Header: http.Header{"Content-Type": {"application/json"}}, Body: http.NoBody}
	if meta.Method != http.MethodPost || meta.HasBody || route.RawQuery != "" || route.Fragment != "" {
		return response
	}
	response.StatusCode = http.StatusServiceUnavailable
	if b.PickWorkspaceDirectory == nil || ctx.Err() != nil {
		return response
	}
	path, err := b.PickWorkspaceDirectory(ctx)
	if err != nil || ctx.Err() != nil {
		return response
	}
	var result *string
	if path != "" {
		result = &path
	}
	body, _ := json.Marshal(result)
	response.StatusCode = http.StatusOK
	response.Body = io.NopCloser(bytes.NewReader(body))
	return response
}
