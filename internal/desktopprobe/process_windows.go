//go:build windows

package desktopprobe

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"syscall"
	"time"
)

func Processes(worker uint32, daemonPID, uiPID int) any {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	// Only numeric PIDs enter this script. Command lines and unrelated process
	// metadata are never returned. WebView subprocesses are outside this sample.
	script := fmt.Sprintf(`$all=Get-CimInstance Win32_Process
$ids=@(%d)
do {$next=@($all | Where-Object {$ids -contains $_.ParentProcessId -and $ids -notcontains $_.ProcessId} | ForEach-Object {$_.ProcessId}); $ids+=$next} while($next.Count)
$ids+=%d,%d
$tcp=@(Get-NetTCPConnection -State Listen -ErrorAction SilentlyContinue | Where-Object {$ids -contains $_.OwningProcess} | Select-Object OwningProcess,LocalAddress,LocalPort)
$udp=@(Get-NetUDPEndpoint -ErrorAction SilentlyContinue | Where-Object {$ids -contains $_.OwningProcess} | Select-Object OwningProcess,LocalAddress,LocalPort)
$processes=@(Get-Process -Id $ids -ErrorAction SilentlyContinue | Select-Object Id,ProcessName,WorkingSet64,CPU)
@{tcpListeners=$tcp;udpEndpoints=$udp;processes=$processes} | ConvertTo-Json -Depth 4 -Compress`, worker, daemonPID, uiPID)
	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000}
	b, err := cmd.Output()
	if err != nil {
		return map[string]any{"error": "process sampling failed"}
	}
	var data any
	if json.Unmarshal(b, &data) != nil {
		return map[string]any{"error": "invalid process sample"}
	}
	return data
}
