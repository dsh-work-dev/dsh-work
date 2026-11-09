package platform

import (
	"runtime"
	"testing"
)

func TestNewUsesQualifiedPlatformAdapters(t *testing.T) {
	dependencies := New()
	if runtime.GOOS == "windows" || runtime.GOOS == "linux" || runtime.GOOS == "darwin" && dependencies.Err == nil {
		if dependencies.Err != nil || dependencies.Supervisor == nil || dependencies.CommandExecutor == nil || dependencies.FileReplacer == nil {
			t.Fatalf("native dependencies are incomplete: %+v", dependencies)
		}
		return
	}
	if dependencies.Err == nil || dependencies.Supervisor != nil || dependencies.CommandExecutor != nil || dependencies.FileReplacer != nil {
		t.Fatalf("unimplemented dependencies must fail closed without fake adapters: %+v", dependencies)
	}
}
