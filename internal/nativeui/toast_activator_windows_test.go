//go:build windows

package nativeui

import (
	"fmt"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/windows/registry"
)

func TestEnsureToastActivator(t *testing.T) {
	appName := fmt.Sprintf("dsh-work-test-%d-%d", os.Getpid(), time.Now().UnixNano())
	path := `Software\Classes\AppUserModelId\` + appName
	t.Cleanup(func() {
		if err := registry.DeleteKey(registry.CURRENT_USER, path); err != nil {
			t.Errorf("delete test notification identity: %v", err)
		}
	})
	const clsid = "{2b99be78-db90-4fa9-9880-9221bd0a6cb8}"
	assertValues := func(display, activator string) time.Time {
		t.Helper()
		key, err := registry.OpenKey(registry.CURRENT_USER, path, registry.QUERY_VALUE)
		if err != nil {
			t.Fatal(err)
		}
		defer key.Close()
		for name, want := range map[string]string{"CustomActivator": activator, "DisplayName": display} {
			got, _, err := key.GetStringValue(name)
			if err != nil || got != want {
				t.Fatalf("%s = %q, %v; want %q", name, got, err, want)
			}
		}
		info, err := key.Stat()
		if err != nil {
			t.Fatal(err)
		}
		return info.ModTime()
	}
	if err := EnsureToastActivator(appName, "DSH Work Dev", clsid); err != nil {
		t.Fatal(err)
	}
	before := assertValues("DSH Work Dev", clsid)
	if err := EnsureToastActivator(appName, "DSH Work Dev", clsid); err != nil {
		t.Fatal(err)
	}
	if after := assertValues("DSH Work Dev", clsid); !after.Equal(before) {
		t.Fatal("unchanged registration was written again")
	}
	const replacement = "{ca84fd06-433b-4c75-84ba-c74220f1f290}"
	if err := EnsureToastActivator(appName, "DSH Work", replacement); err != nil {
		t.Fatal(err)
	}
	assertValues("DSH Work", replacement)
}
