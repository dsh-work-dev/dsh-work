package desktopapp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUIOpenRequestSelectsAndReturnsTheActualWindow(t *testing.T) {
	var section, window string
	handler := uiHandler(func(gotSection, gotWindow string) string {
		section, window = gotSection, gotWindow
		return "workspace"
	}, nil, nil)
	request := httptest.NewRequest(http.MethodPost, "/open", strings.NewReader(`{"section":"","window":"workspace-2"}`))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || section != "" || window != "workspace-2" {
		t.Fatalf("open request = %d, %q, %q", recorder.Code, section, window)
	}
	var result uiOpenResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil || result.Window != "workspace" {
		t.Fatalf("open result = %q, %v", recorder.Body.String(), err)
	}

	legacy := httptest.NewRequest(http.MethodPost, "/open", strings.NewReader(`"settings"`))
	legacyResult := httptest.NewRecorder()
	handler.ServeHTTP(legacyResult, legacy)
	if legacyResult.Code != http.StatusBadRequest {
		t.Fatalf("legacy string /open body status = %d", legacyResult.Code)
	}
}
