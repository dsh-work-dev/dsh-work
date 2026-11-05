//go:build windows

package computerruntime_test

import (
	"context"
	"github.com/local/dsh-work/internal/automation/computerruntime"
	win "github.com/local/dsh-work/internal/platform/windows"
	"os"
	"os/exec"
	"testing"
	"time"
)

// Explicit read-only live check: initialize + tools/list, then Job teardown.
// It never enumerates, captures, or inputs to any desktop window.
func TestLiveCuaContract(t *testing.T) {
	deps := os.Getenv("DSH_WORK_CUA_TEST_DEPENDENCIES")
	driver := os.Getenv("DSH_WORK_CUA_TEST_DRIVER")
	if deps == "" || driver == "" {
		t.Skip("explicit locked Cua dependencies and driver required")
	}
	node, e := exec.LookPath("node")
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	factory := computerruntime.NewFactory(computerruntime.Config{Supervisor: win.NewJobObjectAdapter(), Root: t.TempDir(), Resolve: func(context.Context, string) (computerruntime.Runtime, error) {
		return computerruntime.Runtime{Node: node, Dependencies: deps, Driver: driver}, nil
	}})
	r, e := factory(ctx, "cua-driver")
	if e != nil {
		t.Fatal(e)
	}
	if e = r.Close(); e != nil {
		t.Fatal(e)
	}
}

func TestLiveAuvContract(t *testing.T) {
	deps, driver := os.Getenv("DSH_WORK_AUV_TEST_DEPENDENCIES"), os.Getenv("DSH_WORK_AUV_TEST_DRIVER")
	if deps == "" || driver == "" {
		t.Skip("explicit exact AUV runtime required")
	}
	node, e := exec.LookPath("node")
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	factory := computerruntime.NewFactory(computerruntime.Config{Supervisor: win.NewJobObjectAdapter(), Root: t.TempDir(), Resolve: func(context.Context, string) (computerruntime.Runtime, error) {
		return computerruntime.Runtime{Node: node, Dependencies: deps, Driver: driver}, nil
	}})
	r, e := factory(ctx, "auv")
	if e != nil {
		t.Fatal(e)
	}
	if e = r.Close(); e != nil {
		t.Fatal(e)
	}
}
