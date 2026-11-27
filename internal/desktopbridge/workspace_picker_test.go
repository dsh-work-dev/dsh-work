package desktopbridge

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestWorkspacePickerOnlyReturnsNativeSelection(t *testing.T) {
	calls := 0
	choice := ""
	bridge := &Bridge{PickWorkspaceDirectory: func(context.Context) (string, error) {
		calls++
		return choice, nil
	}}
	route := &url.URL{Path: "/__work/workspace-directory", RawQuery: "path=%2Fother"}
	ctx := context.Background()
	response := bridge.workspaceDirectory(ctx, route, request{Method: http.MethodPost})
	if response.StatusCode != http.StatusBadRequest || calls != 0 {
		t.Fatal("a Worker must not supply a directory or an initial location")
	}
	route.RawQuery = ""
	for _, sample := range []struct{ selected, json string }{
		{"", "null"},
		{"/Users/test/workspace", `"/Users/test/workspace"`},
	} {
		choice = sample.selected
		response = bridge.workspaceDirectory(ctx, route, request{Method: http.MethodPost})
		body, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK || err != nil || string(body) != sample.json {
			t.Fatalf("picker response = %d %q (%v)", response.StatusCode, body, err)
		}
	}
}

func TestWorkspacePickerHookIsServedOnlyWhenAvailable(t *testing.T) {
	bridge := &Bridge{}
	for _, available := range []bool{false, true} {
		if available {
			bridge.PickWorkspaceDirectory = func(context.Context) (string, error) { return "", nil }
		}
		response := httptest.NewRecorder()
		bridge.Assets(response, httptest.NewRequest(http.MethodGet, "/__work/origin.js", nil))
		if strings.Contains(response.Body.String(), "__DSH_DIRECTORY_PICKER__") != available {
			t.Fatalf("picker hook availability = %t", available)
		}
	}
}
