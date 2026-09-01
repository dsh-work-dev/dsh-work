package platform

import (
	"runtime"
	"testing"
)

func TestNewUsesNativeAdaptersOnlyOnWindows(t *testing.T) {
	dependencies := New()
	if runtime.GOOS == "windows" {
		if dependencies.Err != nil || dependencies.Supervisor == nil || dependencies.CommandExecutor == nil {
			t.Fatalf("Windows dependencies are incomplete: %+v", dependencies)
		}
		return
	}
	if dependencies.Err == nil || dependencies.Supervisor != nil || dependencies.CommandExecutor != nil {
		t.Fatalf("non-Windows dependencies must fail closed without fake adapters: %+v", dependencies)
	}
}
