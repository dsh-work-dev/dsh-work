package desktopapp

import (
	"os"
	"runtime"
	"sync/atomic"

	"github.com/local/dsh-work/internal/logging"
)

// Temporary candidate-fixture observation. The existing hook carries method
// names only; it does not expose CDP arguments, JS, results or credentials.
func embeddedAgentProtocolObserver(instance string) func(string, string) {
	if runtime.GOOS != "linux" || embeddedDevelopmentRoot() == "" || os.Getenv("DSH_WORK_LINUX_AGENT_TEST") != "1" {
		return nil
	}
	var count atomic.Uint32
	return func(direction, method string) {
		n := count.Add(1)
		if n > 512 {
			if n == 513 {
				logging.Module(logging.ModRuntime).Info("fixture browser protocol trace truncated", "instance", instance)
			}
			return
		}
		if len(method) > 128 {
			return
		}
		for _, r := range method {
			if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '/') {
				return
			}
		}
		switch direction {
		case "command", "dispatch", "event", "result", "policy":
			logging.Module(logging.ModRuntime).Info("fixture browser protocol", "instance", instance, "direction", direction, "method", method)
		}
	}
}
