package desktopapp

import (
	"github.com/local/dsh-work/internal/automation"
	"testing"
)

func TestComputerPointerRetainsOnlyItsLeaseTarget(t *testing.T) {
	p := &computerControlWindow{}
	target := automation.ComputerTarget{PID: 1, WindowID: 2, Created: 3, WindowIdentity: "one"}
	use := &automation.ComputerUse{Session: "a", Lifetime: "life", Target: &target}
	s := &automation.ComputerSnapshot{Use: use}
	p.rememberLocation(s)
	if p.pointKnown {
		t.Fatal("observation invented a point")
	}
	use.Running = true
	use.Rect = map[string]any{"x": 12.0, "y": 34.0}
	p.rememberLocation(s)
	use.Running = false
	use.Rect = nil
	p.rememberLocation(s)
	if !p.pointKnown || p.pointX != 12 || p.pointY != 34 {
		t.Fatal("fast action feedback lost")
	}
	moved := target
	moved.WindowIdentity = "two"
	use.Target = &moved
	p.rememberLocation(s)
	if p.pointKnown {
		t.Fatal("changed target retained old point")
	}
	use.Running = true
	use.Rect = map[string]any{"x": 56.0, "y": 78.0}
	p.rememberLocation(s)
	use.Lifetime = "new"
	use.Running = false
	use.Rect = nil
	p.rememberLocation(s)
	if p.pointKnown {
		t.Fatal("new lease retained old point")
	}
	use.Running = true
	use.Rect = map[string]any{"x": 56.0, "y": 78.0}
	p.rememberLocation(s)
	use.Stopping = true
	p.rememberLocation(s)
	if p.pointKnown {
		t.Fatal("Stop retained point")
	}
	use.Stopping = false
	p.rememberLocation(s)
	p.rememberLocation(nil)
	if p.pointKnown {
		t.Fatal("release retained point")
	}
}
