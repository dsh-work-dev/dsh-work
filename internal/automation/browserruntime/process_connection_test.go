package browserruntime

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/local/dsh-work/internal/supervisor"
	"github.com/local/dsh-work/internal/workeripc"
)

type instanceConnectionWorker struct {
	supervisor.Worker
	pid     uint32
	started uint64
}

func (w instanceConnectionWorker) PrivateConnectionProcess() (uint32, uint64) {
	return w.pid, w.started
}
func TestRuntimeProcessBindingRejectsZeroWithoutFallback(t *testing.T) {
	for _, worker := range []instanceConnectionWorker{{pid: 0, started: 1}, {pid: 1, started: 0}} {
		// A nil carrier would panic if the new-instance branch fell back to Connect.
		conn, err := connectRuntimeProcess(context.Background(), nil, worker)
		if conn != nil || err == nil || !strings.Contains(err.Error(), "BROWSER_RUNTIME_PROCESS_INSTANCE_REQUIRED") {
			t.Fatal("invalid instance did not fail closed", err)
		}
	}
}
func TestRuntimeUnmarkedWorkerKeepsOriginalCarrier(t *testing.T) {
	transport, err := workeripc.New()
	if err != nil {
		t.Fatal(err)
	}
	defer transport.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// The old branch still reaches the carrier, rather than requiring process
	// metadata from legacy Workers. No child is needed for this cancelled dial.
	_, err = connectRuntimeProcess(ctx, transport, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatal("legacy carrier contract changed", err)
	}
}
