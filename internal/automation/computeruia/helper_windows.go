//go:build windows

package computeruia

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Microsoft/go-winio"
	ole "github.com/go-ole/go-ole"
	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/automation/browsernative"
	"github.com/local/dsh-work/internal/workeripc"
	"os"
	"reflect"
	"runtime"
	"syscall"
	"unsafe"
)

func RunHelper() error {
	ctx := context.Background()
	conn, e := winio.DialPipeContext(ctx, os.Getenv("DSH_WORK_BRIDGE_PIPE"))
	if e != nil {
		return e
	}
	defer conn.Close()
	if e = workeripc.AuthenticateClient(conn, os.Getenv("DSH_WORK_BRIDGE_TOKEN")); e != nil {
		return e
	}
	var req request
	if e = browsernative.ReadWireMessage(conn, &req); e != nil {
		return e
	}
	// Wails initializes its entry thread as STA. Keep that thread parked and
	// run the short-lived UIA read on a separate MTA thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	result := make(chan response, 1)
	go func() {
		id, e := inspect(req)
		reply := response{Identity: id}
		if e != nil {
			reply.Error = e.Error()
		}
		result <- reply
	}()
	reply := <-result
	return browsernative.WriteWireMessage(conn, reply)
}

// Vtable indices follow Microsoft's IUIAutomation, IUIAutomationElement and
// IUIAutomationElementArray ABI. go-ole owns COM/VARIANT/SAFEARRAY lifetimes.
func comCall(p *ole.IUnknown, index int, args ...uintptr) error {
	if p == nil {
		return errors.New("COMPUTER_UIA_UNAVAILABLE")
	}
	table := (*[64]uintptr)(unsafe.Pointer(p.RawVTable))
	all := append([]uintptr{uintptr(unsafe.Pointer(p))}, args...)
	hr, _, _ := syscall.SyscallN(table[index], all...)
	if int32(hr) < 0 {
		return fmt.Errorf("COMPUTER_UIA_PROPERTY_UNAVAILABLE: HRESULT 0x%x", hr)
	}
	return nil
}
func property(p *ole.IUnknown, id int) (any, error) {
	var v ole.VARIANT
	if e := comCall(p, 10, uintptr(id), uintptr(unsafe.Pointer(&v))); e != nil {
		return nil, e
	}
	defer v.Clear()
	if a := v.ToArray(); a != nil {
		return a.ToValueArray(), nil
	}
	return v.Value(), nil
}
func inspect(req request) (automation.ComputerElementIdentity, error) {
	empty := automation.ComputerElementIdentity{}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if e := ole.CoInitializeEx(0, ole.COINIT_MULTITHREADED); e != nil {
		return empty, e
	}
	defer ole.CoUninitialize()
	current, e := automation.InspectComputerTarget(req.Target.PID, req.Target.WindowID)
	if e != nil || current.Created != req.Target.Created || current.Executable != req.Target.Executable {
		return empty, errors.New("COMPUTER_TARGET_CHANGED")
	}
	uia, e := ole.CreateInstance(ole.NewGUID("{FF48DBA4-60EF-4201-AA87-54103EEF594E}"), ole.NewGUID("{30CBE57D-D9D0-452A-AB13-7AC5AC4825EE}"))
	if e != nil {
		return empty, e
	}
	defer uia.Release()
	var root, condition, array *ole.IUnknown
	if e = comCall(uia, 6, uintptr(req.Target.WindowID), uintptr(unsafe.Pointer(&root))); e != nil {
		return empty, e
	}
	defer root.Release()
	if e = comCall(uia, 21, uintptr(unsafe.Pointer(&condition))); e != nil {
		return empty, e
	}
	defer condition.Release()
	if e = comCall(root, 6, 7, uintptr(unsafe.Pointer(condition)), uintptr(unsafe.Pointer(&array))); e != nil {
		return empty, e
	}
	defer array.Release()
	var count int32
	if e = comCall(array, 3, uintptr(unsafe.Pointer(&count))); e != nil || count < 1 || count > 5000 {
		return empty, errors.New("COMPUTER_UIA_TREE_UNAVAILABLE")
	}
	label, ok := req.Element["label"].(string)
	if !ok || label == "" {
		return empty, errors.New("COMPUTER_ELEMENT_UNVERIFIED: accessible name required")
	}
	role, ok := req.Element["role"].(string)
	if !ok || role == "" {
		return empty, errors.New("COMPUTER_ELEMENT_UNVERIFIED: role required")
	}
	frame, ok := req.Element["frame"].(map[string]any)
	if !ok {
		return empty, errors.New("COMPUTER_ELEMENT_UNVERIFIED: bounds required")
	}
	expected := []any{frame["x"], frame["y"], frame["w"], frame["h"]}
	automationID, _ := req.Element["automation_id"].(string)
	matches := 0
	var identity automation.ComputerElementIdentity
	for i := int32(0); i < count; i++ {
		var el *ole.IUnknown
		if e = comCall(array, 4, uintptr(i), uintptr(unsafe.Pointer(&el))); e != nil {
			return empty, e
		}
		func() {
			defer el.Release()
			pid, pe := property(el, 30002)
			name, ne := property(el, 30005)
			kind, ke := property(el, 30003)
			bounds, be := property(el, 30001)
			aid, ae := property(el, 30011)
			if pe != nil || ne != nil || ke != nil || be != nil || ae != nil || pid != int32(req.Target.PID) || name != label || roleName(kind) != role || !reflect.DeepEqual(bounds, expected) || automationID != "" && aid != automationID {
				return
			}
			password, e := property(el, 30019)
			if e != nil {
				return
			}
			flag, ok := password.(bool)
			if !ok {
				return
			}
			var ids *ole.SafeArray
			if e = comCall(el, 4, uintptr(unsafe.Pointer(&ids))); e != nil || ids == nil {
				return
			}
			v := ole.NewVariant(ole.VT_ARRAY|ole.VT_I4, int64(uintptr(unsafe.Pointer(ids))))
			defer v.Clear()
			b, _ := json.Marshal(v.ToArray().ToValueArray())
			matches++
			identity = automation.ComputerElementIdentity{RuntimeID: string(b), Password: flag}
		}()
	}
	if matches != 1 {
		return empty, errors.New("COMPUTER_ELEMENT_UNVERIFIED: re-observe; native element must match uniquely")
	}
	return identity, nil
}
func roleName(value any) string {
	n, ok := value.(int32)
	if !ok {
		return ""
	}
	names := []string{"Button", "Calendar", "CheckBox", "ComboBox", "Edit", "Hyperlink", "Image", "ListItem", "List", "Menu", "MenuBar", "MenuItem", "ProgressBar", "RadioButton", "ScrollBar", "Slider", "Spinner", "StatusBar", "Tab", "TabItem", "Text", "ToolBar", "ToolTip", "Tree", "TreeItem", "Custom", "Group", "Thumb", "DataGrid", "DataItem", "Document", "SplitButton", "Window", "Pane", "Header", "HeaderItem", "Table", "TitleBar", "Separator", "SemanticZoom", "AppBar"}
	i := int(n) - 50000
	if i < 0 || i >= len(names) {
		return ""
	}
	return names[i]
}
