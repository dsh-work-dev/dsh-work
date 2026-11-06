//go:build windows

package computeruia_test

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/automation/computeruia"
	win "github.com/local/dsh-work/internal/platform/windows"
	"golang.org/x/sys/windows"
	"os"
	"testing"
	"unsafe"
)

func TestLiveOwnedElementProbe(t *testing.T) {
	user, _ := windows.GetCurrentProcessToken().GetTokenUser()
	var length uint32
	status, _, _ := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetCurrentPackageFullName").Call(uintptr(unsafe.Pointer(&length)), 0)
	t.Log("parent", user.User.Sid.String(), "packageStatus", status, testTokenContext())
	path := os.Getenv("DSH_WORK_UIA_TEST_OBSERVATION")
	exe := os.Getenv("DSH_WORK_UIA_TEST_HELPER")
	if path == "" || exe == "" {
		t.Skip("explicit owned observation/helper required")
	}
	data, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	var observed struct {
		Structured struct {
			PID      uint32           `json:"pid"`
			Window   uint64           `json:"window_id"`
			Elements []map[string]any `json:"elements"`
		} `json:"structuredContent"`
	}
	if e = json.Unmarshal(data, &observed); e != nil {
		t.Fatal(e)
	}
	target, e := automation.InspectComputerTarget(observed.Structured.PID, observed.Structured.Window)
	if e != nil {
		t.Fatal(e)
	}
	probe := computeruia.NewProbe(win.NewJobObjectAdapter(), exe)
	checks := 0
	for _, el := range observed.Structured.Elements {
		label, _ := el["label"].(string)
		if label != "Scratch editor:" && label != "Dummy password:" {
			continue
		}
		id, e := probe(context.Background(), target, el)
		if e != nil {
			t.Fatal(label, e)
		}
		if id.Password != (label == "Dummy password:") {
			t.Fatal("password mismatch", label, id)
		}
		t.Log(label, id)
		checks++
	}
	if checks != 2 {
		t.Fatal("missing owned probe controls")
	}
}

func testTokenContext() string {
	token := windows.GetCurrentProcessToken()
	var needed uint32
	buffer := make([]uint64, 128)
	e := windows.GetTokenInformation(token, windows.TokenIntegrityLevel, (*byte)(unsafe.Pointer(&buffer[0])), uint32(len(buffer)*8), &needed)
	integrity := "unknown"
	if e == nil {
		label := (*windows.Tokenmandatorylabel)(unsafe.Pointer(&buffer[0]))
		integrity = label.Label.Sid.String()
	}
	return fmt.Sprintf("integrity=%s elevated=%t", integrity, token.IsElevated())
}
